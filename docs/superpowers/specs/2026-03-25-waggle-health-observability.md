# Waggle Health Observability — MVP Spec

**Date:** 2026-03-25
**Status:** Design approved
**Depends on:** Phase 1 (broker) is implementable on v1 now. Phases 2-4 require v2 (direct messaging + spawn).
**Validated by:** gpt-5.3-codex (openai), gemini-3.1-pro-preview (google), grok-4.20-multi-agent-beta (openrouter)

## Problem

A waggle orchestrator managing a mixed fleet of AI coding agents (Claude Code, Codex CLI, Gemini CLI, Augment Code) has no visibility into agent capacity. It cannot see context window usage, API rate limits, or task state. When agents hit limits, the orchestrator doesn't know — it keeps assigning work to exhausted agents, degrading output quality.

## What We're Building

Three things:

1. `health.report` command + `health` table in broker
2. Orchestrator queries health before assigning tasks
3. Per-platform wrappers that publish what they can

## What We're NOT Building (deferred)

- Checkpoint serialization (handoff = git state + task queue)
- Persistent learning across sessions
- Stuck-in-loop detection
- Staleness state machine
- API proxy for black-box token counting

## 1. Wire Protocol Additions

Uses the existing `protocol.Request` struct. Health data goes in `Payload` (type `json.RawMessage`), consistent with how `task.create` works. Agent identity (`agent_name`) comes from the connection's `Name` field, set during `connect`.

### Prerequisites

The `connect` command must be extended to accept `platform` (string, e.g. `"claude-code"`, `"codex"`, `"gemini"`, `"augment"`). This is a v2 prerequisite. `session_id` is generated broker-side on connection (UUID), not provided by the agent.

### health.report (agent → broker)

```json
{"cmd": "health.report", "payload": {
  "context_pct": 72,
  "usage_pct": 45,
  "usage_resets_at": "2026-03-27T22:00:00Z",
  "tasks_in_flight": 2,
  "tasks_completed": 5,
  "current_task_id": 14,
  "session_age_seconds": 3600,
  "confidence": "high",
  "source": "statusline"
}}
```

All fields in `payload` are optional. A minimal heartbeat:

```json
{"cmd": "health.report", "payload": {
  "session_age_seconds": 1200,
  "confidence": "low",
  "source": "wrapper"
}}
```

The broker knows `agent_name` from the connection's `Name` field, `platform` from the extended `connect` handshake, and `session_id` from broker-generated UUID.

**Validation rules:**
- `context_pct`: 0-100 (REAL), or omitted
- `usage_pct`: 0-100 (REAL), or omitted
- `confidence`: one of `"high"`, `"medium"`, `"low"` (default: `"low"`)
- `source`: one of `"statusline"`, `"wrapper"`, `"self_report"` (default: `"wrapper"`)
- Out-of-range or invalid enum values → reject with error

### health.list (orchestrator → broker)

```json
{"cmd": "health.list"}
```

Response (uses existing `protocol.Response` — `OK bool` + `Data json.RawMessage`):

```json
{"ok": true, "data": [
  {
    "agent_name": "deployer",
    "platform": "claude-code",
    "session_id": "sess-x7y8z9",
    "context_pct": 72,
    "usage_pct": 45,
    "confidence": "high",
    "source": "statusline",
    "reported_at": "2026-03-25T19:30:00Z",
    "fresh": true
  },
  {
    "agent_name": "tester",
    "platform": "codex",
    "session_id": "sess-a1b2c3",
    "context_pct": null,
    "usage_pct": null,
    "confidence": "low",
    "source": "wrapper",
    "reported_at": "2026-03-25T19:29:45Z",
    "fresh": true
  }
]}
```

`fresh` is computed at query time (not stored): `reported_at` within TTL.

### health.get (orchestrator → broker)

```json
{"cmd": "health.get", "name": "deployer"}
```

Uses existing `protocol.Request.Name` field (same as `connect`).

Response: same shape as one element of `health.list`. If agent not found: `{"ok": false, "error": "agent not found", "code": 404}`.

## 2. SQLite Schema

```sql
CREATE TABLE health_reports (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_name    TEXT NOT NULL,
    session_id    TEXT NOT NULL,
    platform      TEXT NOT NULL,
    context_pct   REAL,
    usage_pct     REAL,
    usage_resets_at TEXT,
    tasks_in_flight INTEGER,
    tasks_completed INTEGER,
    current_task_id INTEGER,
    session_age_seconds INTEGER,
    confidence    TEXT DEFAULT 'low',
    source        TEXT DEFAULT 'wrapper',
    reported_at   TEXT NOT NULL,
    UNIQUE(agent_name)
);
```

Notes:
- `UNIQUE(agent_name)` — each agent has one row, upserted on every report. No history table for MVP. If two agents share a name, last writer wins (same as v1 `connect` behavior). **Limitation:** agent pools (multiple "deployer" instances) are not supported in MVP — each agent must have a unique name.
- `reported_at` is broker receive time (RFC3339 UTC), not agent timestamp. Avoids clock skew issues.
- `usage_resets_at` is RFC3339 UTC (consistent with all other timestamps — no mixed formats).
- `fresh` is computed at query time by the broker: `reported_at` within `config.Defaults.HealthTTL` (default 60s). The broker owns this computation — orchestrators receive it pre-computed.
- All defaults live in `internal/config/config.go` via `config.Defaults`, consistent with v1. Specifically: `HealthTTL`, `HealthReportInterval`, `HealthDebounceInterval`.

