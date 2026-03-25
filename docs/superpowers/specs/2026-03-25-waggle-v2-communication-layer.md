# Waggle v2 — The Communication Layer

**Date:** 2026-03-25
**Status:** Design — validated by GPT-5.3-codex + Grok-4.2-multiagent
**Context:** v1 MVP is complete (tasks, events, locks, sessions). This spec addresses the fundamental UX gap: waggle is a tool agents must learn to use, not invisible infrastructure.

## The Problem

Waggle v1 works mechanically — all commands function, tests pass, broker runs. But in practice:

- Agents must be explicitly told waggle commands (violates zero-knowledge participation)
- The orchestrator said "I can't directly message the other session" (no direct messaging)
- A worker dropped its lease because it forgot to heartbeat (agents don't naturally maintain waggle state)
- The human switches between tabs relaying messages (human is the message bus)

**Root cause:** Waggle has the broker (backend) but no agent integration (frontend). It's a chat server with no chat client.

## Six Invariants

| # | Invariant | v1 Status |
|---|-----------|-----------|
| 1 | **Zero-knowledge participation** — agent starts, waggle just works | VIOLATED — requires explicit instructions |
| 2 | **Direct messaging** — `waggle send deployer "start step 2"` | MISSING — only pub/sub topics |
| 3 | **Automatic session awareness** — agents know who else is working | VIOLATED — no push, no presence |
| 4 | **Work flows without human relay** — no tab-switching to relay messages | VIOLATED — human is the message bus |
| 5 | **Reliable delivery** — messages have states: queued → pushed → seen → acked | MISSING — fire-and-forget only |
| 6 | **Recoverable context** — agent restart reconstructs tasks, messages, locks | PARTIAL — tasks survive, messages don't |

## Three Things to Build (in order)

### 1. Direct Messaging with Inbox + Presence

The primitive everything else depends on.

**Commands:**
```bash
waggle send <name> <message>              # send to specific agent
waggle send <name> <message> --await-ack  # block until acknowledged
waggle inbox                              # check unread messages
waggle presence                           # who's online, idle, busy
```

**Message lifecycle:**
```
queued → pushed → seen → acked
                    └→ expired (TTL)
```

**Design decisions:**
- Per-session **durable inbox** in SQLite. Messages survive broker restart.
- Push over persistent connection when receiver is connected.
- **Never interrupt mid-turn.** Queue messages, deliver at next safe moment (turn boundary / hook point).
- Priority levels: `critical` (interrupt-allowed), `normal` (turn-boundary), `bulk` (digest).
- Presence tracked by connection state + heartbeat: `online`, `busy`, `idle`, `offline`.
- `--await-ack` lets orchestrators block on verifiable state, not chat text.

**Schema additions:**
```sql
CREATE TABLE messages (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    from_name   TEXT NOT NULL,
    to_name     TEXT NOT NULL,
    body        TEXT NOT NULL,
    priority    TEXT DEFAULT 'normal',
    state       TEXT DEFAULT 'queued',  -- queued, pushed, seen, acked, expired
    created_at  TEXT NOT NULL,
    pushed_at   TEXT,
    seen_at     TEXT,
    acked_at    TEXT,
    ttl         INTEGER  -- seconds, NULL = no expiry
);

CREATE TABLE presence (
    name        TEXT PRIMARY KEY,
    state       TEXT DEFAULT 'offline',  -- online, busy, idle, offline
    last_seen   TEXT,
    current_task INTEGER  -- task ID if claimed
);
```

**Wire protocol additions:**
```json
{"cmd": "send", "to": "deployer", "message": "step 1 done", "priority": "normal"}
{"cmd": "inbox"}
{"cmd": "ack", "message_id": 5}
{"cmd": "presence"}
```

**Delivery when receiver is busy (mid-turn):**
1. Message lands in durable inbox immediately (`queued`)
2. If receiver is connected: push lightweight notification (`pushed`)
3. Receiver's integration decides when to surface it:
   - `critical` → inject immediately (system notification)
   - `normal` → inject at turn boundary
   - `bulk` → digest at session idle
4. Agent sends `seen` when surfaced, `acked` when acted on

### 2. Claude Code Integration (superpowers-style)

Makes waggle invisible to Claude Code agents. Based on the superpowers pattern (github.com/obra/superpowers).

**Components:**

**a) SessionStart hook (`~/.claude/hooks/waggle-connect.sh`):**
```bash
# On every Claude Code session start:
# 1. Detect if in a git repo with waggle available
# 2. Auto-connect to broker with session name
# 3. Check inbox for pending messages
# 4. Check for assigned tasks
# 5. Inject context into session
```

