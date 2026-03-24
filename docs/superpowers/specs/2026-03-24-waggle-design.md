# Waggle — Agent Session Coordination Broker

**Date:** 2026-03-24
**Status:** Draft
**Language:** Go
**Distribution:** Open-source CLI tool (single binary)

## Problem

Independent AI coding agent sessions (Claude Code, Gemini CLI, Codex, scripts) working on the same project have no way to coordinate. They can't distribute tasks, avoid file conflicts, or share status. The built-in team/swarm features in Claude Code only work within a single session — separate terminal sessions are blind to each other.

## Solution

A per-project pub/sub broker + task queue that agents interact with through shell commands. Any process that can run bash can participate.

## Design Principles

- **Zero hardcodes.** No magic numbers, no string literals for paths, ports, timeouts, or limits scattered in code. Every configurable value lives in one place (config struct with defaults). If you see a raw number or path string outside of config, it's a bug.
- **Single source of truth.** Every piece of state has exactly one owner. Task state: SQLite. Event state: broker memory. Lock state: broker memory tied to connections. Path resolution: one config module. Nothing is stored, computed, or derived in two places.
- **No dual paths.** One way to do each thing. One function detects the project root. One function computes all derived paths. One protocol for client-broker communication. If there are two ways to accomplish the same thing, one of them is wrong — delete it.
- **Systematic over special-case.** Fix the class of problem, not the instance. If a pattern appears twice, it should be a shared abstraction. No one-off workarounds that leave the underlying issue intact.
- **Two-way doors.** Every design choice can be revised without rewriting. The architecture supports evolution from single-process (v1) to split events/tasks processes (north star) without changing the CLI interface. Build for today, but don't close doors on tomorrow.
- **Broker is dumb, agents are smart.** The broker routes messages and manages task state. It does not parse payloads, interpret task content, or make decisions about what work means. Agents own all semantics. This keeps the broker simple and agent-agnostic.
- **Roles are fluid, not fixed.** The daemon (the `waggle start` process) is infrastructure — it manages the socket and SQLite. Everything else that connects is a session. Sessions have no enforced role. Any session can create tasks (orchestrate), claim tasks (work), subscribe to events (monitor), or all three simultaneously. "Controller" and "worker" are usage patterns described in documentation, not permissions in the protocol. A session orchestrating work on one project can be a worker on another. A worker that discovers sub-problems can post tasks and become an orchestrator. The broker doesn't know or care.
- **Fail loud.** Errors surface immediately as structured JSON with actionable context. No silent failures, no swallowed errors, no "it probably worked." If the broker can't do what was asked, it says so and says why.
- **Public-grade from day one.** This is open-source software other people will install and depend on. Clean CLI help, meaningful error messages, documented config, sensible defaults. No "we'll clean it up later" — every commit should be something you'd ship.
- **Zero identity assumptions.** No usernames, GitHub handles, machine-specific paths, or personal references baked into code, config, or tests. Everything is derived from the environment at runtime. A fresh clone on any machine works without editing anything.
- **Stable CLI, evolvable internals.** The CLI commands and JSON output are the public API. Users and agent scripts depend on them. The broker internals, wire protocol, and storage layer are private — free to change, optimize, or split without breaking consumers. Guard the boundary.
- **Extensible without forking.** New task types, new event topics, new lock namespaces — all possible without modifying waggle's source. The broker is payload-agnostic by design. Users extend through conventions, not code changes.

## Architecture

### System Overview

Single Go binary with two modes:

```
waggle (single binary)
+-- broker mode    (waggle start)    -- long-running daemon, listens on Unix socket
+-- client mode    (waggle <cmd>)    -- short-lived, connects to socket, runs command, exits
```

Broker internals have two modules with a clean interface between them:

```
Broker
+-- events module   -- in-memory pub/sub, fan-out to subscribers, fire-and-forget
+-- tasks module    -- SQLite-backed durable queue, claim/ack/result lifecycle
```

**Per-project isolation.** Each project gets its own broker instance, its own SQLite DB, its own socket. Sessions in different projects never interact.

**Auto-lifecycle.** First `waggle` command in a project auto-starts the broker daemon if not running. Broker exits after configurable idle timeout (no connections and no pending tasks for N minutes, default 30). No manual daemon management.

**North star.** The clean boundary between events and tasks modules means the system can be split into two processes later (events broker + direct SQLite task access) without changing the CLI interface or the wire protocol. v1 is Approach A (single process); the architecture supports evolution to Approach C (hybrid) when needed.

