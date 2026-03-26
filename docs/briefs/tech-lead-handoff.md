# Waggle Tech Lead Handoff

**Date:** 2026-03-26
**Repo:** github.com/seungpyoson/waggle
**Language:** Go 1.26
**Codebase:** ~12,000 lines Go, ~6,700 lines tests (56% test ratio), 11 packages, all pass with `-race`

---

## What Waggle Is

Waggle is a local agent coordination broker. Multiple AI coding agents (Claude Code, Codex, Gemini, Augment) connect to a single broker over Unix sockets and coordinate work: send messages, claim tasks, check who's online, spawn new agents in terminal tabs. Think of it as a local Slack + task queue for AI agents, backed by SQLite.

**The user is a non-engineer building autonomous multi-agent systems.** They want agents to coordinate without a human relaying messages between tabs. The user cares about:
- Evidence-based verification (assertions must have proof, not claims)
- Class-of-problem fixes, not instance patches
- TDD mandatory, no exceptions
- Zero tolerance for incomplete work

---

## Architecture

```
~/.waggle/
├── data/<hash>/          # Per-project state
│   ├── state.db          # SQLite (tasks + messages, shared *sql.DB)
│   ├── waggle.pid        # Broker PID
│   └── waggle.log        # Broker logs
└── sockets/<hash>/
    └── broker.sock       # Unix domain socket

internal/
├── config/      # Path resolution, ALL defaults (single source of truth)
├── protocol/    # Wire format (Request, Response, Event — NDJSON over socket)
├── events/      # In-memory pub/sub hub
├── tasks/       # SQLite task store, dependencies, lease management
├── messages/    # SQLite message store, TTL, ack lifecycle
├── locks/       # Advisory lock manager
├── spawn/       # Terminal tab launcher, safe command builder, PID manager
├── broker/      # Socket listener, session management, command routing
└── client/      # Shared client for CLI commands

cmd/             # Cobra CLI commands (22 commands)
```

**Key design decisions:**
- **Single `*sql.DB`** — SQLite with `MaxOpenConns(1)`, WAL mode, `busy_timeout`. Both task store and message store share one connection. Opened in `broker.New()`, passed to both stores.
- **Broker is dumb, agents are smart** — Broker routes messages and manages state. It doesn't make scheduling decisions or reorder queues. Agents (or the orchestrator) decide what to work on.
- **Config is single source of truth** — `internal/config/config.go` Defaults struct owns every configurable value. No magic numbers anywhere else. `ValidateDefaults()` uses reflection to verify all numeric fields are positive.
- **Safe command builder** — `internal/spawn/command.go` provides `BuildShellCommand`, `BuildAppleScript`, `BuildPgrepPattern` with proper escaping. All user-controlled strings flow through this builder. No raw `fmt.Sprintf` interpolation into shell/AppleScript.

---

## What's Done (22 closed issues)

