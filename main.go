package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

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
