# Waggle

**The communication layer for AI coding agents.**

AI coding agents can't talk to each other. Claude Code doesn't know what Gemini CLI is doing. Codex can't see Augment Code's work. They edit the same repo in parallel, blind to each other.

Waggle fixes this. It's a lightweight messaging protocol that any agent speaks through shell commands — no SDK, no integration, no MCP required.

```
  Terminal 1          Terminal 2          Terminal 3
  ┌─────────┐        ┌─────────┐        ┌─────────┐
  │ Claude   │        │ Gemini  │        │ Codex   │
  │ Code     │        │ CLI     │        │         │
  └────┬─────┘        └────┬────┘        └────┬────┘
       │                   │                   │
       │    safe surface    │    safe surface    │    safe surface
       ▼                    ▼                    ▼
  ┌──────────────────────────────────────────────────┐
  │         waggle machine runtime                    │
  │   notifications · unread cache · watch set       │
  │   (one per machine, thin tool adapters)          │
  └──────────────────────────────────────────────────┘
                         │
                         │ broker transport
                         ▼
  ┌──────────────────────────────────────────────────┐
  │              waggle broker                        │
  │   tasks · events · locks · sessions              │
  │   (auto-started, one per project)                │
  └──────────────────────────────────────────────────┘
```

An orchestrator agent sends instructions. Worker agents receive them. Any agent can be either — roles are fluid. If it can run `waggle <command>`, it's in.

---

## Install

### Pre-built binaries (recommended)

