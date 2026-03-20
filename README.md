# skillweave

MCP server that keeps SKILL.md files up to date as you work. Register a skill, the LLM loads it at session start, learns from corrections, and writes the updated skill back. Push to GitHub as a PR when ready.

## Install

```bash
# Private repo — tell Go to skip the checksum database
go env -w GOPRIVATE=github.com/IgorTodorovskiIBM/skillweave

go install github.com/IgorTodorovskiIBM/skillweave@latest
```

Make sure `$GOPATH/bin` is in your PATH (usually `~/go/bin`):

```bash
export PATH="$PATH:$(go env GOPATH)/bin"
```

## Quick start

```bash
# Register a skill and get your MCP config (many URL formats work)
skillweave setup https://github.com/user/repo/blob/main/skills/my-skill/SKILL.md

# Or with shorthand + explicit path
skillweave setup user/repo --path skills/my-skill/SKILL.md

# Paste the printed JSON into your MCP client config (.mcp.json)
# Start a session — the skill appears as a tool
```

## How it works

1. **Register a skill** — point at a GitHub URL
2. **Each skill becomes an MCP tool** — the LLM calls it to load the skill
3. **Work normally** — the LLM picks up on corrections and patterns
4. **LLM calls `skill_update`** — saves the updated SKILL.md locally
5. **Push when ready** — creates a PR for the team to review

## CLI

| Command | Description |
|---------|-------------|
| `setup <url>` | Register a skill and print MCP config |
| `status` | Show skills, unmerged learnings, AI tools |
| `unregister <name>` | Remove a registered skill |
| `list` | List registered skills |
| `push <name>` | Push skill updates as a PR (`-m`, `--no-pr`, `--ai`, `--dry-run`) |
| `ai add\|list\|remove\|reorder` | Configure AI tools for merging learnings |
| `ledger list\|delete\|clear` | Manage the update ledger |
| `gc` | Clean up stale cache repos and old merged ledger entries |

### Pushing updates

```bash
skillweave push zos-porting-cli                      # auto-generated commit message
skillweave push -m "Add patch tips" zos-porting-cli   # custom message
skillweave push --dry-run zos-porting-cli              # preview diff without pushing
```

If there are unmerged learnings, `push` uses a configured AI tool to merge them into the SKILL.md before committing. If no AI tools are configured, it tries `bob` and `claude` from PATH automatically.

## MCP tools

Each registered skill becomes a tool named `skill_<name>` (e.g. `skill_zos_porting_cli`). Two additional tools are always available:

| Tool | Description |
|------|-------------|
| `skill_update` | Save updated SKILL.md locally with learnings |
| `skill_push` | Push changes to GitHub as a PR |

## Configuration

Config lives in `~/.skillweave/skills.json`. Override the directory with `SKILLWEAVE_DIR` env var or `--cache-dir` flag.

## Build from source

```bash
git clone https://github.com/IgorTodorovskiIBM/skillweave.git
cd skillweave
go build .
```
