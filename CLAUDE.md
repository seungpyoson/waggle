# Waggle — AI Agent Integration Guide

This guide is for AI coding agents (Claude Code, Gemini CLI, Codex, custom agents) working with Waggle.

## Quick Reference

### Build & Test

```bash
# Build
go build -o waggle .

# Run all tests
go test ./... -v

# Run specific package
go test ./internal/tasks/ -v -run TestName

# E2E tests
go test -v -run TestE2E -count=1
```

### Design Principles

Read `docs/superpowers/specs/2026-03-24-waggle-design.md` before making changes.

**Key rules:**
- **Zero hardcodes** — No magic numbers, paths, or timeouts outside config
- **Single source of truth** — Every piece of state has exactly one owner
- **No dual paths** — One way to do each thing
- **Fail loud** — Errors surface immediately with context
- **Broker is dumb, agents are smart** — Broker routes, agents own semantics

### Code Structure

```
internal/
├── config/      # Path resolution, all defaults (THE source of truth)
├── protocol/    # Wire format types (public contract, change carefully)
├── events/      # In-memory pub/sub hub
├── tasks/       # SQLite task store, dependencies, lease management
├── locks/       # Advisory lock manager
├── broker/      # Socket listener, session management, command routing
└── client/      # Shared client for CLI commands

cmd/             # Cobra CLI commands
```

**Important:**
- `internal/config/` is the single source of truth for all defaults
- `internal/protocol/` defines the wire format — changes affect all clients
- Never hardcode paths, ports, timeouts, or limits outside of config

## Agent Workflow Patterns

### Pattern 1: Simple Worker

Claim tasks, do work, complete them.

```bash
#!/bin/bash
# Simple worker loop

while true; do
  TASK=$(waggle task claim --type code-edit)
  
  if [ $? -eq 0 ]; then
    ID=$(echo $TASK | jq -r '.data.id')
    TOKEN=$(echo $TASK | jq -r '.data.claim_token')
    PAYLOAD=$(echo $TASK | jq -r '.data.payload')
    
    # Do the work
    # ...
    
    # Heartbeat every 2 minutes to keep lease alive
    waggle task heartbeat $ID --token $TOKEN &
    
    # Complete when done
    waggle task complete $ID '{"status": "done"}' --token $TOKEN
  else
    sleep 5
  fi
done
```

### Pattern 2: Controller/Orchestrator

Decompose work, create tasks with dependencies, monitor progress.

```bash
#!/bin/bash
# Controller pattern

# Create task graph
T1=$(waggle task create '{"desc": "write tests"}' --type test | jq -r '.data.id')
T2=$(waggle task create '{"desc": "implement"}' --type code --depends-on $T1 | jq -r '.data.id')
T3=$(waggle task create '{"desc": "docs"}' --type docs --depends-on $T2 | jq -r '.data.id')

# Monitor completion
waggle events subscribe task.events | while read EVENT; do
  TYPE=$(echo $EVENT | jq -r '.event')
  ID=$(echo $EVENT | jq -r '.id')
  
  if [ "$TYPE" = "task.completed" ]; then
    echo "Task $ID completed"
  elif [ "$TYPE" = "task.failed" ]; then
    echo "Task $ID failed, creating retry task"
    # Handle failure
  fi
done
```

### Pattern 3: Lock Coordination

Use advisory locks to prevent conflicts.

```bash
#!/bin/bash
# Lock before editing

acquire_lock() {
  local resource=$1
  waggle lock "$resource" 2>/dev/null
  return $?
}

release_lock() {
  local resource=$1
  waggle unlock "$resource"
}

# Try to acquire lock
if acquire_lock "file:src/auth.py"; then
  echo "Lock acquired"
  
  # Do work
  # ...
  
  release_lock "file:src/auth.py"
else
  echo "Resource locked by another agent"
  # Pick different task or wait
fi
```

### Pattern 4: Event-Driven Agent

React to events in real-time.

```bash
#!/bin/bash
# Event-driven agent

waggle events subscribe task.events | while read EVENT; do
  TYPE=$(echo $EVENT | jq -r '.event')
  
  case $TYPE in
    task.created)
      # New task available
      ;;
    task.failed)
      # Task failed, maybe retry
      ;;
    task.completed)
      # Task done, maybe trigger next step
      ;;
  esac
done
```

## Best Practices

### 1. Always Use Claim Tokens

Never complete a task without the claim token. The token proves you still own the task.