### Path Resolution

All paths are computed from the project root by a single config module.

| Resource | Location | Reason |
|----------|----------|--------|
| SQLite DB | `<project-root>/.waggle/state.db` | In-project, no path length issue |
| Config | `<project-root>/.waggle/config.json` | Per-project settings |
| PID file | `<project-root>/.waggle/broker.pid` | Daemon lifecycle |
| Lockfile | `<project-root>/.waggle/start.lock` | Prevents auto-start races |
| Broker log | `<project-root>/.waggle/broker.log` | Structured logging |
| Socket | `~/.waggle/sockets/<hash>/broker.sock` | Hashed path avoids 104-byte UDS limit on macOS; created with 0700 (owner-only) |

`<hash>` is the first 12 characters of SHA-256 of the absolute project root path. Hash collision between different project roots on the same machine would cause cross-talk — extremely unlikely at 48 bits but worth noting.

`WAGGLE_ROOT` env var overrides project root auto-detection. All paths recompute from the override.

## CLI Interface

All output is JSON (one object per line). Exit codes: 0 success, 1 error, 2 broker not running.

### Daemon Management

```bash
waggle start                              # explicitly start broker (usually auto)
waggle stop                               # graceful shutdown
waggle status                             # broker health, connected sessions, queue stats, locks
```

### Session Identity

```bash
waggle connect --name "worker-1"          # register as a named session
waggle disconnect                         # deregister
```

### Events (fire-and-forget)

```bash
waggle publish <topic> <message>          # publish event to topic
waggle subscribe <topic>                  # block and stream events (stdout, one JSON per line)
waggle subscribe <topic> --last 5         # replay last N then stream
```

### Tasks (durable queue)

```bash
waggle task create <payload>              # post a task, returns task ID
  --type <type>                           # task type (code-edit, test, review, lint, etc.)
  --tags <t1,t2>                          # freeform labels for filtering
  --depends-on <id1,id2>                  # task IDs that must complete first
  --lease <duration>                      # override default lease (default 5m)
  --max-retries <n>                       # override default crash retries (default 3)
  --priority <n>                          # higher = claimed first (default 0)
  --idempotency-key <key>                 # deduplicates create calls

waggle task list                          # show all tasks with status
  --state <state>                         # filter by state
  --type <type>                           # filter by type
  --owner <name>                          # filter by current owner

waggle task claim                         # atomically claim next eligible task
  --type <type>                           # only claim tasks of this type
  --tags <t1,t2>                          # only claim tasks with these tags

waggle task complete <id> <result>        # mark done with result payload (requires claim token)
waggle task fail <id> <reason>            # mark failed (requires claim token)
waggle task heartbeat <id>                # renew lease (requires claim token)
waggle task cancel <id>                   # cancel pending or request cancel on claimed
waggle task get <id>                      # fetch task details + result
waggle task update <id> <message>         # append progress note
```

### Coordination

```bash
waggle lock <resource>                    # announce exclusive access
waggle unlock <resource>                  # release
waggle locks                              # list active locks
```

### Usage Personas

**Human (you):** `task create`, `task list`, `subscribe task.events`, `status` — queue work and watch.

**Worker agent:** `connect`, `task claim`, `lock`, `unlock`, `task complete`/`fail`/`heartbeat`, `disconnect` — pick up work and do it.

**Controller agent:** `connect`, `subscribe task.events`, `task create`, `task cancel`, `task list`, `disconnect` — decompose problems, post tasks, react to completions/failures, reassign.

## Wire Protocol

NDJSON (newline-delimited JSON) over Unix domain socket. Stateful connections.

### Connection Lifecycle

```
1. Client connects to socket
2. Client sends: {"cmd": "connect", "name": "worker-1"}
3. Broker responds: {"ok": true, "session_id": "..."}
4. Client sends commands, gets responses
5. Client sends: {"cmd": "disconnect"} (or socket closes)
6. Broker cleans up: re-queues claimed tasks, releases locks, removes from subscribers
```

### Request/Response Format

**Request:**
```json
{"cmd": "task.create", "payload": {"desc": "fix auth bug"}, "type": "code-edit", "tags": ["auth"]}
{"cmd": "task.claim", "type": "test"}
{"cmd": "subscribe", "topic": "task.events"}
{"cmd": "lock", "resource": "src/auth.py"}
```

**Response (short-lived):**
```json
{"ok": true, "data": {"id": 7, "status": "pending"}}
{"ok": false, "error": "resource already locked by worker-2"}
```