Download from [GitHub Releases](https://github.com/seungpyoson/waggle/releases) — no Go required. Pick the binary for your OS and CPU:

```bash
# Choose one: waggle-darwin-arm64, waggle-darwin-amd64,
# waggle-linux-amd64, or waggle-linux-arm64.
WAGGLE_ASSET=waggle-darwin-arm64

curl -L "https://github.com/seungpyoson/waggle/releases/latest/download/${WAGGLE_ASSET}" -o waggle
chmod +x waggle
./waggle --help
```

### From source

```bash
go install github.com/seungpyoson/waggle@latest
```

> **PATH check:** If `waggle` isn't found after install, add Go's bin to your PATH:
> ```bash
> export PATH="$PATH:$(go env GOPATH)/bin"
> ```
> Add this line to your `~/.zshrc` or `~/.bashrc` to make it permanent.

### Build locally

```bash
git clone https://github.com/seungpyoson/waggle.git
cd waggle && go build -o waggle .
# Optionally copy to a directory in your PATH:
cp waggle /usr/local/bin/   # or ~/bin/
```

Single binary. No dependencies. Works on macOS and Linux.

### Tool integrations

Install the thin adapter surfaces Waggle currently ships:

```bash
waggle install claude-code
waggle install codex
```

Current scope in this branch:

- Claude Code adapter is shipped
- Codex adapter is shipped in this branch
- Gemini CLI and Augment Code remain future work

---

## 30-Second Demo

```bash
# Terminal 1 — orchestrator agent
cd your-project
waggle task create '{"desc": "fix auth bug"}' --type fix --priority 10
waggle task create '{"desc": "write tests"}' --type test --depends-on 1
waggle events subscribe task.events    # watch everything happen
```

```bash
# Terminal 2 — worker agent (Claude Code, Codex, Gemini, or a script)
cd your-project
TASK=$(waggle task claim --type fix)
TOKEN=$(echo $TASK | jq -r '.data.ClaimToken')
ID=$(echo $TASK | jq -r '.data.ID')
# ... do the work ...
waggle task complete $ID '{"commit": "abc123"}' --token $TOKEN
# Task 2 auto-unblocks — another agent can claim it now
```

The broker starts automatically. Agents find each other through the shared project. No setup.

---

## Why Waggle?

AI coding agents are powerful individually. But they're isolated. Each runs in its own terminal, its own context, its own world. There's no way for a Claude Code session to tell a Codex session "I'm done with the plan, start implementing." No way for a test runner to tell the orchestrator "all tests pass, ship it."

Waggle is the missing communication layer:

- **Messages** — agents send and receive through tasks, events, and locks
- **Coordination** — "I'm editing auth.py" prevents conflicts without agents knowing about each other
- **Orchestration** — one agent decomposes a problem into tasks, others pick them up
- **Dependencies** — "don't start testing until implementation is done"
- **Any platform** — Claude Code, Gemini CLI, Codex, Augment Code, bash scripts — all speak the same protocol

Think of it like HTTP for the web, but for AI agents working on code. The protocol is simple (shell commands + JSON), so any agent that can run bash is instantly compatible.

---

## How It Works

**Per-project broker, machine-local runtime.** Waggle identifies your project by the git repo (root commit hash). All agents in the same repo — even in different clones, worktrees, or sandboxes — automatically share one broker. Automatic delivery runs through a separate machine-local runtime that watches `(project_id, agent_name)` pairs, stores unread local records, and emits OS notifications.

**Hooks stay thin.** The shipped Claude Code and Codex adapters do not launch persistent listeners themselves. They register watch intent with `waggle runtime watch`, then read unread local records with `waggle runtime pull` at safe interaction boundaries. Gemini CLI and Augment Code are not part of this branch yet.

**Cheapness is a product bar.** Waggle must stay boringly cheap under load: one machine-local runtime process max, thin bounded hooks, no per-watch retry polling, no adapter-side process fanout, and safe collapse under failure.

**Four communication primitives:**

| Primitive | What it does | Example |
|-----------|-------------|---------|
| **Tasks** | Durable work queue with claim tokens | Orchestrator posts, workers claim and complete |
| **Events** | Real-time pub/sub fire-and-forget | Subscribe to `task.events` to watch all state changes |
| **Locks** | Advisory resource coordination | "I'm editing this file, don't touch it" |
| **Status** | Shared system state | Who's connected, what's queued, what's locked |

**Crash recovery built in.** If an agent crashes, the broker detects the broken connection and re-queues its work. Claimed tasks have leases — expire without heartbeat and they're back in the queue.

**Everything is JSON.** All input and output is NDJSON. Agents parse with `jq` or any JSON library.

---

## Task Lifecycle

```
pending (blocked) ──▶ pending (eligible) ──▶ claimed ──▶ completed
                                                │
                                                ├──▶ failed
                                                └──▶ canceled
```

- **Blocked** — waiting for dependencies to complete
- **Eligible** — ready to be claimed
- **Claimed** — an agent owns it (with a lease + claim token)
- **Completed / Failed / Canceled** — terminal states

If a claimed task's lease expires (agent went silent), the broker re-queues it automatically.

---

## CLI Reference

### Tasks

```bash
# Create
waggle task create '{"desc": "..."}' --type fix --priority 10 --depends-on 1,2

# Claim (returns claim token)
waggle task claim --type fix --tags urgent

# Complete / Fail
waggle task complete <id> '{"result": "..."}' --token <token>
waggle task fail <id> "reason" --token <token>

# Monitor
waggle task list --state pending
waggle task get <id>
waggle task heartbeat <id> --token <token>
waggle task cancel <id>
```

### Locks

```bash
waggle lock file:src/auth.py       # Advisory — signals intent
waggle unlock file:src/auth.py
waggle locks                        # List all active locks
```

### Events

```bash
waggle events subscribe task.events  # Stream events (blocks until Ctrl+C)
waggle events publish my.topic '{"data": "..."}'
```

### Broker

```bash
waggle start                  # Usually auto-starts
waggle stop
waggle status                 # Sessions, tasks, locks
```

### Runtime

```bash
waggle runtime start                    # Start machine-local runtime daemon
waggle runtime status
waggle runtime watch claude-main        # Reliable explicit fallback
waggle runtime watches
waggle runtime pull claude-main         # Adapter-safe unread surfacing
```

---

## Communication Patterns

### Worker — Claims and Completes Tasks

```bash
#!/bin/bash
while true; do
  TASK=$(waggle task claim --type code-edit)
  if [ $? -eq 0 ]; then
    ID=$(echo $TASK | jq -r '.data.ID')
    TOKEN=$(echo $TASK | jq -r '.data.ClaimToken')

    # Do the work...

    waggle task complete $ID '{"status": "done"}' --token $TOKEN
  else
    sleep 5  # No tasks available
  fi
done
```

### Orchestrator — Decomposes and Delegates

```bash
#!/bin/bash
# Create a dependency chain
T1=$(waggle task create '{"desc": "write tests"}' --type test | jq -r '.data.ID')
T2=$(waggle task create '{"desc": "implement"}' --type code --depends-on $T1 | jq -r '.data.ID')
T3=$(waggle task create '{"desc": "review"}' --type review --depends-on $T2 | jq -r '.data.ID')

# Monitor
waggle events subscribe task.events
```

### Mutual Exclusion — Agents Avoid Conflicts

```bash
# Lock a file before editing
if waggle lock "file:src/main.go"; then
  # Safe to edit
  waggle unlock "file:src/main.go"
else
  echo "Locked by another agent, picking different task"
fi
```

---

## Multi-Clone / Worktree Support

Waggle identifies your project by the git repo's root commit — not the filesystem path. This means:

- Two clones of the same repo share one broker
- Git worktrees share one broker with the main checkout
- `WAGGLE_PROJECT_ID=myproject` forces a specific identity (for cross-repo coordination or sandboxes)

No setup needed. It just works.

---

## Architecture

```
waggle (single binary)
├── CLI client          — parses commands, talks to broker/runtime
├── Broker daemon       — per-project transport and state
└── Runtime daemon      — machine-local watches, notifications, unread cache

Broker internals:
├── Events module       — in-memory pub/sub, fire-and-forget
├── Tasks module        — SQLite-backed queue with claim/lease/deps
└── Locks module        — in-memory advisory locks, connection-tied
```

**Design principles:**
- **Zero config** — auto-detects project, auto-starts broker
- **Broker is dumb, agents are smart** — broker routes messages, agents decide what work means
- **One watch model** — manual registration, spawn, and tool hooks all feed the same runtime watch set
- **Safe automatic delivery** — Claude Code no longer starts `waggle listen` from `SessionStart`
- **Roles are fluid** — any session can create tasks, claim tasks, or both
- **Fail loud** — errors are JSON with codes, never silent

Full spec: [`docs/superpowers/specs/2026-03-24-waggle-design.md`](docs/superpowers/specs/2026-03-24-waggle-design.md)

---

## Configuration

| Method | Purpose |
|--------|---------|
| `WAGGLE_PROJECT_ID` | Force a specific project identity (cross-repo, sandboxes) |
| `WAGGLE_ROOT` | Override project root detection (non-git projects) |

Waggle stores state at `~/.waggle/`:
```
~/.waggle/
├── data/<hash>/        # Per-project: DB, PID, logs
└── sockets/<hash>/     # Per-project: Unix domain socket
```

---

## Development

```bash
go build -o waggle .
go test ./... -race -v
go vet ./...
```

Project structure:
```
internal/
├── config/     — Project identity, path resolution, defaults
├── protocol/   — Wire format (NDJSON types)
├── events/     — In-memory pub/sub hub
├── tasks/      — SQLite task store + dependencies + leases
├── locks/      — Advisory lock manager
├── broker/     — Socket listener, sessions, command routing
└── client/     — Shared client for CLI
cmd/            — Cobra CLI commands
```

---

## License

MIT
