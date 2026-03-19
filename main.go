package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func defaultCacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("cannot determine home dir: %v", err)
	}
	return filepath.Join(home, ".skillweave")
}

const serverInstructions = `Skill Updater MCP server. Each registered skill appears as its own tool (skill_<name>). Read the relevant skill before starting work.

DURING THE SESSION:
Pay attention to when the user corrects you or you discover something new. These are learnings that should be captured in the skill.

WHEN TO CALL skill_update:
- You have been corrected multiple times on the same topic
- You discovered a new pattern, fix, or tip that would help future sessions
- The user explicitly asks you to update the skill
- The session is ending and you accumulated meaningful learnings

Generate the full updated SKILL.md content yourself — you have the original from the skill tool and you know what you learned. Pass both the learnings list and the updated content to skill_update.

skill_update writes locally. Call skill_push only when the user asks to share changes with the team.`

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "register":
			cmdRegister(os.Args[2:])
			return
		case "unregister":
			cmdUnregister(os.Args[2:])
			return
		case "list":
			cmdList(os.Args[2:])
			return
		case "push":
			cmdPush(os.Args[2:])
			return
		case "ledger":
			cmdLedger(os.Args[2:])
			return
		}
	}

	httpAddr := flag.String("http", "", "HTTP listen address (e.g. :8080). If empty, run in stdio mode.")
	logTransport := flag.Bool("log-transport", false, "Log transport frames to stderr")
	cacheDir := flag.String("cache-dir", "", "Cache directory (default ~/.skillweave)")
	flag.Parse()

	if *cacheDir == "" {
		*cacheDir = defaultCacheDir()
	}

	cfg, err := LoadConfig(*cacheDir)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "skillweave",
		Version: "0.3.0",
	}, &mcp.ServerOptions{
		Instructions: serverInstructions,
	})

	sessions := NewSessionManager()
	registerTools(srv, sessions, cfg, *cacheDir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "shutting down...")
		cancel()
	}()

	if *httpAddr != "" {
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
		sseHandler := mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return srv }, nil)
		http.Handle("/mcp", handler)
		http.Handle("/sse", sseHandler)
		log.Printf("skillweave listening on %s", *httpAddr)
		if err := http.ListenAndServe(*httpAddr, nil); err != nil {
			log.Fatalf("http server: %v", err)
		}
		return
	}

	var transport mcp.Transport
	transport = &mcp.StdioTransport{}
	if *logTransport {
		transport = &mcp.LoggingTransport{
			Transport: &mcp.StdioTransport{},
			Writer:    os.Stderr,
		}
	}
	if err := srv.Run(ctx, transport); err != nil && ctx.Err() == nil {
		log.Fatalf("server error: %v", err)
	}
}

// --- CLI subcommands ---

func cmdRegister(args []string) {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	name := fs.String("name", "", "Skill name (auto-derived from path if omitted)")
	cacheDir := fs.String("cache-dir", "", "Cache directory (default ~/.skillweave)")
	repoURL := fs.String("repo", "", "Git repo URL (if not using a GitHub blob URL)")
	skillPath := fs.String("path", "", "Path to SKILL.md in repo (if not using a GitHub blob URL)")
	localPath := fs.String("local-path", "", "Local checkout root (e.g. /Users/igor/Projects/zos-porting). skill_update will write here too.")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: skillweave register [flags] <github-url>\n\n")
		fmt.Fprintf(os.Stderr, "Register a SKILL.md for automatic tracking.\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  skillweave register https://github.com/user/repo/blob/main/skills/my-skill/SKILL.md\n")
		fmt.Fprintf(os.Stderr, "  skillweave register --local-path ~/Projects/zos-porting https://github.com/user/repo/blob/main/skills/my-skill/SKILL.md\n")
		fmt.Fprintf(os.Stderr, "  skillweave register --repo git@github.com:user/repo.git --path skills/my-skill/SKILL.md\n\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if *cacheDir == "" {
		*cacheDir = defaultCacheDir()
	}

	rURL, sPath := *repoURL, *skillPath
	if rURL == "" || sPath == "" {
		if fs.NArg() < 1 {
			fs.Usage()
			os.Exit(1)
		}
		parsed, parsedPath, err := ParseGitHubURL(fs.Arg(0))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if rURL == "" {
			rURL = parsed
		}
		if sPath == "" {
			sPath = parsedPath
		}
	}

	if *localPath == "" {
		*localPath = DetectLocalPath(rURL)
	}

	skillName := *name
	if skillName == "" {
		skillName = DeriveSkillName(sPath)
	}

	cfg, err := LoadConfig(*cacheDir)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	skill := RegisteredSkill{
		Name:      skillName,
		RepoURL:   rURL,
		SkillPath: sPath,
		LocalPath: *localPath,
	}
	cfg.AddSkill(skill)

	if err := SaveConfig(*cacheDir, cfg); err != nil {
		log.Fatalf("save config: %v", err)
	}

	fmt.Printf("Registered skill %q\n", skillName)
	fmt.Printf("  repo:  %s\n", rURL)
	fmt.Printf("  path:  %s\n", sPath)
	if *localPath != "" {
		fmt.Printf("  local: %s\n", filepath.Join(*localPath, sPath))
	}
}