**b) Waggle skill (`/waggle`):**
```
/waggle send <name> <message>    — send message to another agent
/waggle status                   — who's online, task queue state
/waggle claim                    — claim next eligible task
/waggle done <result>            — complete current task
```

**c) Incoming message injection:**
When a message arrives via the persistent connection, inject into the conversation as a system-level notification:
```
[waggle] Message from orchestrator: "step 1 is done, start implementing auth module"
[waggle] Task #3 unblocked — ready to claim
[waggle] deployer completed task #1 with result: {"commit": "abc123"}
```

**d) Automatic heartbeat:**
The hook/skill maintains the waggle connection and sends heartbeats for any claimed task. The agent never needs to know about leases.

**e) Auto-claim on assignment:**
If the orchestrator creates a task with `--assign deployer`, the deployer's session auto-claims it when the message arrives.

**Cross-platform expansion (post Claude Code):**
- Gemini CLI: equivalent `GEMINI.md` instructions + startup script
- Codex: `AGENTS.md` instructions + startup script
- Generic: `waggle agent start --name worker` wrapper that maintains connection + polls inbox

### 3. Spawn — Visible Terminal Sessions

The magical experience. One command → agent appears working in a new terminal tab.

```bash
waggle spawn --name deployer                          # default agent (claude)
waggle spawn --name tester --type codex               # specific platform
waggle spawn --name reviewer --task '{"desc":"..."}'  # create task + spawn
waggle status                                         # shows all spawned agents
waggle stop                                           # kills all agents + broker
```

**What spawn does:**
1. Opens a new terminal tab (Terminal.app, iTerm2, or Linux equivalent)
2. In that tab: sets `WAGGLE_PROJECT_ID`, starts the agent
3. The agent's SessionStart hook fires → auto-connects to waggle
4. Agent is visible, interactive — you can watch and interact
5. Broker tracks spawned PID for status and cleanup

**Spawn depends on integration (#2).** Without the hook, spawned agents don't know about waggle. With the hook, they connect automatically.

## What Doesn't Change

- Broker architecture (Unix socket, SQLite, goroutine per connection)
- Task lifecycle (pending → claimed → completed/failed/canceled)
- Lock semantics (advisory, connection-tied)
- Event pub/sub (fire-and-forget fan-out)
- CLI command format (JSON in, JSON out)
- Wire protocol (NDJSON over Unix socket)

## Implementation Order

```
Phase 1: Direct messaging + inbox + presence
  - Schema: messages + presence tables (migration v2→v3)
  - Protocol: send, inbox, ack, presence commands
  - Router: new handlers
  - CLI: waggle send, waggle inbox, waggle presence
  - Tests: delivery lifecycle, mid-turn queuing, TTL expiry

Phase 2: Claude Code integration
  - SessionStart hook: auto-connect, check inbox/tasks
  - Skill: /waggle command set
  - Message injection: incoming messages as system notifications
  - Auto-heartbeat: background keepalive for claimed tasks
  - Auto-claim: --assign flag on task create

Phase 3: Spawn
  - Terminal detection (Terminal.app, iTerm2, Linux)
  - Tab launch with env injection
  - PID tracking in broker
  - Status + stop integration
  - Agent config (~/.waggle/agents.json)
```

## Related Issues

- #38 (spawn — P0, updated with as-is/to-be)
- #40 (GitHub Releases — distribution)
- #41 (task TTL — stale queue cleanup)

## Validated By

- GPT-5.3-codex: "Your 3 builds are mostly right. Add reliable delivery semantics + recoverable context."
- Grok-4.2-multiagent: "These are the right three things in the right order. Don't interrupt mid-turn. Queue and inject on next turn."
- Both models: Unix socket is architecturally sound for local dev. Keep transport abstract for future TCP.
