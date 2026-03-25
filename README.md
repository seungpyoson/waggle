# Waggle

**The messaging protocol for AI coding agents.**

Waggle is a lightweight coordination layer that lets independent AI agent sessions — Claude Code, Gemini CLI, Codex, Augment Code, or plain bash scripts — talk to each other. Post tasks, claim work, lock files, stream events. Any process that can run a shell command speaks Waggle.

```
            You (human)
                │
        waggle task create
                │
    ┌───────────┼───────────┐
    ▼           ▼           ▼
 Agent 1     Agent 2     Agent 3
  (Claude)   (Gemini)    (Codex)
    │           │           │
    └───────────┼───────────┘
                │
          waggle broker
       (auto-started, per-project)
```

No SDK. No integration. No MCP. Just `waggle <command>`.

---

## Install

### Pre-built binaries (recommended)

Download from [GitHub Releases](https://github.com/seungpyoson/waggle/releases) — no Go required.

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

---

## 30-Second Demo

```bash
cd your-project

# Post some work
waggle task create '{"desc": "fix auth bug"}' --type fix --priority 10
waggle task create '{"desc": "write tests"}' --type test --depends-on 1

# Agent claims highest-priority eligible task
TASK=$(waggle task claim --type fix)
ID=$(echo $TASK | jq -r '.data.ID')
TOKEN=$(echo $TASK | jq -r '.data.ClaimToken')

# Agent finishes
waggle task complete $ID '{"commit": "abc123"}' --token $TOKEN

# Task 2 auto-unblocks (dependency resolved)
waggle task list
```

The broker starts automatically on your first command. No setup needed.

---

## Why Waggle?

You open three terminals. Claude Code in one, Gemini CLI in another, a test runner script in the third. They're all editing the same repo, but they can't see each other.

Waggle gives them a shared work queue:

- **Tasks** — one agent posts work, another claims and completes it
- **Dependencies** — task B waits until task A finishes
- **Locks** — "I'm editing auth.py, don't touch it"
- **Events** — real-time notifications when things happen
- **Priority** — urgent work gets claimed first

Agents don't need to know about each other. They just talk to the broker.

---

## How It Works

**One broker per project.** Waggle detects your project from the git repo and starts a broker automatically. All agents in the same repo share the same broker — even across different clones and worktrees.

**Stateful connections.** Each command connects, does its thing, and disconnects cleanly. Claimed tasks survive disconnects (your agent can claim in one command and complete in another). If an agent crashes mid-task, the broker detects the broken connection and re-queues the work.

**Everything is JSON.** All output is machine-readable NDJSON. Agents parse it with `jq` or any JSON library.

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

---

## Agent Patterns

### Simple Worker

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

### Controller (Task Graph)

```bash
#!/bin/bash
# Create a dependency chain
T1=$(waggle task create '{"desc": "write tests"}' --type test | jq -r '.data.ID')
T2=$(waggle task create '{"desc": "implement"}' --type code --depends-on $T1 | jq -r '.data.ID')
T3=$(waggle task create '{"desc": "review"}' --type review --depends-on $T2 | jq -r '.data.ID')

# Monitor
waggle events subscribe task.events
```

### Cross-Agent Coordination

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
├── CLI client          — parses commands, talks to broker
└── Broker daemon       — manages state, auto-started per project

Broker internals:
├── Events module       — in-memory pub/sub, fire-and-forget
├── Tasks module        — SQLite-backed queue with claim/lease/deps
└── Locks module        — in-memory advisory locks, connection-tied
```

**Design principles:**
- **Zero config** — auto-detects project, auto-starts broker
- **Broker is dumb, agents are smart** — broker routes messages, agents decide what work means
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