```bash
# ✅ Correct
TASK=$(waggle task claim --type test)
TOKEN=$(echo $TASK | jq -r '.data.claim_token')
waggle task complete $ID '{"result": "pass"}' --token $TOKEN

# ❌ Wrong - missing token
waggle task complete $ID '{"result": "pass"}'
```

### 2. Heartbeat Long-Running Tasks

Default lease is 5 minutes. For longer tasks, send heartbeats.

```bash
# Start heartbeat in background
(
  while true; do
    sleep 120  # Every 2 minutes
    waggle task heartbeat $ID --token $TOKEN
  done
) &
HEARTBEAT_PID=$!

# Do work
# ...

# Stop heartbeat
kill $HEARTBEAT_PID
```

### 3. Handle Dependencies Correctly

Tasks with `--depends-on` stay blocked until dependencies complete.

```bash
# Create dependency chain
T1=$(waggle task create '{"step": 1}' | jq -r '.data.id')
T2=$(waggle task create '{"step": 2}' --depends-on $T1 | jq -r '.data.id')

# T2 won't be claimable until T1 completes
```

### 4. Use Idempotency Keys

Prevent duplicate task creation.

```bash
# Safe to call multiple times
waggle task create '{"desc": "deploy"}' --idempotency-key "deploy-v1.2.3"
```

### 5. Namespace Locks

Use prefixes to organize lock resources.

```bash
waggle lock "file:src/auth.py"
waggle lock "module:auth"
waggle lock "db:users_table"
```

### 6. Subscribe to Specific Topics

Use topic names to organize events.

```bash
# Task lifecycle events
waggle events subscribe task.events

# Custom agent events
waggle events subscribe agent.status
waggle events subscribe build.results
```

## Common Patterns

### Retry Failed Tasks

```bash
# Monitor for failures and retry
waggle events subscribe task.events | while read EVENT; do
  if [ "$(echo $EVENT | jq -r '.event')" = "task.failed" ]; then
    ID=$(echo $EVENT | jq -r '.id')
    ORIGINAL=$(waggle task get $ID)
    PAYLOAD=$(echo $ORIGINAL | jq -r '.data.payload')
    
    # Create retry task
    waggle task create "$PAYLOAD" --type retry --priority 5
  fi
done
```

### Parallel Task Execution

```bash
# Create multiple independent tasks
for i in {1..5}; do
  waggle task create "{\"file\": \"test_$i.py\"}" --type test
done

# Workers claim and execute in parallel
```

### Progress Reporting

```bash
# Publish progress events
waggle events publish agent.progress "{\"task\": $ID, \"percent\": 50}"
```

## Commit Convention

Use conventional commits:

```bash
git commit -m "feat: add task priority support"
git commit -m "fix: handle lease expiry correctly"
git commit -m "test: add dependency cycle detection test"
git commit -m "docs: update CLI reference"
git commit -m "chore: update dependencies"
```

## Troubleshooting

### Broker Not Starting

```bash
# Check logs
cat .waggle/broker.log

# Manually start in foreground
waggle start --foreground
```

### Socket Path Too Long

Waggle uses `~/.waggle/sockets/<hash>/broker.sock` to avoid macOS 104-byte limit.

If you see socket errors, check:
```bash
echo $HOME  # Should be set
ls ~/.waggle/sockets/
```

### Task Stuck in Claimed State

If a worker crashes, the task will be re-queued after lease expiry (default 5 minutes).

To force re-queue:
```bash
waggle stop
waggle start
```

### Lock Not Released

Locks are tied to connections. If a worker crashes, locks are auto-released when the connection drops.

## Testing

### Unit Tests

```bash
# Test specific package
go test ./internal/tasks/ -v

# Test with coverage
go test ./... -cover
```

### E2E Tests

```bash
# Run full E2E suite
go test -v -run TestE2E -count=1

# E2E tests build the binary and test real workflows
```

### Manual Testing

```bash
# Terminal 1: Start broker
waggle start --foreground

# Terminal 2: Create tasks
waggle task create '{"test": 1}' --type test

# Terminal 3: Worker
waggle task claim --type test

# Terminal 4: Monitor
waggle events subscribe task.events
```

## Further Reading

- [Design Spec](docs/superpowers/specs/2026-03-24-waggle-design.md) — Full architecture
- [Task Plans](docs/superpowers/plans/) — Implementation details
- [Protocol Types](internal/protocol/types.go) — Wire format reference