### v1 — Core Infrastructure (March 24)
All 13 MVP issues (#1-#13) closed in one day. Full task lifecycle: create, claim (with lease), heartbeat, complete, fail, cancel. Dependencies with cycle detection. Advisory locks. Event pub/sub. E2E tests.

### v1 Fixes (March 25)
6 issues (#27, #29, #31, #33, #35, #37): connection handshake enforcement, 1MB scanner buffer, 6 bug batch, clean disconnect, hardcoded constant elimination, universal project identity (git root commit hash).

### v2 — Communication Layer (March 25)
3 issues (#43, #48, #38):
- **#43 (PR #49):** `waggle send` + `waggle inbox`. SQLite message store, push delivery, write mutex on session encoder.
- **#48 (PR #51):** Ack lifecycle (queued→pushed→seen→acked), presence (in-memory, online/offline), priority (critical/normal/bulk), message TTL with periodic checker, `--await-ack` blocking sends with timeout + broker shutdown cleanup.
- **#38 (PR #53):** `waggle spawn` opens terminal tabs via AppleScript (macOS) with safe command builder. Register-before-spawn ordering (no orphan tabs). PID tracking with `pgrep` polling + PID=0 fallback.

---

## What's In Flight

### Delegated to Augment Code (briefs written, not yet started):

**#41 — Task Lifecycle Policies** (`docs/briefs/task-41-task-lifecycle.md`)
- `--ttl` on `waggle task create`, periodic TTL checker, `task.stale` events, `queue_health` in status
- Pure Go, same patterns as message TTL
- 12 invariants, 13 unit tests, 4 integration tests
- Brief has exact Go code, SQL, line-number references

**#44 — Claude Code Integration** (`docs/briefs/task-44-claude-code-integration.md`)
- `waggle install claude-code` — creates SessionStart hook + `/waggle` skills + auto-heartbeat
- Different deliverable: shell scripts + markdown + one Go command
- 15 invariants, 12 Go tests, 4 shell integration tests
- Depends on #41 being merged first

**Orchestrator prompt:** `docs/briefs/augment-v3-orchestrator-prompt.md` — Phase A-E execution discipline. Augment creates PRs but does NOT merge. Human reviews.

### Open Issues (not yet briefed):
- **#40** — GitHub Releases with pre-built binaries
- **#42** — Agent health observability (context/usage reporting)
- **#45** — Health wrapper for Claude Code statusline
- **#46** — Health wrapper for spawn (black-box heartbeats)
- **#47** — Health-aware orchestrator (capacity scheduling)
- **#50** — Architecture reference doc (Paperclip research)

---

## Review Process That Works

We iterated on this during PR #51 and #52. The pattern that caught real bugs:

1. **MECE invariant audit** — map every brief invariant to its test, read the actual test code, verify the test asserts what it claims
2. **Live smoke test** — build binary, start broker in tmpdir, run the brief's smoke test verbatim
3. **Grep verification** — `grep -n` for specific patterns (hardcodes, raw interpolation, lock ordering)
4. **False positive/negative audit** — after the review, check your own claims. "I said D1 is covered — did I actually read the test?"

**Bugs caught by this process:**
- Deadlock: presence event published under `broker.mu` write lock in `session.go:doCleanup` (Q2, PR #51)
- Command injection: user-controlled strings interpolated into AppleScript via `fmt.Sprintf` (F1/F2, PR #52)
- Test theater: `TestBroker_TTLCheckerRuns` passed via SQL belt-and-suspenders, never exercised the goroutine (Q5, PR #51)
- Test lies: `TestBroker_FullAckLifecycle` walked lifecycle but never asserted state at each step (R2-2, PR #51)
- Register-after-spawn: duplicate name opened orphan terminal tab before broker rejected it (R3, PR #52)

**Pattern: the implementer's first pass is ~80% correct.** The remaining 20% is consistently: weak tests that don't verify their invariant, unsanitized strings in command construction, operations ordered wrong (side effects before validation). Review for these specifically.

---

## Gotchas for New Contributors

### SQLite
- `MaxOpenConns(1)` — all access is serialized. No need for complex locking on the DB itself, but `sync.Mutex` still needed on Go-side state (session map, ack waiters, spawn manager).
- `CAST(strftime('%s','now') AS INTEGER)` — the pattern for time comparison in SQLite. Used by both message and task TTL expiry.
- Schema migration: `ALTER TABLE ADD COLUMN` with `pragma_table_info` existence check. Never `DROP TABLE`. See `messages/store.go:72-98` for the template.

### Concurrency
- Session encoder writes come from multiple goroutines (readLoop response + push delivery from sender's goroutine). `Session.writeMu` protects all `enc.Encode()` calls.
- `broker.mu` (RWMutex) protects the sessions map. Never call `hub.Publish()` while holding `broker.mu` — deadlock risk if any subscriber reads sessions.
- `ackWaiters` map has its own mutex (`ackWaitersMu`). The waiter channel is registered BEFORE push delivery to avoid a race where ack arrives before the sender enters `select`.

### Platform
- macOS AppleScript can't return child PIDs from `do script`. Workaround: `pgrep -f "WAGGLE_AGENT_NAME=<name>"` with 3s timeout, PID=0 fallback.
- Socket path limit: macOS has 104-byte limit on Unix socket paths. Solved by hashing project ID to a short path: `~/.waggle/sockets/<12-char-hash>/broker.sock`.

### Config
- `config.Defaults` is a struct literal (not a function). `ValidateDefaults()` uses reflection — adding a new `time.Duration` or `int` field to the struct automatically validates it.
- `ValidMsgPriorities []string` — priority values live in config, not hardcoded in store. Error messages reference config: `"must be one of %v"`.

### Test Patterns
- `startTestBroker(t)` returns `(sockPath string, broker *Broker, cleanup func())`. Tests that need broker internals (state queries, ackWaiters count) use the `*Broker` directly.
- `startTestBrokerWithTTL(t, 500*time.Millisecond)` — short TTL check period for tests. Without this, the 30s default means the goroutine never fires during the test.
- TTL checker tests MUST query state directly (e.g., `store.GetState(id)`) — NOT via `Inbox`/`List` which have SQL filters that independently exclude expired items. The SQL filter masks whether the goroutine actually ran.

---

## Process Rules (from user's CLAUDE.md)

These are non-negotiable:
1. **80% done is 0% done.** No loose ends.
2. **2 failures = stop.** Same approach fails twice → explain what's stuck, present options.
3. **Never suggest completion.** User decides when work is done.
4. **TDD mandatory.** Failing tests first, then implementation.
5. **Single source of truth.** Never duplicate state, config, or logic.
6. **Fix the class of problem, not the instance.** One broken call → fix the shared pattern.
7. **Right design over small diff.** If restructuring is correct, do it.
8. **Any rule violation = full revert.** Volume of work doesn't buy leniency. (Enforced: Bolt had 800+ closed issues and zero working pipeline from undisciplined sessions.)

---

## Delegation Pattern (Augment Code)

Waggle uses Augment Code as the primary implementer. The pattern:

1. **Human writes issue** on GitHub with problem statement, approach, risks, test plan
2. **Claude Code (tech lead) writes the brief** — `docs/briefs/task-N-*.md` with exact code, invariants, tests, smoke test, gotchas, "read this first" section, "do NOT" section
3. **Claude Code writes the orchestrator prompt** — `docs/briefs/augment-v3-orchestrator-prompt.md` with Phase A-E execution discipline
4. **Human sends blurb to Augment** pointing at the orchestrator prompt + briefs
5. **Augment implements** — TDD, creates PR, does NOT merge
6. **Claude Code reviews** — MECE invariant audit, live smoke test, grep verification, false positive check
7. **Human approves merge** after review passes

**Lesson learned:** The implementer rewrote the brief for PR #51 to match what they built instead of flagging scope concerns. The orchestrator prompt now says "Do NOT rewrite the brief" explicitly.

---

## File Index

| File | Purpose |
|------|---------|
| `docs/briefs/augment-v3-orchestrator-prompt.md` | Execution discipline for Augment agents |
| `docs/briefs/task-41-task-lifecycle.md` | Brief: task TTL, stale detection, queue health |
| `docs/briefs/task-44-claude-code-integration.md` | Brief: hook + skills + installer |
| `docs/briefs/task-48-review-fixes.md` | Historical: review fix spec from PR #51 |
| `docs/briefs/task-38-review-fixes.md` | Historical: review fix spec from PR #52 |
| `docs/superpowers/specs/2026-03-25-waggle-v2-communication-layer.md` | v2 design spec (6 invariants, 3 phases) |
| `docs/superpowers/specs/2026-03-24-waggle-design.md` | v1 design spec |
| `CLAUDE.md` | Agent integration guide (patterns, best practices, troubleshooting) |

---

## What to Do Next

1. **Wait for Augment to create PRs for #41 and #44** — briefs are on main, orchestrator prompt is ready
2. **Review #41 PR** using the MECE process above. Focus on: TTL checker test actually exercises goroutine, `CancelExpiredTTL` only targets pending (not claimed), schema migration idempotent
3. **Merge #41**, then review #44 PR. Focus on: settings.json merge doesn't overwrite existing hooks, hook exits <6s, all tests use tmpdir HOME
4. **After #44 merges:** the critical path for autonomous orchestration is complete. Agents auto-connect, check inbox, claim tasks, heartbeat, and receive commands — all without manual instruction.
5. **Then #42/#45/#46/#47** (health observability) — not yet briefed. These make the orchestrator aware of agent capacity (context window usage, workload) for smart scheduling.
