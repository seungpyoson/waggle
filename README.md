# Waggle

Agent session coordination broker for AI coding agents.

Waggle lets independent AI coding agent sessions (Claude Code, Gemini CLI, Codex, scripts) coordinate work on the same project through shell commands. Any process that can run bash can participate.

## What it does

- **Task distribution** — Post tasks, workers claim and complete them with dependency tracking
- **Coordination** — Advisory file locks prevent agents from stepping on each other
- **Event streaming** — Subscribe to real-time events (task completions, status changes)

## How it works

One broker per project, auto-started on first command. Agents communicate through a Unix domain socket using NDJSON protocol.

```bash
# Queue work
waggle task create '{"desc": "fix lint errors in src/auth.py"}' --type code-edit

# Worker claims next task
waggle task claim --type code-edit

# Check what's happening
waggle task list
waggle status
```

## Installation

### From source

```bash
go install github.com/seungpyoson/waggle@latest
```

### Build locally

```bash
git clone https://github.com/seungpyoson/waggle.git
cd waggle
go build -o waggle .
```

The binary is self-contained with no external dependencies.

## Quick Start

### 1. Create a task

```bash
cd your-project
waggle task create '{"desc": "implement login feature"}' --type feature
```

Output:
```json
{
  "ok": true,
  "data": {
    "ID": 1,
    "State": "pending",
    "Type": "feature",
    "Payload": "{\"desc\": \"implement login feature\"}"
  }
}
```

### 2. Claim and complete in a script

**Important:** Claim and complete must happen in the same process to maintain the session.

```bash
#!/bin/bash
# worker.sh

# Claim a task
TASK=$(waggle task claim --type feature)
ID=$(echo $TASK | jq -r '.data.ID')
TOKEN=$(echo $TASK | jq -r '.data.ClaimToken')

echo "Working on task $ID..."

# Do the work
# ...

# Complete the task
waggle task complete $ID '{"status": "done"}' --token $TOKEN
```

Output from claim:
```json
{
  "ok": true,
  "data": {
    "ID": 1,
    "ClaimToken": "abc123...",
    "Payload": "{\"desc\": \"implement login feature\"}"
  }
}
```

### 3. Monitor progress

```bash
# List all tasks
waggle task list

# Subscribe to task events (runs until interrupted)
waggle events subscribe task.events
```

## CLI Reference

### Daemon Management

```bash
# Start broker (usually auto-started)
waggle start

# Stop broker
waggle stop

# Check broker status
waggle status
```

### Task Commands

#### Create a task

```bash
waggle task create <payload> [flags]
```

Flags:
- `--type <type>` — Task type (e.g., `code-edit`, `test`, `review`)
- `--tags <t1,t2>` — Comma-separated tags for filtering
- `--depends-on <id1,id2>` — Task IDs that must complete first
- `--lease <seconds>` — Lease duration (default: 300)
- `--max-retries <n>` — Max retry attempts (default: 3)
- `--priority <n>` — Higher = claimed first (default: 0)
- `--idempotency-key <key>` — Prevent duplicate creates

Example:
```bash
waggle task create '{"file": "auth.py", "issue": "type error"}' \
  --type code-edit \
  --tags bug,urgent \
  --priority 10
```

#### List tasks

```bash
waggle task list [flags]
```

Flags:
- `--state <state>` — Filter by state (`pending`, `claimed`, `completed`, `failed`)
- `--type <type>` — Filter by task type
- `--owner <name>` — Filter by current owner

#### Claim a task

```bash
waggle task claim [flags]
```

Flags:
- `--type <type>` — Only claim tasks of this type
- `--tags <t1,t2>` — Only claim tasks with these tags

Returns the task with a `claim_token` needed for completion.

#### Complete a task

```bash
waggle task complete <id> <result> --token <claim_token>
```

Example:
```bash
waggle task complete 1 '{"status": "success"}' --token abc123...
```

#### Fail a task

```bash
waggle task fail <id> <reason> --token <claim_token>
```

#### Heartbeat (renew lease)

```bash
waggle task heartbeat <id> --token <claim_token>
```

#### Cancel a task

```bash
waggle task cancel <id>
```

#### Get task details

```bash
waggle task get <id>
```

### Event Commands

#### Subscribe to events

```bash
waggle events subscribe <topic>
```

Example:
```bash
waggle events subscribe task.events
```

Streams events as NDJSON:
```json
{"topic": "task.events", "event": "task.created", "id": 1, "ts": "2026-03-24T10:00:00Z"}
{"topic": "task.events", "event": "task.claimed", "id": 1, "by": "worker-1", "ts": "2026-03-24T10:00:05Z"}
{"topic": "task.events", "event": "task.completed", "id": 1, "result": {...}, "ts": "2026-03-24T10:05:00Z"}
```

#### Publish an event

```bash
waggle events publish <topic> <message>
```

Example:
```bash
waggle events publish agent.status '{"agent": "worker-1", "status": "idle"}'
```

### Lock Commands

Advisory locks for coordination:

```bash
# Acquire a lock
waggle lock <resource>

# Release a lock
waggle unlock <resource>

# List all locks
waggle locks
```

Example:
```bash
waggle lock file:src/auth.py
# ... do work ...
waggle unlock file:src/auth.py
```

Locks are automatically released when the session disconnects.

