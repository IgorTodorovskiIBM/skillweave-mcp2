package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
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
Pay attention to when the user corrects you or you discover something new.

WHEN YOU GET CORRECTED OR LEARN SOMETHING NEW:
Call skill_note immediately with a one-line description. Do not wait. Do not batch. Examples:
  skill_note({ note: "always use %w not %v for error wrapping in this codebase" })
  skill_note({ note: "the build requires GOOS=linux even on mac — cross-compile only" })
  skill_note({ note: "prefer table-driven tests over individual test functions here" })

skill_note is lightweight — just a sentence. Use it every time you are corrected, discover a gotcha, or learn a pattern that would help future sessions. Err on the side of noting too much rather than too little.

WHEN TO CALL skill_update (full rewrite):
- The user explicitly asks you to update or rewrite the skill document
- You want to reorganize the skill structure significantly
Use skill_note for everything else.

PUSHING CHANGES:
Call skill_push when the user asks to share changes with the team. skill_push automatically merges any unmerged notes into the SKILL.md using a configured AI tool — you do not need to call skill_update first.

OTHER TOOLS:
- skill_read: Re-read the current SKILL.md without creating a new session (useful if you need to refresh your context).
- skill_list_notes: See all unmerged notes for a skill.`

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
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	logJSON := flag.Bool("log-json", false, "Output logs in JSON format")
	flag.Parse()

	// Initialize logger
	level := LevelInfo
	switch strings.ToLower(*logLevel) {
	case "debug":
		level = LevelDebug
	case "info":
		level = LevelInfo
	case "warn":
		level = LevelWarn
	case "error":
		level = LevelError
	default:
		fmt.Fprintf(os.Stderr, "Invalid log level %q, using info\n", *logLevel)
	}
	InitLogger(level, *logJSON)
	logger := GetLogger()
	logger.WithFields(map[string]interface{}{
		"version":   "0.1.0",
		"cache_dir": *cacheDir,
	}).Info("skillweave starting")

	if *cacheDir == "" {
		*cacheDir = defaultCacheDir()
	}

	cfg, err := LoadConfig(*cacheDir)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "skillweave",
		Version: "0.1.0",
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
	all := fs.Bool("all", false, "Show all entries including merged (for list)")
	yes := fs.Bool("yes", false, "Skip confirmation prompt (for clear)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: skillweave ledger <action> <skill-name> [entry-id]\n\n")
		fmt.Fprintf(os.Stderr, "Actions:\n")
		fmt.Fprintf(os.Stderr, "  list   <skill-name>             List ledger entries\n")
		fmt.Fprintf(os.Stderr, "  review <skill-name>             Walk through unmerged notes (keep/discard)\n")
		fmt.Fprintf(os.Stderr, "  delete <skill-name> <entry-id>  Delete a specific entry\n")
		fmt.Fprintf(os.Stderr, "  clear  <skill-name>             Delete all entries\n\n")
		fs.PrintDefaults()
	}
	// Reorder args so flags (--all, --yes, etc.) come before positional args,
	// since Go's flag package stops parsing at the first non-flag argument.
	fs.Parse(reorderFlags(args))

	if *cacheDir == "" {
		*cacheDir = defaultCacheDir()
	}

	// No args or just an action with no skill: show skills with ledger summaries.
	if fs.NArg() < 2 {
		// If they passed just "list" with no skill, treat it as a summary view.
		if fs.NArg() == 1 && fs.Arg(0) != "list" {
			fs.Usage()
			os.Exit(1)
		}
		cfg, err := LoadConfig(*cacheDir)
		if err != nil {
			log.Fatalf("load config: %v", err)
		}
		if len(cfg.Skills) == 0 {
			fmt.Fprintln(os.Stderr, "No skills registered. Use 'skillweave setup <github-url>' to add one.")
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Usage: skillweave ledger <action> <skill-name> [entry-id]\n\n")
		fmt.Fprintf(os.Stderr, "Available skills:\n")
		for _, s := range cfg.Skills {
			entries, _ := ReadLedger(*cacheDir, s.RepoURL, s.SkillPath, 0)
			_, unmerged := collectUnmergedLearnings(entries)
			var merged int
			for _, e := range entries {
				if e.CommitSHA != "" {
					merged++
				}
			}
			fmt.Fprintf(os.Stderr, "  %s  (%d unmerged, %d merged, %d total)\n", s.Name, len(unmerged), merged, len(entries))
		}
		fmt.Fprintf(os.Stderr, "\nActions:\n")
		fmt.Fprintf(os.Stderr, "  list   <skill-name>             List ledger entries\n")
		fmt.Fprintf(os.Stderr, "  delete <skill-name> <entry-id>  Delete a specific entry\n")
		fmt.Fprintf(os.Stderr, "  clear  <skill-name>             Delete all entries\n\n")
		fs.PrintDefaults()
		os.Exit(1)
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
		var shown, merged int
		for _, e := range entries {
			if e.CommitSHA != "" {
				merged++
				if !*all {
					continue
				}
			}
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
			shown++
		}
		if shown == 0 {
			fmt.Println("No unmerged notes. Use --all to see merged entries too.")
			return
		}
		if !*all && merged > 0 {
			fmt.Printf("\n(%d merged entries hidden. Use --all to show, or 'ledger clear %s' to clean up.)\n", merged, skillName)
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

	case "review":
		entries, err := ReadLedger(*cacheDir, skill.RepoURL, skill.SkillPath, 0)
		if err != nil {
			log.Fatalf("read ledger: %v", err)
		}
		// Collect only unmerged entries that have learnings.
		var unmerged []LedgerEntry
		for _, e := range entries {
			if e.CommitSHA == "" && len(e.Learnings) > 0 {
				unmerged = append(unmerged, e)
			}
		}
		if len(unmerged) == 0 {
			fmt.Println("No unmerged notes to review.")
			return
		}
		fmt.Printf("Reviewing %d unmerged note(s) for %q\n\n", len(unmerged), skillName)
		reader := bufio.NewReader(os.Stdin)
		var kept, discarded int
		for i, e := range unmerged {
			for _, l := range e.Learnings {
				fmt.Printf("  [%d/%d] %s\n", i+1, len(unmerged), l)
			}
			fmt.Fprintf(os.Stderr, "  Keep? [Y/n/q]: ")
			line, _ := reader.ReadString('\n')
			line = strings.TrimSpace(strings.ToLower(line))
			switch line {
			case "n", "no":
				if err := DeleteLedgerEntry(*cacheDir, skill.RepoURL, skill.SkillPath, e.ID); err != nil {
					fmt.Fprintf(os.Stderr, "  warning: could not delete: %v\n", err)
				} else {
					discarded++
					fmt.Println("  Discarded.")
				}
			case "q", "quit":
				fmt.Printf("\nStopped. Kept %d, discarded %d, %d remaining.\n", kept, discarded, len(unmerged)-i-discarded-kept)
				return
			default:
				kept++
				fmt.Println("  Kept.")
			}
			fmt.Println()
		}
		fmt.Printf("Done. Kept %d, discarded %d.\n", kept, discarded)

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

	skill := RegisteredSkill{
		Name:      skillName,
		RepoURL:   rURL,
		SkillPath: sPath,
		LocalPath: *localPath,
	}

	fmt.Println("\nFetching skill from remote...")
	desc, fileExists, err := loadRegisteredSkill(*cacheDir, skill)
	if err != nil {
		log.Fatalf("validate skill: %v", err)
	}

	if !fileExists {
		fmt.Println("SKILL.md not found in remote — creating skeleton...")
		skeleton := SkeletonSKILL(skillName)

		// Write skeleton to cache repo.
		cacheSkillFile := filepath.Join(repoCacheDir(*cacheDir, rURL), sPath)
		if err := os.MkdirAll(filepath.Dir(cacheSkillFile), 0o755); err != nil {
			log.Fatalf("create cache dir: %v", err)
		}
		if err := os.WriteFile(cacheSkillFile, []byte(skeleton), 0o644); err != nil {
			log.Fatalf("write skeleton to cache: %v", err)
		}

		// Write skeleton to local checkout if set.
		if *localPath != "" {
			localFile := filepath.Join(*localPath, sPath)
			if err := os.MkdirAll(filepath.Dir(localFile), 0o755); err == nil {
				if err := os.WriteFile(localFile, []byte(skeleton), 0o644); err == nil {
					fmt.Printf("  Skeleton written to: %s\n", localFile)
				}
			}
		}
	}

	cfg, err := LoadConfig(*cacheDir)
	if err != nil {
		log.Fatalf("load config: %v", err)
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
	if !fileExists {
		fmt.Println("  status: new skill (skeleton created)")
	}
	if desc != "" {
		fmt.Printf("  description: %s\n", desc)
	}
	fmt.Println("  OK")

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

	// Offer to add skillweave as an MCP server in bob if available.
	if _, err := exec.LookPath("bob"); err == nil {
		// Check if already registered.
		out, err := exec.Command("bob", "mcp", "list").Output()
		alreadyRegistered := err == nil && strings.Contains(string(out), "skillweave")
		if !alreadyRegistered {
			binPath, _ := os.Executable()
			if p, err := exec.LookPath("skillweave"); err == nil {
				binPath = p
			}
			fmt.Printf("\nDetected bob on PATH. Add skillweave as an MCP server?\n")
			fmt.Printf("  bob mcp add skillweave %s\n", binPath)
			if confirmAction("Add now?") {
				cmd := exec.Command("bob", "mcp", "add", "skillweave", binPath)
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				if err := cmd.Run(); err != nil {
					fmt.Fprintf(os.Stderr, "  Failed: %v\n", err)
					fmt.Fprintf(os.Stderr, "  Run manually: bob mcp add skillweave %s\n", binPath)
				} else {
					fmt.Println("  Added skillweave to bob.")
				}
			}
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

// loadRegisteredSkill fetches the repo and reads the SKILL.md.
// Returns (description, fileExists, error). When the file doesn't exist in the
// remote repo, fileExists is false and err is nil — the caller can create a skeleton.
func loadRegisteredSkill(cacheDir string, skill RegisteredSkill) (string, bool, error) {
	localRepoPath, err := ensureRepo(skill.RepoURL, cacheDir)
	if err != nil {
		return "", false, fmt.Errorf("fetch repo: %w", err)
	}

	raw, err := os.ReadFile(filepath.Join(localRepoPath, skill.SkillPath))
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read %s: %w", skill.SkillPath, err)
	}

	_, desc, _ := parseFrontmatter(string(raw))
	return desc, true, nil
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

	if *cacheDir == "" {
		*cacheDir = defaultCacheDir()
	}

	if fs.NArg() < 1 {
		cfg, err := LoadConfig(*cacheDir)
		if err != nil {
			log.Fatalf("load config: %v", err)
		}
		if len(cfg.Skills) == 0 {
			fmt.Fprintln(os.Stderr, "No skills registered. Use 'skillweave setup <github-url>' to add one.")
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Usage: skillweave push [flags] <skill-name>\n\n")
		fmt.Fprintf(os.Stderr, "Available skills:\n")
		for _, s := range cfg.Skills {
			entries, _ := ReadLedger(*cacheDir, s.RepoURL, s.SkillPath, 0)
			_, unmerged := collectUnmergedLearnings(entries)
			if len(unmerged) > 0 {
				fmt.Fprintf(os.Stderr, "  %s  (%d unmerged)\n", s.Name, len(unmerged))
			} else {
				fmt.Fprintf(os.Stderr, "  %s\n", s.Name)
			}
		}
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
		os.Exit(1)
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
		if os.IsNotExist(err) {
			currentContent = []byte("")
		} else {
			log.Fatalf("read SKILL.md: %v", err)
		}
	}

	// Check for unmerged learnings (ledger entries with learnings but no commit_sha).
	entries, err := ReadLedger(*cacheDir, skill.RepoURL, skill.SkillPath, 0)
	if err != nil {
		log.Fatalf("read ledger: %v", err)
	}

	selectedEntryIDs, unmergedLearnings := collectUnmergedLearnings(entries)

	updatedContent := string(currentContent)
	var prURL string

	if len(unmergedLearnings) > 0 {
		fmt.Printf("Found %d unmerged learnings.\n", len(unmergedLearnings))

		// If --ai is specified, override the config for this run.
		mergeCfg := cfg
		if *aiName != "" {
			cmd, err := cfg.FindAICommand(*aiName)
			if err != nil {
				log.Fatalf("%v", err)
			}
			mergeCfg = &SkillConfig{AICommands: []AICommand{*cmd}}
		}

		merged, err := mergeNotesWithAI(mergeCfg, string(currentContent), unmergedLearnings, os.Stdout)
		if err != nil {
			log.Fatalf("%v", err)
		}
		updatedContent = merged
	} else {
		fmt.Println("No unmerged learnings. Pushing current SKILL.md as-is.")
	}

	contentChanged := updatedContent != string(currentContent)

	// Generate commit message if not provided.
	if *commitMsg == "" {
		if len(unmergedLearnings) > 0 {
			*commitMsg = fmt.Sprintf("Update %s skill with %d learnings", skillName, len(unmergedLearnings))
		} else {
			*commitMsg = fmt.Sprintf("Update %s skill", skillName)
		}
	}

	if !contentChanged {
		if len(unmergedLearnings) > 0 {
			fmt.Println("Merged learnings produced no SKILL.md changes. Nothing to push; ledger entries remain unmerged.")
		} else {
			fmt.Println("SKILL.md is already up to date with origin. Nothing to push.")
		}
		return
	}

	// Dry-run: show diff and exit without committing or pushing.
	if *dryRun {
		fmt.Print("\n--- Dry run: showing diff (no changes will be pushed) ---\n\n")
		showDiff(string(currentContent), updatedContent, skill.SkillPath)
		return
	}

	if len(unmergedLearnings) > 0 {
		fmt.Println("SKILL.md updated with merged learnings.")
		if skill.LocalPath != "" {
			localFile := filepath.Join(skill.LocalPath, skill.SkillPath)
			if err := os.MkdirAll(filepath.Dir(localFile), 0o755); err == nil {
				if err := os.WriteFile(localFile, []byte(updatedContent), 0o644); err == nil {
					fmt.Printf("Also written to: %s\n", localFile)
				}
			}
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
		prURL, err = createPR(localRepoPath, branch, *commitMsg, body)
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

	// Mark the exact learnings merged into this push, then record the push.
	if err := MarkLedgerEntriesMerged(*cacheDir, skill.RepoURL, skill.SkillPath, selectedEntryIDs, commitSHA); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to mark ledger entries as merged: %v\n", err)
	}
	entry := LedgerEntry{
		ID:        generateID(),
		RepoURL:   skill.RepoURL,
		SkillPath: skill.SkillPath,
		Learnings: unmergedLearnings,
		CommitSHA: commitSHA,
		PRUrl:     prURL,
		Timestamp: time.Now(),
	}
	if err := WriteLedger(*cacheDir, entry); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to write ledger: %v\n", err)
	}
}

// runAI calls an AI tool with a prompt, streams progress to w, and returns the result.
// Pass os.Stdout for CLI usage or os.Stderr/io.Discard for MCP (where stdout is the transport).
func runAI(ai AICommand, prompt string, w io.Writer) (string, error) {
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
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		result.WriteString(line)
		result.WriteString("\n")
		fmt.Fprintf(w, "%s %s\n", prefix, line)
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("%s: read stdout: %w", ai.Name, err)
	}

	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("%s: %w", ai.Name, err)
	}
	return cleanAIOutput(result.String()), nil
}

// mergeNotesWithAI uses configured AI tools to merge learnings into the current SKILL.md content.
// Returns the merged content or an error if all AI tools fail (or none are available).
func mergeNotesWithAI(cfg *SkillConfig, currentContent string, learnings []string, w io.Writer) (string, error) {
	aiTools := cfg.AICommands
	if len(aiTools) == 0 {
		for _, fallback := range defaultAIFallbacks() {
			if _, err := exec.LookPath(fallback.Command); err == nil {
				fmt.Fprintf(w, "No AI tools configured. Using %q from PATH.\n", fallback.Name)
				aiTools = []AICommand{fallback}
				break
			}
		}
		if len(aiTools) == 0 {
			return "", fmt.Errorf("no AI tools configured and none found on PATH")
		}
	}

	prompt := fmt.Sprintf(
		"You are updating a SKILL.md file with new learnings.\n\n"+
			"Here is the current SKILL.md:\n```\n%s\n```\n\n"+
			"Here are the new learnings to incorporate:\n%s\n\n"+
			"Output ONLY the full updated SKILL.md content with the learnings merged into the appropriate sections. "+
			"Do not add commentary before or after. Do not wrap in code fences. Do not summarize changes. "+
			"Just output the raw SKILL.md content, starting with the first line of the document and ending with the last.",
		currentContent,
		formatLearnings(learnings),
	)

	var merged string
	var mergeErr error
	for _, ai := range aiTools {
		fmt.Fprintf(w, "Trying %q (%s)...\n", ai.Name, ai.Command)
		merged, mergeErr = runAI(ai, prompt, w)
		if mergeErr == nil {
			break
		}
		fmt.Fprintf(w, "  %s failed: %v\n", ai.Name, mergeErr)
	}
	if mergeErr != nil {
		return "", fmt.Errorf("all AI tools failed, last error: %w", mergeErr)
	}
	if err := validateMergedContent(currentContent, merged); err != nil {
		return "", fmt.Errorf("invalid AI output: %w", err)
	}
	return merged, nil
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

func collectUnmergedLearnings(entries []LedgerEntry) ([]string, []string) {
	var entryIDs []string
	var learnings []string
	for _, e := range entries {
		if e.CommitSHA != "" || len(e.Learnings) == 0 {
			continue
		}
		entryIDs = append(entryIDs, e.ID)
		learnings = append(learnings, e.Learnings...)
	}
	return entryIDs, learnings
}

func validateMergedContent(original, merged string) error {
	originalTrimmed := strings.TrimSpace(original)
	mergedTrimmed := strings.TrimSpace(merged)

	if mergedTrimmed == "" {
		return fmt.Errorf("AI output is empty")
	}
	if strings.HasPrefix(mergedTrimmed, "```") {
		return fmt.Errorf("AI output appears to be wrapped in code fences")
	}
	if strings.HasPrefix(originalTrimmed, "---") && !strings.HasPrefix(mergedTrimmed, "---") {
		return fmt.Errorf("AI output dropped YAML frontmatter")
	}
	if strings.Contains(originalTrimmed, "\n") && !strings.Contains(mergedTrimmed, "\n") {
		return fmt.Errorf("AI output is unexpectedly short")
	}
	if len(originalTrimmed) >= 200 && len(mergedTrimmed) < len(originalTrimmed)/2 {
		return fmt.Errorf("AI output is much shorter than the current SKILL.md")
	}
	return nil
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

// reorderFlags moves flag-like arguments (starting with "-") before positional
// arguments so that Go's flag package parses them correctly.
func reorderFlags(args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			flags = append(flags, args[i])
			// If this flag takes a value (e.g. --cache-dir /tmp), grab the next arg too.
			if i+1 < len(args) && !strings.Contains(args[i], "=") && !strings.HasPrefix(args[i+1], "-") {
				// Check if it's a boolean flag (--all, --yes) — those don't take a value.
				name := strings.TrimLeft(args[i], "-")
				if name != "all" && name != "yes" {
					i++
					flags = append(flags, args[i])
				}
			}
		} else {
			positional = append(positional, args[i])
		}
	}
	return append(flags, positional...)
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
