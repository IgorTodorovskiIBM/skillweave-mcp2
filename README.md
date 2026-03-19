# skillweave

MCP server that keeps SKILL.md files up to date as you work. Register a skill, the LLM loads it at session start, learns from corrections during the session, and writes the updated skill locally. Push to GitHub as a PR when ready to share with the team.

## Install

```bash
go install github.com/IgorTodorovskiIBM/skillweave@latest
```

Or build from source:

```bash
git clone https://github.com/IgorTodorovskiIBM/skillweave.git
cd skillweave
go build .
```

## Quick start

```bash
# One command: register a skill and get your MCP config
skillweave setup https://github.com/user/repo/blob/main/skills/zos-porting-cli/SKILL.md

# Paste the printed JSON into your MCP client config (.mcp.json)
# Done. Start a session and the skill appears as a tool.
```

## How it works

1. **You register a skill once** — point at a GitHub URL, local checkout is auto-detected
2. **Each skill becomes its own MCP tool** (`skill_zos_porting_cli`) with the description from the SKILL.md frontmatter
3. **On startup, the server checks for remote updates** — fetches and pulls only when the skill file has changed
4. **The LLM calls the skill tool** to load it — no separate boot step needed
5. **You work normally** — the LLM pays attention to corrections and patterns it discovers
6. **The LLM calls `skill_update`** when it has enough learnings — writes the updated SKILL.md locally
7. **You push via LLM or CLI** — creates a PR; teammates review and merge

## CLI commands

### `setup`

One-command onboarding: registers a skill, fetches it, and prints your MCP config.

```bash
skillweave setup https://github.com/user/repo/blob/main/skills/my-skill/SKILL.md
```

### `status`

See everything at a glance: registered skills, unmerged learnings, AI tools.

```bash
skillweave status
```

### `register`

```bash
# From a GitHub blob URL (auto-derives name "zos-porting-cli" from path)
skillweave register https://github.com/user/repo/blob/main/skills/zos-porting-cli/SKILL.md

# Auto-detects --local-path if you're inside a matching git checkout
cd ~/Projects/zos-porting
skillweave register https://github.com/user/repo/blob/main/skills/zos-porting-cli/SKILL.md

# With explicit local checkout
skillweave register \
  --local-path ~/Projects/zos-porting \
  https://github.com/user/repo/blob/main/skills/zos-porting-cli/SKILL.md

# Override the auto-derived name
skillweave register --name my-custom-name https://github.com/user/repo/blob/main/SKILL.md
```

### `unregister`

```bash
skillweave unregister zos-porting-cli
```

### `list`

```bash
skillweave list
```

### `push`

```bash
# Push with auto-generated commit message
skillweave push zos-porting-cli

# With a custom commit message
skillweave push -m "Add patch conflict tips" zos-porting-cli

# Push branch only, no PR
skillweave push --no-pr zos-porting-cli

# Use a specific AI tool for merging
skillweave push --ai gemini zos-porting-cli
```

If there are unmerged learnings in the ledger, `push` uses a configured AI tool to merge them into the SKILL.md before committing. AI tools are tried in order (see `skillweave ai list`). If none are configured, it tries `bob` or `claude` from PATH automatically. Output streams with a `[toolname]` prefix. After a successful push, learnings are marked as merged.

### `ai`

Configure AI tools used for merging learnings during CLI push.

```bash
# Add AI tools (prompt is appended as the last argument)
skillweave ai add bob bob --yolo --output-format text
skillweave ai add gemini /path/to/gemini.mjs -p

# List configured tools (order = fallback priority)
skillweave ai list

# Reorder (first = tried first)
skillweave ai reorder gemini bob

# Remove
skillweave ai remove gemini
```

If no AI tools are configured, `push` automatically tries `bob` and `claude` from PATH.

### `ledger`

```bash
# List all ledger entries for a skill (shows merged/unmerged status)
skillweave ledger list zos-porting-cli

# Delete a specific entry by ID
skillweave ledger delete zos-porting-cli <entry-id>

# Clear all entries for a skill
skillweave ledger clear zos-porting-cli
```

## MCP Tools

Each registered skill is exposed as its own tool, named `skill_<name>` (e.g. `skill_zos_porting_cli`). The tool description comes from the SKILL.md YAML frontmatter.

### `skill_update`

| Param | Required | Description |
|-------|----------|-------------|
| `session_id` | no | Session ID from the skill tool (optional if `skill_name` provided) |
| `skill_name` | no | Skill name as fallback when session is unavailable |
| `learnings` | yes | List of things learned (corrections, tips, patterns) |
| `updated_content` | yes | Full new SKILL.md with learnings incorporated |

### `skill_push`

| Param | Required | Description |
|-------|----------|-------------|
| `session_id` | no | Session ID from the skill tool (optional if `skill_name` provided) |
| `skill_name` | no | Skill name as fallback when session is unavailable |
| `commit_message` | yes | Commit message |
| `skip_pr` | no | Skip PR creation, push branch only (default `false`) |

## Configuration

Config is stored in `~/.skillweave/skills.json` (or `$SKILLWEAVE_DIR/skills.json`).

| Setting | Description |
|---------|-------------|
| `SKILLWEAVE_DIR` | Override cache directory (default `~/.skillweave`) |
| `--cache-dir` | Per-command override (takes precedence over env var) |

## Architecture

```
skillweave/
├── main.go           # CLI (setup, register, push, status, ai, ledger) + MCP server
├── config.go         # Skills + AI tool config, GitHub URL parsing, frontmatter
├── tools.go          # Dynamic MCP tools (skill_*, skill_update, skill_push)
├── git.go            # Git operations (clone, fetch, update check, commit, push, PR)
├── session.go        # In-memory session state
├── ledger.go         # Update history (write, read, mark merged, delete, clear)
├── scripts/          # z/OS deployment scripts
├── go.mod
└── Makefile
```

### Data layout

```
~/.skillweave/
├── skills.json       # Registered skills + AI tool config
├── repos/            # Cached repo clones (keyed by normalized URL hash)
│   └── <hash>/
└── ledger/           # Update history
    └── <repo-hash>/
        └── <skill-path-hash>/
            └── YYYY/MM/DD/
                └── <id>.json
```

## Build

```bash
make build    # go build ./...
make run      # go run . (stdio mode)
make fmt      # gofmt
make lint     # go vet
make test     # go test
```

### Server flags

| Flag | Default | Description |
|------|---------|-------------|
| `-http` | *(stdio mode)* | HTTP listen address (e.g. `:8080`) |
| `-log-transport` | `false` | Log MCP transport frames to stderr |
| `-cache-dir` | `~/.skillweave` | Override cache directory |

## z/OS deployment

Prerequisites: Go 1.23+, git, and `gh` (GitHub CLI).

```bash
./scripts/sync-and-build.sh              # rsync + go build on z/OS
./scripts/sync-and-build.sh --test       # also run go test
./scripts/start-mcp-server.sh            # start on z/OS with port forward
./scripts/bootstrap-mcp-config.sh        # verify prereqs + print MCP config
```