## Configuration

### Environment Variables

- `WAGGLE_ROOT` — Override project root detection (useful for testing)

### Project Configuration

Waggle stores state in `.waggle/` at your project root:

```
your-project/
├── .waggle/
│   ├── state.db        # SQLite task database
│   ├── config.json     # Per-project settings (optional)
│   ├── broker.pid      # Daemon process ID
│   ├── broker.log      # Structured logs
│   └── start.lock      # Prevents auto-start races
```

Socket location: `~/.waggle/sockets/<hash>/broker.sock` (hash based on project path)

### Auto-start Behavior

The broker auto-starts on the first `waggle` command and auto-stops after 30 minutes of inactivity (no connections and no pending tasks).

## Architecture

### System Overview

```
┌─────────────────────────────────────────────────────────┐
│                    Waggle Broker                        │
│  ┌──────────────┐              ┌──────────────┐        │
│  │   Events     │              │    Tasks     │        │
│  │  (pub/sub)   │              │  (SQLite)    │        │
│  │              │              │              │        │
│  │ • Subscribe  │              │ • Create     │        │
│  │ • Publish    │              │ • Claim      │        │
│  │ • Fan-out    │              │ • Complete   │        │
│  └──────────────┘              └──────────────┘        │
│         │                              │                │
│         └──────────┬───────────────────┘                │
│                    │                                    │
│              Unix Socket                                │
└────────────────────┼────────────────────────────────────┘
                     │
        ┌────────────┼────────────┐
        │            │            │
   ┌────▼───┐   ┌───▼────┐   ┌──▼─────┐
   │ Agent  │   │ Agent  │   │ Human  │
   │   1    │   │   2    │   │  CLI   │
   └────────┘   └────────┘   └────────┘
```

### Task Lifecycle

```
pending (blocked) → pending (eligible) → claimed → completed
                                           │
                                           ├──→ failed
                                           └──→ canceled
```

- **pending (blocked)**: Has incomplete dependencies
- **pending (eligible)**: Ready to be claimed
- **claimed**: Worker owns it (with lease)
- **completed**: Successfully finished
- **failed**: Explicitly failed or max retries exceeded
- **canceled**: Canceled by controller

### Design Principles

See [design spec](docs/superpowers/specs/2026-03-24-waggle-design.md) for full details:

- **Zero hardcodes** — All config in one place
- **Single source of truth** — Each piece of state has one owner
- **Broker is dumb, agents are smart** — Broker routes messages, agents own semantics
- **Fail loud** — Errors surface immediately with context
- **Stable CLI, evolvable internals** — CLI is the public API

## Integration Examples

### Claude Code Agent

```bash
#!/bin/bash
# worker.sh - Simple worker agent

while true; do
  # Claim next task
  TASK=$(waggle task claim --type code-edit)

  if [ $? -eq 0 ]; then
    ID=$(echo $TASK | jq -r '.data.ID')
    TOKEN=$(echo $TASK | jq -r '.data.ClaimToken')
    DESC=$(echo $TASK | jq -r '.data.Payload | fromjson | .desc')

    echo "Working on task $ID: $DESC"

    # Do the work (call Claude, run tests, etc.)
    # ...

    # Complete the task
    waggle task complete $ID '{"status": "done"}' --token $TOKEN
  else
    sleep 5
  fi
done
```

### Controller Pattern

```bash
#!/bin/bash
# controller.sh - Decompose work into tasks

# Create dependent tasks
TASK1=$(waggle task create '{"desc": "write tests"}' --type test)
ID1=$(echo $TASK1 | jq -r '.data.ID')

TASK2=$(waggle task create '{"desc": "implement feature"}' --type code-edit --depends-on $ID1)
ID2=$(echo $TASK2 | jq -r '.data.ID')

TASK3=$(waggle task create '{"desc": "update docs"}' --type docs --depends-on $ID2)

# Monitor progress
waggle events subscribe task.events | while read EVENT; do
  echo "Event: $EVENT"
done
```

### Lock Coordination

```bash
#!/bin/bash
# Safe file editing with locks

FILE="src/auth.py"

if waggle lock "file:$FILE"; then
  echo "Lock acquired, editing $FILE"

  # Edit the file
  # ...

  waggle unlock "file:$FILE"
  echo "Lock released"
else
  echo "File locked by another agent, skipping"
fi
```

## Contributing

### Build and Test

```bash
# Build
go build -o waggle .

# Run all tests
go test ./... -v

# Run E2E tests
go test -v -run TestE2E -count=1

# Run specific package tests
go test ./internal/tasks/ -v
```

### Project Structure

```
waggle/
├── cmd/                 # CLI commands (Cobra)
├── internal/
│   ├── broker/         # Socket listener, session management
│   ├── client/         # Shared client for CLI
│   ├── config/         # Path resolution, defaults
│   ├── events/         # In-memory pub/sub
│   ├── locks/          # Advisory lock manager
│   ├── protocol/       # Wire format types
│   └── tasks/          # SQLite task store
├── docs/               # Design specs and plans
└── main.go            # Entry point
```

### Commit Convention

Use conventional commits:
- `feat:` — New features
- `fix:` — Bug fixes
- `test:` — Test changes
- `docs:` — Documentation
- `chore:` — Maintenance

## License

MIT