func cmdUnregister(args []string) {
	fs := flag.NewFlagSet("unregister", flag.ExitOnError)
	cacheDir := fs.String("cache-dir", "", "Cache directory (default ~/.skillweave)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: skillweave unregister <name>\n\n")
		fmt.Fprintf(os.Stderr, "Remove a registered skill.\n\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if fs.NArg() < 1 {
		fs.Usage()
		os.Exit(1)
	}

	if *cacheDir == "" {
		*cacheDir = defaultCacheDir()
	}

	cfg, err := LoadConfig(*cacheDir)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	name := fs.Arg(0)
	if !cfg.RemoveSkill(name) {
		fmt.Fprintf(os.Stderr, "Skill %q not found\n", name)
		os.Exit(1)
	}

	if err := SaveConfig(*cacheDir, cfg); err != nil {
		log.Fatalf("save config: %v", err)
	}

	fmt.Printf("Unregistered skill %q\n", name)
}

func cmdList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	cacheDir := fs.String("cache-dir", "", "Cache directory (default ~/.skillweave)")
	fs.Parse(args)

	if *cacheDir == "" {
		*cacheDir = defaultCacheDir()
	}

	cfg, err := LoadConfig(*cacheDir)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	fmt.Println(FormatSkillList(cfg))
}

func cmdLedger(args []string) {
	fs := flag.NewFlagSet("ledger", flag.ExitOnError)
	cacheDir := fs.String("cache-dir", "", "Cache directory (default ~/.skillweave)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: skillweave ledger <action> <skill-name> [entry-id]\n\n")
		fmt.Fprintf(os.Stderr, "Actions:\n")
		fmt.Fprintf(os.Stderr, "  list   <skill-name>             List ledger entries\n")
		fmt.Fprintf(os.Stderr, "  delete <skill-name> <entry-id>  Delete a specific entry\n")
		fmt.Fprintf(os.Stderr, "  clear  <skill-name>             Delete all entries\n\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if fs.NArg() < 2 {
		fs.Usage()
		os.Exit(1)
	}

	if *cacheDir == "" {
		*cacheDir = defaultCacheDir()
	}

	action := fs.Arg(0)
	skillName := fs.Arg(1)

	cfg, err := LoadConfig(*cacheDir)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	skill, err := cfg.FindSkill(skillName)
	if err != nil {
		log.Fatalf("%v", err)
	}

	switch action {
	case "list":
		entries, err := ReadLedger(*cacheDir, skill.RepoURL, skill.SkillPath, 0)
		if err != nil {
			log.Fatalf("read ledger: %v", err)
		}
		if len(entries) == 0 {
			fmt.Println("No ledger entries.")
			return
		}
		for _, e := range entries {
			status := "unmerged"
			if e.CommitSHA != "" {
				status = "merged (" + e.CommitSHA[:8] + ")"
			}
			fmt.Printf("  %s  [%s]  %s\n", e.ID, status, e.Timestamp.Format("2006-01-02 15:04"))
			for _, l := range e.Learnings {
				fmt.Printf("    - %s\n", l)
			}
			if e.PRUrl != "" {
				fmt.Printf("    PR: %s\n", e.PRUrl)
			}
		}

	case "delete":
		if fs.NArg() < 3 {
			fmt.Fprintf(os.Stderr, "Usage: skillweave ledger delete <skill-name> <entry-id>\n")
			os.Exit(1)
		}
		entryID := fs.Arg(2)
		if err := DeleteLedgerEntry(*cacheDir, skill.RepoURL, skill.SkillPath, entryID); err != nil {
			log.Fatalf("%v", err)
		}
		fmt.Printf("Deleted ledger entry %s\n", entryID)

	case "clear":
		count, err := ClearLedger(*cacheDir, skill.RepoURL, skill.SkillPath)
		if err != nil {
			log.Fatalf("clear ledger: %v", err)
		}
		fmt.Printf("Cleared %d ledger entries for %q\n", count, skillName)

	default:
		fmt.Fprintf(os.Stderr, "Unknown ledger action: %s\n", action)
		fs.Usage()
		os.Exit(1)
	}
}