## 3. Per-Platform Wrappers

### Claude Code (rich data)

Data source: Claude Code pipes structured JSON via stdin to statusline plugins on every render tick. Contains `context_window.used_percentage`, `rate_limits.five_hour.used_percentage`, and token counts.

Mechanism:
1. SessionStart hook connects to waggle broker (already in v2 design)
2. Statusline plugin (or sidecar) writes stdin JSON to a project-scoped file (`~/.waggle/data/<project-hash>/health/<agent-name>.json`)
3. Background process reads that file every 5s and publishes `health.report` to waggle

Hardening:
- Atomic file writes (temp + rename)
- Debounce — publish every 5s or on significant change (>5% delta), not every render tick
- Sidecar lifecycle tied to session (dies when session ends)

### Codex CLI / Gemini CLI / Augment Code (heartbeat only)

These platforms expose no programmatic health data.

Mechanism:
1. `waggle spawn` launches a wrapper that starts the CLI as a child process
2. Wrapper connects to waggle broker
3. Wrapper sends `health.report` every 30s with `session_age_seconds` and `confidence: "low"`
4. Wrapper detects process exit and publishes final report

This gives the orchestrator: alive/dead + session age. No context % or usage data. The orchestrator can use session age as a crude proxy ("agent running >45min on a 200k context model is probably getting full").

### Instruction file fallback

For agents started outside `waggle spawn`, instruction files (CLAUDE.md, AGENTS.md, GEMINI.md) can include:

```
After completing each task, run: waggle health report --context-pct <estimate>
```

This violates zero-knowledge but is better than nothing for manually started agents.

## 4. Orchestrator Behavior

The orchestrator is a waggle agent — it consumes health data and makes scheduling decisions. This logic lives in the orchestrator, not in waggle core.

### Task assignment flow

```
1. Orchestrator has work to assign
2. Query: waggle health list
3. For each candidate agent:
   - If context_pct > 70% → skip (don't assign new work)
   - If context_pct > 85% → mark for relay after current task
   - If not fresh → treat as unknown capacity, be cautious
   - If confidence = "low" → use session_age as proxy
4. Assign to healthiest available agent
5. If no healthy agents → spawn a fresh one
```

### Relay flow

```
1. Agent A finishes task, context_pct = 87%
2. Orchestrator decides to relay remaining work
3. Orchestrator spawns Agent B via waggle spawn
4. Orchestrator reassigns remaining tasks to Agent B
   - Tasks marked with relayed_from: "agent-a-session-id"
5. Agent A gets no more assignments, eventually idles out
```

Handoff is git state + waggle task queue. No checkpoint serialization. The new agent reads the repo, sees committed work, picks up remaining tasks. If uncommitted work exists, it's visible in the working directory (same repo).

### Thresholds

Configurable, not hardcoded. Defaults:
- 70% context → stop assigning new tasks
- 85% context → relay after current task completes
- 60s health TTL → stale if no report within window
- 45min session age → crude proxy for "probably high context" (black-box agents only, used when `context_pct` is unavailable)

### Session-scoped adaptation (deferred — not in MVP)

Deferred to post-MVP. When implemented, the mechanism would be:
- Orchestrator maintains an in-memory map of `{task_type → capacity_outcome}` during a session
- When an agent hits >85% context on a task, record the task type and approximate size
- For subsequent tasks of the same type, split into subtasks before assigning (e.g., "refactor module" → "extract interfaces" + "implement" + "update callers")
- Map resets when the orchestrator session ends — no cross-session persistence

## 5. Implementation Order

```
Phase 1: Broker (health.report, health.list, health.get, SQLite table)
  - New router handlers
  - Schema migration
  - Wire protocol additions
  - Tests: report storage, upsert, freshness, query

Phase 2: Claude Code wrapper
  - Statusline sidecar that writes health to file
  - Background publisher that reads file and sends health.report
  - Integration test with live Claude Code session

Phase 3: Spawn wrapper (black-box platforms)
  - Heartbeat publisher in waggle spawn
  - Process exit detection
  - Tests: heartbeat lifecycle, process crash

Phase 4: Orchestrator logic (consumer, not part of waggle core)
  - Health-aware task assignment
  - Relay flow
  - Session-scoped adaptation
```

Phases 1-3 are waggle changes. Phase 4 is orchestrator implementation — separate concern.

## Related Issues

- v2 spec: `docs/superpowers/specs/2026-03-25-waggle-v2-communication-layer.md`
- #38 (spawn — required for relay + black-box wrappers)
- Direct messaging (required for orchestrator → agent commands)

## Decisions (resolved from open questions)

- **Health events:** MVP uses polling via `health.list` only. Event-driven notification (`health.updated` topic) is deferred.
- **Debounce interval:** 5s for Claude Code statusline sidecar. Will validate during Phase 2 integration testing.
- **Session age proxy:** The 45min default threshold is included in MVP for black-box agents. What's deferred is per-platform/per-model calibration (e.g., "Codex on GPT-4o exhausts context faster than Gemini on 2M window"). MVP uses a single default.