**Response (subscribe stream):**
```json
{"topic": "task.events", "event": "task.claimed", "id": 7, "by": "worker-1", "ts": "..."}
{"topic": "task.events", "event": "task.completed", "id": 7, "result": {...}, "ts": "..."}
```

### Connection Types

- **Short-lived:** Client connects, sends handshake, sends one command, gets one response, disconnects. Used by all commands except `subscribe`.
- **Long-lived:** Client connects, sends handshake, sends subscribe, receives streamed events until disconnect. Used only by `subscribe`.

The broker does not assume all connections are short-lived. Both types use the same handshake and cleanup path.

## Task Lifecycle

### States

```
pending (blocked) ---> pending (eligible) ---> claimed ---> completed
                                                  |
                                                  +-------> failed
                                                  |
                                                  +-------> canceled

pending (any) ---------> canceled
```

- **pending (blocked):** Has unresolved `depends_on`. Not claimable.
- **pending (eligible):** All dependencies completed (or none). Claimable.
- **claimed:** A worker owns it. Claim token issued.
- **completed:** Worker finished. Result payload attached.
- **failed:** Explicit failure by worker, or max crash retries exceeded, or dependency failed.
- **canceled:** Controller canceled it.

### Transitions

| Trigger | From | To | Notes |
|---------|------|----|-------|
| `task create` | — | pending (blocked or eligible) | Blocked if `depends_on` has incomplete tasks. Rejects if deps don't exist or would create a cycle. |
| Dependency completed | pending (blocked) | pending (eligible) | Broker checks on each completion |
| Dependency failed/canceled | pending (blocked) | failed | Reason: `dependency_failed` |
| `task claim` | pending (eligible) | claimed | Atomic. Claim token + lease issued. |
| `task complete` | claimed | completed | Requires valid claim token. |
| `task fail` | claimed | failed | Requires valid claim token. Stays failed. |
| Worker disconnect | claimed | pending (eligible) | Auto re-queue. Increments retry count. |
| Lease expired | claimed | pending (eligible) | Auto re-queue. Increments retry count. |
| Retry count > max | claimed | failed | Reason: `max_retries_exceeded` |
| `task cancel` (pending) | pending | canceled | Immediate. |
| `task cancel` (claimed) | claimed | canceled | Best-effort notification via `task.events` topic; hard-cancel on lease expiry if worker doesn't ack. |

### Claim Token

On `task claim`, broker returns a random token. Worker must pass this token on `complete`, `fail`, and `heartbeat`. If a task is re-queued and claimed by another worker, the old token is invalidated. This prevents stale workers from completing tasks they no longer own.

### Lease and Heartbeat

- Default lease: 5 minutes. Configurable per-task on create.
- `task heartbeat <id>` resets the lease timer. Requires valid claim token.
- Expired lease: task returns to pending (eligible), retry count incremented.
- Broker checks leases periodically (e.g., every 30 seconds).

### Retry Policy

- **Worker crash (disconnect) or lease expiry:** Auto re-queue up to `max_retries` (default 3). After that, task moves to `failed` with reason `max_retries_exceeded`.
- **Explicit `task fail`:** Task stays failed. Controller reviews and decides.
- Retry count persists across re-queues. Reset only if controller manually re-creates the task.

### Priority and Ordering

`task claim` selects from eligible tasks: highest `priority` first, then oldest `created_at` within same priority. Default priority is 0.

### Idempotency

`task create` with an `idempotency_key` that already exists returns the existing task instead of creating a duplicate. Key is unique per project.

### Auto-Published Events

Every task state change automatically publishes to the `task.events` topic:

```json
{"topic": "task.events", "event": "task.created", "id": 1, "type": "code-edit", "ts": "..."}
{"topic": "task.events", "event": "task.claimed", "id": 1, "by": "worker-1", "ts": "..."}
{"topic": "task.events", "event": "task.completed", "id": 1, "by": "worker-1", "result": {...}, "ts": "..."}
{"topic": "task.events", "event": "task.failed", "id": 1, "reason": "...", "ts": "..."}
{"topic": "task.events", "event": "task.unblocked", "id": 3, "unblocked_by": 1, "ts": "..."}
{"topic": "task.events", "event": "task.canceled", "id": 2, "ts": "..."}
```

## Coordination (Locks)