func cmdPush(args []string) {
	fs := flag.NewFlagSet("push", flag.ExitOnError)
	cacheDir := fs.String("cache-dir", "", "Cache directory (default ~/.skillweave)")
	commitMsg := fs.String("m", "", "Commit message (auto-generated if omitted)")
	skipPR := fs.Bool("no-pr", false, "Push branch only, don't create a PR")
	bobPath := fs.String("bob", "bob", "Path to bob CLI for AI-powered merging")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: skillweave push [flags] <skill-name>\n\n")
		fmt.Fprintf(os.Stderr, "Push skill updates to GitHub as a PR.\n")
		fmt.Fprintf(os.Stderr, "If there are unmerged learnings in the ledger, uses bob to merge them into the SKILL.md first.\n\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if fs.NArg() < 1 {
		fs.Usage()
		os.Exit(1)
	}

	if *cacheDir == "" {
		*cacheDir = defaultCacheDir()
	}

	cfg, err := LoadConfig(*cacheDir)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	skillName := fs.Arg(0)
	skill, err := cfg.FindSkill(skillName)
	if err != nil {
		log.Fatalf("%v", err)
	}

	// Ensure we have the repo cached.
	localRepoPath, err := ensureRepo(skill.RepoURL, *cacheDir)
	if err != nil {
		log.Fatalf("fetch repo: %v", err)
	}

	// Read current SKILL.md.
	skillFile := filepath.Join(localRepoPath, skill.SkillPath)
	currentContent, err := os.ReadFile(skillFile)
	if err != nil {
		log.Fatalf("read SKILL.md: %v", err)
	}

	// Check for unmerged learnings (ledger entries with learnings but no commit_sha).
	entries, err := ReadLedger(*cacheDir, skill.RepoURL, skill.SkillPath, 0)
	if err != nil {
		log.Fatalf("read ledger: %v", err)
	}

	var unmergedLearnings []string
	for _, e := range entries {
		if e.CommitSHA == "" && len(e.Learnings) > 0 {
			unmergedLearnings = append(unmergedLearnings, e.Learnings...)
		}
	}

	updatedContent := string(currentContent)

	if len(unmergedLearnings) > 0 {
		fmt.Printf("Found %d unmerged learnings. Using bob to merge them into SKILL.md...\n", len(unmergedLearnings))

		prompt := fmt.Sprintf(
			"You are updating a SKILL.md file with new learnings.\n\n"+
				"Here is the current SKILL.md:\n```\n%s\n```\n\n"+
				"Here are the new learnings to incorporate:\n%s\n\n"+
				"Output ONLY the full updated SKILL.md content with the learnings merged into the appropriate sections. "+
				"Do not add commentary before or after. Do not wrap in code fences. Do not summarize changes. "+
				"Just output the raw SKILL.md content, starting with --- and ending with the last line of the document.",
			string(currentContent),
			formatLearnings(unmergedLearnings),
		)

		merged, err := runBob(*bobPath, prompt)
		if err != nil {
			log.Fatalf("bob merge failed: %v", err)
		}
		updatedContent = merged

		// Write merged content back to cache.
		if err := os.WriteFile(skillFile, []byte(updatedContent), 0o644); err != nil {
			log.Fatalf("write merged SKILL.md: %v", err)
		}
		fmt.Println("SKILL.md updated with merged learnings.")

		// Also write to local checkout if registered.
		if skill.LocalPath != "" {
			localFile := filepath.Join(skill.LocalPath, skill.SkillPath)
			if err := os.MkdirAll(filepath.Dir(localFile), 0o755); err == nil {
				if err := os.WriteFile(localFile, []byte(updatedContent), 0o644); err == nil {
					fmt.Printf("Also written to: %s\n", localFile)
				}
			}
		}
	} else {
		fmt.Println("No unmerged learnings. Pushing current SKILL.md as-is.")
	}

	// Generate commit message if not provided.
	if *commitMsg == "" {
		if len(unmergedLearnings) > 0 {
			*commitMsg = fmt.Sprintf("Update %s skill with %d learnings", skillName, len(unmergedLearnings))
		} else {
			*commitMsg = fmt.Sprintf("Update %s skill", skillName)
		}
	}

	// Branch, commit, push.
	branch := fmt.Sprintf("skill-update/%s/%s", skillName, time.Now().Format("20060102-150405"))

	commitSHA, err := createBranchAndCommit(localRepoPath, skill.SkillPath, updatedContent, *commitMsg, branch)
	if err != nil {
		log.Fatalf("commit: %v", err)
	}
	fmt.Printf("Committed: %s\n", commitSHA)

	if err := push(localRepoPath, branch); err != nil {
		log.Fatalf("push: %v", err)
	}
	fmt.Printf("Pushed branch: %s\n", branch)

	if !*skipPR {
		body := buildPRBody(*commitMsg)
		prURL, err := createPR(localRepoPath, branch, *commitMsg, body)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: PR creation failed: %v\n", err)
		} else {
			fmt.Printf("PR created: %s\n", prURL)
		}
	}

	// Mark old learnings-only entries as merged, then record the push.
	if err := MarkLedgerMerged(*cacheDir, skill.RepoURL, skill.SkillPath, commitSHA); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to mark ledger entries as merged: %v\n", err)
	}
	entry := LedgerEntry{
		ID:        generateID(),
		RepoURL:   skill.RepoURL,
		SkillPath: skill.SkillPath,
		Learnings: unmergedLearnings,
		CommitSHA: commitSHA,
		Timestamp: time.Now(),
	}
	if err := WriteLedger(*cacheDir, entry); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to write ledger: %v\n", err)
	}
}

