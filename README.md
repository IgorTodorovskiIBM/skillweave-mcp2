# skillweave

**Shared memory for AI coding agents.**

skillweave is an MCP server that turns corrections, patterns, and hard-won knowledge into persistent SKILL.md files that every team member's AI agent can learn from. Stop re-explaining the same things across sessions and across people — teach it once, share it with the team via Git.

### The problem

AI coding agents forget everything between sessions. You correct the same mistakes, re-explain the same patterns, and watch the same wrong approaches play out — over and over. Multiply that by every person on your team, each discovering the same gotchas independently.

### The solution

skillweave captures learnings as you work and stores them in version-controlled SKILL.md files. When a new session starts, the agent loads the skill and already knows what took you hours to teach. When you push, the whole team benefits.

```
You: "No, use %w not %v for error wrapping here"
Agent: [calls skill_note] → noted.
                            ↓
                    next session, every team member's agent knows this
```

## Install

```bash
go install github.com/IgorTodorovskiIBM/skillweave@latest
```

Make sure `$GOPATH/bin` is in your PATH (usually `~/go/bin`):

```bash
export PATH="$PATH:$(go env GOPATH)/bin"
```

## Quick start

```bash
# Point at an existing skill
skillweave setup https://github.com/user/repo/blob/main/skills/my-skill/SKILL.md

# Or start a brand-new skill from scratch
skillweave setup user/repo --path skills/my-skill/SKILL.md

# Paste the printed JSON into your MCP client config (.mcp.json)
# Start a session — the skill loads automatically
```

That's it. The agent now reads the skill at the start of every session, captures corrections as you work, and can push updates back as a PR.

## How it works

1. **Register** — point at a GitHub repo (works even if the SKILL.md doesn't exist yet)
2. **Load** — each skill becomes an MCP tool; the agent calls it to load context
3. **Learn** — as you work, the agent calls `skill_note` whenever it gets corrected or discovers something new
4. **Push** — `skill_push` merges notes into the SKILL.md and opens a PR
5. **Share** — the whole team's agents pick up the updated skill next session

## CLI

| Command | Description |
|---------|-------------|
| `setup <url>` | Register a skill and print MCP config |
| `push <name>` | Push skill updates as a PR |
| `status` | Show skills, unmerged learnings, AI tools |
| `ledger list\|review\|delete\|clear` | Manage captured learnings |
| `ai add\|list\|remove\|reorder` | Configure AI tools for merging |
| `unregister <name>` | Remove a registered skill |
| `gc` | Clean up stale caches and old ledger entries |

### Pushing updates

```bash
skillweave push zos-porting-cli                       # auto-merge notes + push
skillweave push -m "Add patch tips" zos-porting-cli   # custom commit message
skillweave push --dry-run zos-porting-cli              # preview diff first
```

When pushing, skillweave uses a configured AI tool to intelligently merge accumulated notes into the SKILL.md. If none are configured, it tries `bob` and `claude` from PATH automatically.

## MCP tools

Each registered skill becomes a tool named `skill_<name>` (e.g. `skill_zos_porting_cli`). Additional tools are always available:

| Tool | Description |
|------|-------------|
| `skill_note` | Jot down a learning or correction (one line, merged at push time) |
| `skill_update` | Full rewrite of the SKILL.md with learnings |
| `skill_read` | Re-read the current SKILL.md without creating a new session |
| `skill_list_notes` | See all unmerged notes for a skill |
| `skill_push` | Merge notes, commit, push, and open a PR |

For new skills (no SKILL.md in the repo yet), all tools work normally — the first `skill_push` creates the file in the remote repo.

## Configuration

Config lives in `~/.skillweave/skills.json`. Override the directory with `SKILLWEAVE_DIR` env var or `--cache-dir` flag.

## Build from source

```bash
git clone https://github.com/IgorTodorovskiIBM/skillweave.git
cd skillweave
go build .
```