- **Advisory.** Locks signal intent. They don't physically prevent file access.
- **Connection-tied.** Worker disconnect auto-releases all locks held by that session.
- **In-memory only.** Broker restart clears all locks (correct since all connections also drop).
- **Namespaced by convention.** Use `file:<path>`, `module:<name>`, etc. Broker treats lock resource as an opaque string.
- **No deadlock detection.** Not needed at this scale — lease timeouts on tasks break any circular waits.
- **No queuing.** Lock attempt on a held resource returns an error with the holder's name. Worker decides: retry, pick a different task, or ignore.

## SQLite Schema (Reference)

```sql
CREATE TABLE tasks (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    idempotency_key TEXT UNIQUE,
    type            TEXT,
    tags            TEXT,          -- JSON array
    payload         TEXT NOT NULL, -- JSON string
    priority        INTEGER DEFAULT 0,
    state           TEXT NOT NULL DEFAULT 'pending',
    blocked         BOOLEAN DEFAULT FALSE,
    depends_on      TEXT,          -- JSON array of task IDs
    claim_token     TEXT,
    claimed_by      TEXT,
    claimed_at      TEXT,
    lease_expires_at TEXT,
    lease_duration  INTEGER DEFAULT 300, -- seconds
    max_retries     INTEGER DEFAULT 3,
    retry_count     INTEGER DEFAULT 0,
    result          TEXT,          -- JSON string, set on complete
    failure_reason  TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_tasks_claimable ON tasks (state, blocked, priority DESC, created_at ASC);
CREATE UNIQUE INDEX idx_tasks_idempotency ON tasks (idempotency_key) WHERE idempotency_key IS NOT NULL;
```

SQLite is opened in WAL mode for concurrent read performance. All claim operations use `BEGIN IMMEDIATE` transactions for atomicity.

## Integration Model

Waggle is three layers. Only Layer 1 is in scope for v1. Layers 2 and 3 are enabled by — but not part of — this implementation.

### Layer 1: The binary (v1 — what we're building)

`waggle` is a Go CLI binary that speaks NDJSON over Unix domain sockets. Any process that can run a shell command can participate. This is the universal baseline — it works with every agent platform today without any platform-specific code.

### Layer 2: Agent-specific wrappers (post-v1)

Optional integrations that make waggle feel native in each platform's ecosystem:

| Platform | Integration | Purpose |
|----------|-------------|---------|
| Claude Code | Skill (`/waggle`) | Teaches Claude the workflow patterns, wraps common sequences |
| Claude Code | MCP server | Native tool calls without bash overhead |
| Gemini CLI | Extension / MCP | Same as above |
| Codex CLI | Shell only | Already works — no wrapper needed |
| Custom agents | Client library (Go/Python/JS) | Speaks the wire protocol directly, no shell overhead |

These are thin wrappers over Layer 1. They don't add new capabilities — they make existing capabilities more ergonomic for each platform.

### Layer 3: The protocol spec (long-term)

The wire protocol (NDJSON over Unix socket, stateful connections, command/response format) can be extracted as a standalone specification — independent of the Go implementation. This enables:

- Alternative broker implementations (Rust, cloud-hosted, etc.)
- Native protocol support in agent frameworks (no shelling out)
- A **standard for multi-agent coordination** across platforms

The model is LSP (Language Server Protocol): VS Code shipped an implementation first, then extracted the protocol spec. Other editors adopted it. The Go binary becomes the reference implementation, not the only one.

### Design implications for v1

- **Wire protocol must be clean and fully documented.** It's the future public contract.
- **No Go-specific assumptions in the protocol.** Message format, state transitions, and error codes must make sense to any implementer reading the spec.
- **CLI is a reference client.** It demonstrates the protocol but doesn't define it. A Python script speaking raw NDJSON to the socket is equally valid.

## Deferred (v2+)

- Topic wildcards/prefix matching (exact topic match in v1)
- Hierarchical lock enforcement
- Direct messaging between sessions (use topic `direct.<name>` as workaround)
- Event replay/snapshots for late-joining subscribers
- Protocol version negotiation (version field in handshake, no negotiation logic)
- Rich `watch` command (real-time dashboard)
- Split into Approach C (separate event broker + direct SQLite task access)
- Priority aging / starvation prevention
- Result payload separation for large artifacts
- Per-priority concurrency caps
- `waggle spawn` — process manager that launches agent sessions (Claude, Codex, Gemini), auto-connects them, and tracks PIDs. Makes waggle the session registry, not just the message broker. `waggle status` shows all running agents. `waggle stop` kills them all. Two-way door: broker already tracks sessions by name, spawn adds a process layer on top.
