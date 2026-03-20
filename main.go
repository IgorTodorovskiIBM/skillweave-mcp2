package main

import (
	"bufio"
	"context"
	"encoding/json"
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
	if dir := os.Getenv("SKILLWEAVE_DIR"); dir != "" {
		return dir
	}
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
		case "ai":
			cmdAI(os.Args[2:])
			return
		case "setup":
			cmdSetup(os.Args[2:])
			return
		case "status":
			cmdStatus(os.Args[2:])
			return
		case "gc", "clean":
			cmdGC(os.Args[2:])
			return
		case "help", "--help", "-h":
			printHelp()
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
	fmt.Fprintln(os.Stderr, "Note: 'skillweave register' is deprecated. Use 'skillweave setup' instead.")
	fmt.Fprintln(os.Stderr, "      setup does everything register does, plus validates the repo and prints MCP config.")
	fmt.Fprintln(os.Stderr)
	cmdSetup(args)
}

func cmdUnregister(args []string) {
	fs := flag.NewFlagSet("unregister", flag.ExitOnError)
	cacheDir := fs.String("cache-dir", "", "Cache directory (default ~/.skillweave)")
	yes := fs.Bool("yes", false, "Skip confirmation prompt")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: skillweave unregister [flags] <name>\n\n")
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

	// Check it exists before prompting.
	if _, err := cfg.FindSkill(name); err != nil {
		fmt.Fprintf(os.Stderr, "Skill %q not found\n", name)
		os.Exit(1)
	}

	if !*yes && !confirmAction(fmt.Sprintf("Unregister skill %q?", name)) {
		fmt.Println("Cancelled.")
		return
	}

	cfg.RemoveSkill(name)

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
	yes := fs.Bool("yes", false, "Skip confirmation prompt (for clear)")
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
		// Contextual hint: suggest clear when there are many merged entries.
		var merged int
		for _, e := range entries {
			if e.CommitSHA != "" {
				merged++
			}
		}
		if merged >= 5 {
			fmt.Printf("\nHint: %d merged entries. Run 'skillweave ledger clear %s' to clean up.\n", merged, skillName)
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
		if !*yes && !confirmAction(fmt.Sprintf("Delete all ledger entries for %q?", skillName)) {
			fmt.Println("Cancelled.")
			return
		}
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

func printHelp() {
	fmt.Fprintf(os.Stderr, `skillweave - MCP server that keeps SKILL.md files up to date

Usage: skillweave <command> [args]

Getting started:
  setup       One-command setup: register a skill and print MCP config
  status      Show registered skills, AI tools, and unmerged learnings

Commands:
  register    Register a SKILL.md (deprecated, use setup)
  unregister  Remove a registered skill
  list        List registered skills
  push        Push skill updates to GitHub as a PR
  ledger      Manage ledger entries (list, delete, clear)
  ai          Configure AI tools for merging (add, list, remove, reorder)
  gc          Clean up stale cache repos and old merged ledger entries
  help        Show this help

Server mode (no command):
  skillweave [flags]       Start the MCP server

Server flags:
  -http <addr>             HTTP listen address (default: stdio mode)
  -log-transport           Log MCP transport frames to stderr
  -cache-dir <path>        Cache directory (default: ~/.skillweave)

Environment:
  SKILLWEAVE_DIR           Override cache directory (default: ~/.skillweave)

Run 'skillweave <command> --help' for details on each command.
`)
}

func cmdAI(args []string) {
	fs := flag.NewFlagSet("ai", flag.ExitOnError)
	cacheDir := fs.String("cache-dir", "", "Cache directory (default ~/.skillweave)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: skillweave ai <action> [args]\n\n")
		fmt.Fprintf(os.Stderr, "Actions:\n")
		fmt.Fprintf(os.Stderr, "  add     <name> <command> [args...]  Add an AI tool\n")
		fmt.Fprintf(os.Stderr, "  list                                List configured AI tools\n")
		fmt.Fprintf(os.Stderr, "  remove  <name>                      Remove an AI tool\n")
		fmt.Fprintf(os.Stderr, "  reorder <name1> <name2> ...         Set the order (first = tried first)\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  skillweave ai add bob bob --yolo --output-format text\n")
		fmt.Fprintf(os.Stderr, "  skillweave ai add gemini /home/itodoro/bin/gemini.mjs -p\n")
		fmt.Fprintf(os.Stderr, "  skillweave ai reorder gemini bob\n")
		fmt.Fprintf(os.Stderr, "  skillweave ai list\n")
		fmt.Fprintf(os.Stderr, "  skillweave ai remove gemini\n\n")
		fmt.Fprintf(os.Stderr, "The prompt is always appended as the last argument.\n")
		fmt.Fprintf(os.Stderr, "When pushing, tools are tried in order until one succeeds.\n\n")
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

	action := fs.Arg(0)

	cfg, err := LoadConfig(*cacheDir)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	switch action {
	case "add":
		if fs.NArg() < 3 {
			fmt.Fprintf(os.Stderr, "Usage: skillweave ai add <name> <command> [args...]\n")
			os.Exit(1)
		}
		name := fs.Arg(1)
		command := fs.Arg(2)
		var cmdArgs []string
		for i := 3; i < fs.NArg(); i++ {
			cmdArgs = append(cmdArgs, fs.Arg(i))
		}
		cfg.AddAICommand(AICommand{
			Name:    name,
			Command: command,
			Args:    cmdArgs,
		})
		if err := SaveConfig(*cacheDir, cfg); err != nil {
			log.Fatalf("save config: %v", err)
		}
		fmt.Printf("Added AI tool %q: %s %s\n", name, command, strings.Join(cmdArgs, " "))

	case "list":
		if len(cfg.AICommands) == 0 {
			fmt.Println("No AI tools configured. Use 'skillweave ai add' to add one.")
			fmt.Println("\nExample:")
			fmt.Println("  skillweave ai add bob bob --yolo --output-format text")
			return
		}
		for i, cmd := range cfg.AICommands {
			order := fmt.Sprintf("%d.", i+1)
			args := ""
			if len(cmd.Args) > 0 {
				args = " " + strings.Join(cmd.Args, " ")
			}
			fmt.Printf("  %s %s  →  %s%s\n", order, cmd.Name, cmd.Command, args)
		}
		if len(cfg.AICommands) > 1 {
			fmt.Println("\nHint: use 'skillweave ai reorder <name1> <name2> ...' to change the try order.")
		}

	case "remove":
		if fs.NArg() < 2 {
			fmt.Fprintf(os.Stderr, "Usage: skillweave ai remove <name>\n")
			os.Exit(1)
		}
		name := fs.Arg(1)
		if !cfg.RemoveAICommand(name) {
			fmt.Fprintf(os.Stderr, "AI tool %q not found\n", name)
			os.Exit(1)
		}
		if err := SaveConfig(*cacheDir, cfg); err != nil {
			log.Fatalf("save config: %v", err)
		}
		fmt.Printf("Removed AI tool %q\n", name)

	case "reorder":
		if fs.NArg() < 2 {
			fmt.Fprintf(os.Stderr, "Usage: skillweave ai reorder <name1> <name2> ...\n")
			os.Exit(1)
		}
		names := make([]string, 0, fs.NArg()-1)
		for i := 1; i < fs.NArg(); i++ {
			names = append(names, fs.Arg(i))
		}
		// Build a map of existing commands.
		byName := make(map[string]AICommand, len(cfg.AICommands))
		for _, cmd := range cfg.AICommands {
			byName[cmd.Name] = cmd
		}
		// Validate all names exist.
		for _, n := range names {
			if _, ok := byName[n]; !ok {
				log.Fatalf("AI tool %q not found", n)
			}
		}
		// Rebuild in the requested order, append any not mentioned at the end.
		reordered := make([]AICommand, 0, len(cfg.AICommands))
		seen := make(map[string]bool)
		for _, n := range names {
			reordered = append(reordered, byName[n])
			seen[n] = true
		}
		for _, cmd := range cfg.AICommands {
			if !seen[cmd.Name] {
				reordered = append(reordered, cmd)
			}
		}
		cfg.AICommands = reordered
		if err := SaveConfig(*cacheDir, cfg); err != nil {
			log.Fatalf("save config: %v", err)
		}
		fmt.Println("AI tools reordered:")
		for i, cmd := range cfg.AICommands {
			fmt.Printf("  %d. %s\n", i+1, cmd.Name)
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown ai action: %s\n", action)
		fs.Usage()
		os.Exit(1)
	}
}

func cmdSetup(args []string) {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	name := fs.String("name", "", "Skill name (auto-derived from path if omitted)")
	cacheDir := fs.String("cache-dir", "", "Cache directory (default ~/.skillweave)")
	localPath := fs.String("local-path", "", "Local checkout root (auto-detected if inside a matching repo)")
	repoURL := fs.String("repo", "", "Git repo URL (if not using a GitHub blob URL)")
	skillPath := fs.String("path", "", "Path to SKILL.md in repo (required if URL doesn't include a file path)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: skillweave setup [flags] <github-url>\n\n")
		fmt.Fprintf(os.Stderr, "One-command setup: register a skill and print MCP client config.\n\n")
		fmt.Fprintf(os.Stderr, "Accepts many URL formats:\n")
		fmt.Fprintf(os.Stderr, "  skillweave setup https://github.com/user/repo/blob/main/skills/my-skill/SKILL.md\n")
		fmt.Fprintf(os.Stderr, "  skillweave setup https://github.com/user/repo --path skills/my-skill/SKILL.md\n")
		fmt.Fprintf(os.Stderr, "  skillweave setup git@github.com:user/repo.git --path skills/my-skill/SKILL.md\n")
		fmt.Fprintf(os.Stderr, "  skillweave setup user/repo --path skills/my-skill/SKILL.md\n")
		fmt.Fprintf(os.Stderr, "  skillweave setup --repo git@github.com:user/repo.git --path skills/my-skill/SKILL.md\n\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if *cacheDir == "" {
		*cacheDir = defaultCacheDir()
	}

	rURL, sPath := *repoURL, *skillPath
	if rURL == "" {
		if fs.NArg() < 1 {
			fs.Usage()
			os.Exit(1)
		}
		parsed, parsedPath, err := ParseGitHubURL(fs.Arg(0))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		rURL = parsed
		if sPath == "" {
			sPath = parsedPath
		}
	}

	if sPath == "" {
		fmt.Fprintf(os.Stderr, "Error: could not determine the skill path from the URL.\n")
		fmt.Fprintf(os.Stderr, "Use --path to specify it, e.g.:\n")
		fmt.Fprintf(os.Stderr, "  skillweave setup %s --path skills/my-skill/SKILL.md\n", fs.Arg(0))
		os.Exit(1)
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

	// Check if we can clone the repo (validates the URL).
	fmt.Println("\nFetching skill from remote...")
	if _, err := ensureRepo(rURL, *cacheDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not fetch repo: %v\n", err)
		fmt.Fprintf(os.Stderr, "  The skill was registered but will not be available until the repo is accessible.\n")
		fmt.Fprintf(os.Stderr, "  Common fixes:\n")
		fmt.Fprintf(os.Stderr, "    - Check SSH keys: ssh -T git@github.com\n")
		fmt.Fprintf(os.Stderr, "    - For private repos: go env -w GOPRIVATE=github.com/...\n")
	} else {
		skillFile := filepath.Join(repoCacheDir(*cacheDir, rURL), sPath)
		if raw, err := os.ReadFile(skillFile); err == nil {
			_, desc, _ := parseFrontmatter(string(raw))
			if desc != "" {
				fmt.Printf("  description: %s\n", desc)
			}
		}
		fmt.Println("  OK")
	}

	fmt.Println()
	printMCPConfig()

	// Check AI tools.
	if len(cfg.AICommands) == 0 {
		fmt.Println("\nAI tools (for CLI push with auto-merge):")
		found := false
		for _, fb := range defaultAIFallbacks() {
			if _, err := exec.LookPath(fb.Command); err == nil {
				fmt.Printf("  Found %q on PATH (will be used automatically)\n", fb.Command)
				found = true
				break
			}
		}
		if !found {
			fmt.Println("  None found. Optional: run 'skillweave ai add' to configure one.")
		}
	}

	fmt.Println("\nSetup complete. Start using skillweave with your MCP client.")
}

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	cacheDir := fs.String("cache-dir", "", "Cache directory (default ~/.skillweave)")
	fs.Parse(args)

	if *cacheDir == "" {
		*cacheDir = defaultCacheDir()
	}

	cfg, err := LoadConfig(*cacheDir)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// Skills.
	fmt.Printf("Skills: %d registered\n", len(cfg.Skills))
	for _, s := range cfg.Skills {
		entries, _ := ReadLedger(*cacheDir, s.RepoURL, s.SkillPath, 0)
		var unmerged int
		for _, e := range entries {
			if e.CommitSHA == "" && len(e.Learnings) > 0 {
				unmerged++
			}
		}
		status := ""
		if unmerged > 0 {
			status = fmt.Sprintf(" (%d unmerged learnings)", unmerged)
		}
		fmt.Printf("  %s%s\n", s.Name, status)
	}

	// AI tools.
	fmt.Printf("\nAI tools: %d configured\n", len(cfg.AICommands))
	for i, cmd := range cfg.AICommands {
		fmt.Printf("  %d. %s → %s\n", i+1, cmd.Name, cmd.Command)
	}
	if len(cfg.AICommands) == 0 {
		found := false
		for _, fb := range defaultAIFallbacks() {
			if _, err := exec.LookPath(fb.Command); err == nil {
				fmt.Printf("  (will use %q from PATH as fallback)\n", fb.Command)
				found = true
				break
			}
		}
		if !found {
			fmt.Println("  (none configured, none found on PATH)")
		}
	}

	// Cache dir.
	fmt.Printf("\nCache: %s\n", *cacheDir)
}

// printMCPConfig prints MCP client configuration JSON.
func printMCPConfig() {
	// Find the skillweave binary path.
	binPath, err := exec.LookPath("skillweave")
	if err != nil {
		// Fall back to the current executable.
		binPath, _ = os.Executable()
	}
	fmt.Println("Add this to your MCP client config (.mcp.json):")
	fmt.Printf(`
{
  "mcpServers": {
    "skillweave": {
      "command": "%s",
      "args": []
    }
  }
}
`, binPath)
}

func cmdPush(args []string) {
	fs := flag.NewFlagSet("push", flag.ExitOnError)
	cacheDir := fs.String("cache-dir", "", "Cache directory (default ~/.skillweave)")
	commitMsg := fs.String("m", "", "Commit message (auto-generated if omitted)")
	skipPR := fs.Bool("no-pr", false, "Push branch only, don't create a PR")
	aiName := fs.String("ai", "", "Use a specific AI tool by name (default: try all in order)")
	dryRun := fs.Bool("dry-run", false, "Show what would change without committing or pushing")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: skillweave push [flags] <skill-name>\n\n")
		fmt.Fprintf(os.Stderr, "Push skill updates to GitHub as a PR.\n")
		fmt.Fprintf(os.Stderr, "If there are unmerged learnings in the ledger, uses a configured AI tool to merge them.\n")
		fmt.Fprintf(os.Stderr, "AI tools are tried in order (see 'skillweave ai list'). Use --ai to pick one.\n\n")
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
		fmt.Printf("Found %d unmerged learnings.\n", len(unmergedLearnings))

		// Determine which AI tools to try.
		var aiTools []AICommand
		if *aiName != "" {
			cmd, err := cfg.FindAICommand(*aiName)
			if err != nil {
				log.Fatalf("%v", err)
			}
			aiTools = []AICommand{*cmd}
		} else {
			aiTools = cfg.AICommands
		}

		if len(aiTools) == 0 {
			// Try common AI tools from PATH as fallback.
			for _, fallback := range defaultAIFallbacks() {
				if _, err := exec.LookPath(fallback.Command); err == nil {
					fmt.Fprintf(os.Stderr, "No AI tools configured. Using %q from PATH.\n", fallback.Name)
					fmt.Fprintf(os.Stderr, "Run 'skillweave ai add' to configure permanently.\n")
					aiTools = []AICommand{fallback}
					break
				}
			}
			if len(aiTools) == 0 {
				log.Fatalf("No AI tools configured and none found on PATH.\nRun 'skillweave ai add' first.\nExample: skillweave ai add bob bob --yolo --output-format text")
			}
		}

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

		var merged string
		var mergeErr error
		for _, ai := range aiTools {
			fmt.Printf("Trying %q (%s)...\n", ai.Name, ai.Command)
			merged, mergeErr = runAI(ai, prompt)
			if mergeErr == nil {
				break
			}
			fmt.Fprintf(os.Stderr, "  %s failed: %v\n", ai.Name, mergeErr)
			if len(aiTools) > 1 {
				fmt.Fprintf(os.Stderr, "  Trying next AI tool...\n")
			}
		}
		if mergeErr != nil {
			log.Fatalf("All AI tools failed. Last error: %v", mergeErr)
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

	// Check if content actually changed vs origin.
	if updatedContent == string(currentContent) && len(unmergedLearnings) == 0 {
		fmt.Println("SKILL.md is already up to date with origin. Nothing to push.")
		return
	}

	// Dry-run: show diff and exit without committing or pushing.
	if *dryRun {
		fmt.Print("\n--- Dry run: showing diff (no changes will be pushed) ---\n\n")
		showDiff(string(currentContent), updatedContent, skill.SkillPath)
		return
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
			fmt.Fprintf(os.Stderr, "\n*** ACTION REQUIRED: PR creation failed ***\n")
			fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
			fmt.Fprintf(os.Stderr, "Your changes were pushed to branch: %s\n", branch)
			fmt.Fprintf(os.Stderr, "Create the PR manually:\n")
			fmt.Fprintf(os.Stderr, "  gh pr create --head %s --title %q\n\n", branch, *commitMsg)
			fmt.Fprintf(os.Stderr, "Common fixes:\n")
			fmt.Fprintf(os.Stderr, "  - Install gh: https://cli.github.com\n")
			fmt.Fprintf(os.Stderr, "  - Authenticate: gh auth login\n")
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

// runAI calls an AI tool with a prompt, streams output with a prefix, and returns the result.
func runAI(ai AICommand, prompt string) (string, error) {
	args := append(ai.Args, prompt)
	cmd := exec.Command(ai.Command, args...)
	cmd.Stderr = os.Stderr

	prefix := fmt.Sprintf("[%s]", ai.Name)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("%s: pipe: %w", ai.Name, err)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("%s: start: %w", ai.Name, err)
	}

	var result strings.Builder
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		result.WriteString(line)
		result.WriteString("\n")
		fmt.Printf("%s %s\n", prefix, line)
	}

	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("%s: %w", ai.Name, err)
	}
	return cleanAIOutput(result.String()), nil
}

// cleanAIOutput strips bob's tool-use artifacts and trailing commentary.
func cleanAIOutput(raw string) string {
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

// defaultAIFallbacks returns common AI tools to try if none are configured.
func defaultAIFallbacks() []AICommand {
	return []AICommand{
		{Name: "bob", Command: "bob", Args: []string{"--yolo", "--output-format", "text"}},
		{Name: "claude", Command: "claude", Args: []string{"-p"}},
	}
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

// confirmAction prompts the user for confirmation. Returns true if they accept.
func confirmAction(prompt string) bool {
	fmt.Fprintf(os.Stderr, "%s [y/N]: ", prompt)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}

// showDiff prints a unified diff between original and updated content.
func showDiff(original, updated, label string) {
	origFile, err := os.CreateTemp("", "skill-orig-*.md")
	if err != nil {
		fmt.Println(updated)
		return
	}
	newFile, err := os.CreateTemp("", "skill-new-*.md")
	if err != nil {
		os.Remove(origFile.Name())
		fmt.Println(updated)
		return
	}
	defer os.Remove(origFile.Name())
	defer os.Remove(newFile.Name())

	origFile.WriteString(original)
	newFile.WriteString(updated)
	origFile.Close()
	newFile.Close()

	cmd := exec.Command("diff", "-u",
		"--label", "current/"+label, origFile.Name(),
		"--label", "updated/"+label, newFile.Name(),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run() // exit code 1 = differences found, which is expected
}

func cmdGC(args []string) {
	fs := flag.NewFlagSet("gc", flag.ExitOnError)
	cacheDir := fs.String("cache-dir", "", "Cache directory (default ~/.skillweave)")
	maxAge := fs.Int("days", 30, "Delete merged ledger entries older than this many days")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: skillweave gc [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Clean up stale cache repos and old merged ledger entries.\n\n")
		fmt.Fprintf(os.Stderr, "Removes:\n")
		fmt.Fprintf(os.Stderr, "  - Cached repos for skills that are no longer registered\n")
		fmt.Fprintf(os.Stderr, "  - Merged ledger entries older than --days (default 30)\n\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if *cacheDir == "" {
		*cacheDir = defaultCacheDir()
	}

	cfg, err := LoadConfig(*cacheDir)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// Build set of active repo hashes.
	activeRepos := make(map[string]bool)
	for _, s := range cfg.Skills {
		activeRepos[hashString(normalizeRepoURL(s.RepoURL))] = true
	}

	// Clean up stale repo caches.
	reposDir := filepath.Join(*cacheDir, "repos")
	var removedRepos int
	if entries, err := os.ReadDir(reposDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if !activeRepos[e.Name()] {
				repoPath := filepath.Join(reposDir, e.Name())
				if err := os.RemoveAll(repoPath); err != nil {
					fmt.Fprintf(os.Stderr, "warning: could not remove %s: %v\n", repoPath, err)
				} else {
					removedRepos++
				}
			}
		}
	}

	// Clean up old merged ledger entries.
	cutoff := time.Now().AddDate(0, 0, -*maxAge)
	ledgerDir := filepath.Join(*cacheDir, "ledger")
	var removedEntries int
	filepath.WalkDir(ledgerDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var e LedgerEntry
		if err := json.Unmarshal(data, &e); err != nil {
			return nil
		}
		// Only remove merged entries that are old enough.
		if e.CommitSHA != "" && e.Timestamp.Before(cutoff) {
			if err := os.Remove(path); err == nil {
				removedEntries++
			}
		}
		return nil
	})

	// Clean up empty ledger directories.
	cleanEmptyDirs(ledgerDir)

	fmt.Printf("Removed %d stale repo cache(s) and %d old merged ledger entry/entries.\n", removedRepos, removedEntries)
}

// cleanEmptyDirs removes empty directories under root (bottom-up).
func cleanEmptyDirs(root string) {
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() || path == root {
			return nil
		}
		entries, err := os.ReadDir(path)
		if err == nil && len(entries) == 0 {
			os.Remove(path)
		}
		return nil
	})
}
