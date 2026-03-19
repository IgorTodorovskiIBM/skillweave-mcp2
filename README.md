# skillweave

MCP server that keeps SKILL.md files up to date as you work. Register a skill, the LLM loads it at session start, learns from corrections during the session, and writes the updated skill locally. Push to GitHub as a PR when ready to share with the team.

## Quick start

```bash
# Register a skill (auto-detects local checkout if you're inside the repo)
skillweave register \
  https://github.com/IgorTodorovskiIBM/zos-porting/blob/main/skills/zos-porting-cli/SKILL.md

# Start the MCP server (stdio mode)
./skillweave
```

## How it works

1. **You register a skill once** via CLI, pointing at a GitHub URL and optionally your local checkout
2. **Each skill becomes its own MCP tool** (`skill_zos_porting_cli`) with the description from the SKILL.md frontmatter
3. **On startup, the server checks for remote updates** — fetches and compares against the cache, pulling only when the skill file has changed
4. **The LLM calls the skill tool** to load it — no separate boot step needed
5. **You work normally** — the LLM pays attention to corrections and patterns it discovers
6. **The LLM calls `skill_update`** when it has enough learnings — writes the updated SKILL.md locally (both to cache and your working copy)
7. **You push via LLM or CLI** — `skill_push` (MCP) or `skillweave push` (CLI) creates a PR; teammates review and merge

The server instructions tell the LLM when to update: after multiple corrections on the same topic, when it discovers a new pattern, when the user asks, or when a session ends with meaningful learnings.

## CLI commands

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

# With explicit repo + path
skillweave register --repo git@github.com:user/repo.git --path skills/my-skill/SKILL.md

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
# Push with auto-generated commit message (merges unmerged learnings via bob)
skillweave push zos-porting-cli

# With a custom commit message
skillweave push -m "Add patch conflict tips" zos-porting-cli

# Push branch only, no PR
skillweave push --no-pr zos-porting-cli

# Use a specific bob binary path
skillweave push --bob /home/itodoro/bin/bob zos-porting-cli
```

If there are unmerged learnings in the ledger (from `skill_update` calls that haven't been pushed yet), `push` uses bob to intelligently merge them into the SKILL.md before committing. Bob's output streams with a `[bob]` prefix so you can see what's happening. If no unmerged learnings exist, it pushes the current SKILL.md as-is. After a successful push, learnings are marked as merged so they won't be reprocessed.

### `ledger`

```bash
# List all ledger entries for a skill (shows merged/unmerged status)
skillweave ledger list zos-porting-cli

# Delete a specific entry by ID
skillweave ledger delete zos-porting-cli <entry-id>

# Clear all entries for a skill
skillweave ledger clear zos-porting-cli
```

Config is stored in `~/.skillweave/skills.json`.

## MCP Tools

Each registered skill is exposed as its own tool, named `skill_<name>` (e.g. `skill_zos_porting_cli`). The tool description comes from the SKILL.md YAML frontmatter. Calling the tool loads the skill and returns its content + a session ID.

### `skill_update`

| Param | Required | Description |
|-------|----------|-------------|
| `session_id` | no | Session ID from the skill tool (optional if `skill_name` provided) |
| `skill_name` | no | Skill name as fallback when session is unavailable |
| `learnings` | yes | List of things learned (corrections, tips, patterns) |
| `updated_content` | yes | Full new SKILL.md with learnings incorporated |

Writes locally — to the cache and to your local checkout (if `--local-path` was registered). Also records a ledger entry. Accepts either `session_id` or `skill_name` — if the session expired (e.g. server restarted), `skill_name` creates an ad-hoc session from the registered config.

### `skill_push`

| Param | Required | Description |
|-------|----------|-------------|
| `session_id` | no | Session ID from the skill tool (optional if `skill_name` provided) |
| `skill_name` | no | Skill name as fallback when session is unavailable |
| `commit_message` | yes | Commit message |
| `skip_pr` | no | Skip PR creation, push branch only (default `false`) |

Creates a branch, commits, pushes, and opens a PR. Call `skill_update` first.

## Architecture

```
skillweave/
├── main.go           # CLI subcommands (register, unregister, list, push, ledger) + MCP server setup
├── config.go         # Skill registration (skills.json, GitHub URL parsing, frontmatter, local path detection)
├── tools.go          # Dynamic skill tools + skill_update + skill_push
├── git.go            # Git operations (clone, fetch, update check, commit, push, branch, PR)
├── session.go        # In-memory session state
├── ledger.go         # Immutable changelog entries (write, read, mark merged, delete, clear)
├── scripts/
│   ├── ssh-zos.sh              # SSH to z/OS through jump host
│   ├── sync-and-build.sh       # Rsync + remote Go build on z/OS
│   ├── start-mcp-server.sh     # Start server on z/OS with port forwarding
│   └── bootstrap-mcp-config.sh # Verify prereqs + print MCP config JSON
├── go.mod
└── Makefile
```

### Data layout

```
~/.skillweave/
├── skills.json       # Registered skills
├── repos/            # Cached repo clones (keyed by normalized URL hash)
│   └── <hash>/
└── ledger/           # Update history
    └── <repo-hash>/
        └── <skill-path-hash>/
            └── YYYY/MM/DD/
                └── <id>.json   # {learnings, commit_sha, pr_url, timestamp}
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
| `-http` | *(empty — stdio mode)* | HTTP listen address (e.g. `:8080`) |
| `-log-transport` | `false` | Log MCP transport frames to stderr |
| `-cache-dir` | `~/.skillweave` | Directory for cached repos, ledger, and config |

## z/OS deployment

Prerequisites: Go 1.23+, git, and `gh` (GitHub CLI).

```bash
./scripts/sync-and-build.sh              # rsync + go build on z/OS
./scripts/sync-and-build.sh --test       # also run go test
./scripts/start-mcp-server.sh            # start on z/OS with port forward (default 7377)
./scripts/bootstrap-mcp-config.sh        # verify prereqs + print MCP config JSON
```

### Environment overrides

| Variable | Default |
|----------|---------|
| `JUMP_USER` | `itodorov` |
| `JUMP_HOST` | `rogi21.fyre.ibm.com` |
| `ZOS_HOST` | `zoscan2b.pok.stglabs.ibm.com` |
| `ZOS_USER` | `itodoro` |
| `ZOS_DIR` | `skillweave` |
| `GO_BIN` | `/home/itodoro/install_test/go1.25/bin` |
| `RSYNC_PATH` | `/home/itodoro/zopen/usr/local/bin/rsync` |