// runBob calls bob in non-interactive mode with a prompt, streams output
// with a [bob] prefix, and returns the full output.
func runBob(bobPath, prompt string) (string, error) {
	cmd := exec.Command(bobPath, "--yolo", "--output-format", "text", prompt)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("bob: pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("bob: start: %w", err)
	}

	var result strings.Builder
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		result.WriteString(line)
		result.WriteString("\n")
		fmt.Printf("[bob] %s\n", line)
	}

	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("bob: %w", err)
	}
	return cleanBobOutput(result.String()), nil
}

// cleanBobOutput strips bob's tool-use artifacts and trailing commentary.
func cleanBobOutput(raw string) string {
	// Strip everything from "[using tool" onwards — bob appends completion markers.
	if idx := strings.Index(raw, "[using tool"); idx >= 0 {
		raw = raw[:idx]
	}
	// Also strip "---output---" blocks.
	if idx := strings.Index(raw, "---output---"); idx >= 0 {
		raw = raw[:idx]
	}
	return strings.TrimSpace(raw)
}

// formatLearnings formats a list of learnings as a bulleted list.
func formatLearnings(learnings []string) string {
	var sb strings.Builder
	for _, l := range learnings {
		sb.WriteString("- ")
		sb.WriteString(l)
		sb.WriteString("\n")
	}
	return sb.String()
}
