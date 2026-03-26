# Task 41 Brief: Task Lifecycle Policies — TTL, Stale Detection, Queue Health

**Issue:** #41
**Branch:** `feat/41-task-lifecycle`
**Goal:** Pending tasks self-manage: TTL auto-cancels abandoned work, stale detection publishes alerts, status shows queue health. The broker becomes self-cleaning.

**Dependencies:** None. Builds on main.

**Read these files first:**
- `internal/tasks/store.go` — Task struct (line 28), CreateParams (line 51), Create() (line 172), Cancel() (line 437), schema (line 123), scanTask() (line 251)
- `internal/tasks/lease.go` — StartLeaseChecker pattern (exact template for TTL checker)
- `internal/messages/store.go` — Message TTL migration pattern (line 72-98 `migrate()`), MarkExpired() (line 207)
- `internal/messages/ttl.go` — StartTTLChecker (exact template to copy)
- `internal/broker/broker.go` — Config struct (line 22), Serve() goroutine starts (line 148-160)
- `internal/broker/router.go` — handleTaskCreate (line 150), handleStatus (line 440), publishTaskEvent (line 474)
- `internal/broker/broker_test.go` — startTestBroker returns `(string, *Broker, func())`, startTestBrokerWithTTL pattern (line 41)
- `internal/config/config.go` — Defaults struct (line 56), ValidateDefaults() (line 19)
- `cmd/task_create.go` — CLI flag pattern

---

## Scope

**In:**
- `--ttl` flag on `waggle task create` — pending tasks auto-cancel after duration
- Periodic task TTL checker goroutine (same pattern as lease checker and message TTL checker)
- `task.stale` event published when tasks exceed configurable threshold
- `waggle status` includes queue health metrics
- Schema migration: add `ttl` column to tasks table

**Out:**
- Auto-retry of canceled tasks (separate concern)
- TTL on claimed tasks (lease checker handles those)
- Priority-based scheduling changes
- Any messaging or spawn changes

---

## What to Build

### 1. Schema Migration (`internal/tasks/store.go`)

Add `ttl` column to tasks table. Use the **exact same migration pattern** as `internal/messages/store.go:72-98`:

```go
// Add to initSchema(), after CREATE TABLE and after schema_version insert:
func (s *Store) migrateTaskSchema() error {
    var colCount int
    if err := s.db.QueryRow(
        `SELECT COUNT(*) FROM pragma_table_info('tasks') WHERE name = 'ttl'`,
    ).Scan(&colCount); err != nil {
        return fmt.Errorf("checking ttl column: %w", err)
    }
    if colCount == 0 {
        if _, err := s.db.Exec("ALTER TABLE tasks ADD COLUMN ttl INTEGER"); err != nil {
            return fmt.Errorf("adding ttl column: %w", err)
        }
    }
    return nil
}
```

Call `migrateTaskSchema()` from `NewStore()` after `initSchema()`. Update schema_version to 2.

### 2. Task Struct + CreateParams (`internal/tasks/store.go`)

Add TTL field to both structs:

```go
// In Task struct (after RetryCount):
TTL int `json:"ttl,omitempty"` // seconds, 0 = no expiry

// In CreateParams struct (after MaxRetries):
TTL int // seconds, 0 = no expiry
```

Update `Create()` — add TTL to the INSERT:
```go
// Validate TTL
if params.TTL < 0 {
    return nil, fmt.Errorf("ttl must be non-negative")
}
if params.TTL > config.Defaults.MaxTaskTTL {
    return nil, fmt.Errorf("ttl exceeds maximum (%d seconds)", config.Defaults.MaxTaskTTL)
}

// In the INSERT statement, add ttl to columns and values:
result, err := s.db.Exec(`
    INSERT INTO tasks (idempotency_key, type, tags, payload, priority, blocked, depends_on, lease_duration, max_retries, ttl)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, idempotencyKey, params.Type, tagsJSON, params.Payload, params.Priority, blocked, dependsOnJSON, leaseDuration, maxRetries, nullableInt(params.TTL))
```

Where `nullableInt` converts 0 to `nil` (SQL NULL):
```go
func nullableInt(v int) interface{} {
    if v == 0 {
        return nil
    }
    return v
}
```

Update `scanTask()` and list scanner — add `ttl` to SELECT and scan:
```go
// In scanTask SELECT:
// ... existing columns ..., ttl
var ttl sql.NullInt64
// In Scan:
err := row.Scan(
    // ... existing fields ...,
    &ttl,
)
// After Scan:
if ttl.Valid {
    t.TTL = int(ttl.Int64)
}
```

**Do the same for the List() scanner** — it has its own Scan call with all columns.

### 3. Config Defaults (`internal/config/config.go`)

Add to the Defaults struct definition and initialization:

```go
// In struct definition (after SpawnKillPollInterval):
TaskTTLCheckPeriod  time.Duration
TaskStaleThreshold  time.Duration
MaxTaskTTL          int

// In initialization (after SpawnKillPollInterval value):
TaskTTLCheckPeriod:  30 * time.Second,
TaskStaleThreshold:  5 * time.Minute,
MaxTaskTTL:          86400, // 24 hours
```

### 4. Broker Config (`internal/broker/broker.go`)

Add to the Config struct:
```go
type Config struct {
    // ... existing fields ...
    TaskTTLCheckPeriod time.Duration
}
```

Add to the duration defaults loop in `New()`:
```go
{"TaskTTLCheckPeriod", &cfg.TaskTTLCheckPeriod, config.Defaults.TaskTTLCheckPeriod},
```

### 5. CancelExpiredTTL (`internal/tasks/store.go`)

```go
// CancelExpiredTTL cancels pending tasks whose TTL has expired.
// Only cancels 'pending' tasks — claimed tasks are managed by the lease checker.
func (s *Store) CancelExpiredTTL() (int, error) {
    now := time.Now().UTC().Format(time.RFC3339)
    result, err := s.db.Exec(`
        UPDATE tasks
        SET state = 'canceled',
            failure_reason = 'ttl_expired',
            updated_at = ?
        WHERE state = 'pending'
          AND ttl IS NOT NULL
          AND CAST(strftime('%s','now') AS INTEGER) >= CAST(strftime('%s', created_at) AS INTEGER) + ttl
    `, now)
    if err != nil {
        return 0, fmt.Errorf("canceling expired tasks: %w", err)
    }

    count, err := result.RowsAffected()
    if err != nil {
        return 0, fmt.Errorf("getting rows affected: %w", err)
    }

    return int(count), nil
}
```

### 6. QueueHealth (`internal/tasks/store.go`)

```go
type QueueHealth struct {
    OldestPendingAge int `json:"oldest_pending_age_seconds"`
    StaleCount       int `json:"stale_count"`
    PendingCount     int `json:"pending_count"`
}

// QueueHealth returns health metrics for the task queue.
func (s *Store) QueueHealth(staleThreshold time.Duration) (*QueueHealth, error) {
    var health QueueHealth

    // Oldest pending age + count
    var oldestAge sql.NullInt64
    err := s.db.QueryRow(`
        SELECT
            CAST(strftime('%s','now') AS INTEGER) - MIN(CAST(strftime('%s', created_at) AS INTEGER)),
            COUNT(*)
        FROM tasks WHERE state = 'pending'
    `).Scan(&oldestAge, &health.PendingCount)
    if err != nil {
        return nil, fmt.Errorf("querying queue health: %w", err)
    }
    if oldestAge.Valid {
        health.OldestPendingAge = int(oldestAge.Int64)
    }

    // Stale count (pending > threshold)
    thresholdSecs := int(staleThreshold.Seconds())
    err = s.db.QueryRow(`
        SELECT COUNT(*) FROM tasks
        WHERE state = 'pending'
          AND CAST(strftime('%s','now') AS INTEGER) - CAST(strftime('%s', created_at) AS INTEGER) > ?
    `, thresholdSecs).Scan(&health.StaleCount)
    if err != nil {
        return nil, fmt.Errorf("querying stale count: %w", err)
    }

    return &health, nil
}
```

### 7. Task TTL Checker + Stale Event (`internal/tasks/ttl.go`)

New file. Copy the exact pattern from `internal/messages/ttl.go` and `internal/tasks/lease.go`:

```go
package tasks

import (
    "encoding/json"
    "log"
    "time"

    "github.com/seungpyoson/waggle/internal/events"
)

// StartTaskTTLChecker runs a periodic checker that cancels expired-TTL tasks
// and publishes task.stale events when tasks exceed the stale threshold.
func StartTaskTTLChecker(store *Store, hub *events.Hub, period time.Duration, staleThreshold time.Duration, stopCh <-chan struct{}) {
    ticker := time.NewTicker(period)
    defer ticker.Stop()
    for {
        select {
        case <-stopCh:
            return
        case <-ticker.C:
            // 1. Cancel expired TTL tasks
            if count, err := store.CancelExpiredTTL(); err != nil {
                log.Printf("task ttl checker: %v", err)
            } else if count > 0 {
                log.Printf("task ttl checker: canceled %d expired tasks", count)
            }

            // 2. Check for stale tasks and publish event
            health, err := store.QueueHealth(staleThreshold)
            if err != nil {
                log.Printf("task ttl checker: queue health error: %v", err)
                continue
            }
            if health.StaleCount > 0 {
                data, _ := json.Marshal(map[string]any{
                    "stale_count":        health.StaleCount,
                    "oldest_age_seconds": health.OldestPendingAge,
                })
                evt, _ := json.Marshal(map[string]any{
                    "topic": "task.events",
                    "event": "task.stale",
                    "data":  json.RawMessage(data),
                    "ts":    time.Now().UTC().Format(time.RFC3339),
                })
                hub.Publish("task.events", evt)
            }
        }
    }
}
```

### 8. Broker Integration (`internal/broker/broker.go`)

Add TTL checker goroutine to `Serve()`, after the lease checker and message TTL checker:

```go
// Start task TTL checker
b.wg.Add(1)
go func() {
    defer b.wg.Done()
    tasks.StartTaskTTLChecker(b.store, b.hub, b.config.TaskTTLCheckPeriod, config.Defaults.TaskStaleThreshold, b.stopCh)
}()
```

### 9. Status Integration (`internal/broker/router.go`)

In `handleStatus`, add queue health. Find the existing status map construction:

```go
// After the existing status fields:
health, err := s.broker.store.QueueHealth(config.Defaults.TaskStaleThreshold)
if err == nil {
    status["queue_health"] = health
}
```

### 10. handleTaskCreate Integration (`internal/broker/router.go`)

In `handleTaskCreate`, pass `req.TTL` to CreateParams. The `TTL int` field already exists on `protocol.Request` (added by Task 48 for messages). Add after the existing params setup:

```go
params := tasks.CreateParams{
    // ... existing fields ...
    TTL: req.TTL,
}
```

### 11. CLI: --ttl flag (`cmd/task_create.go`)

Read the existing file to understand the flag pattern. Add:

```go
var taskCreateTTL string  // duration string, e.g., "5m"

// In init():
taskCreateCmd.Flags().StringVar(&taskCreateTTL, "ttl", "", "Task TTL (e.g., '5m', '1h', '30s') — auto-cancel if unclaimed")

// In RunE, before sending request:
var ttlSeconds int
if taskCreateTTL != "" {
    d, err := time.ParseDuration(taskCreateTTL)
    if err != nil {
        printErr("INVALID_REQUEST", fmt.Sprintf("invalid ttl duration: %v", err))
        return nil
    }
    ttlSeconds = int(d.Seconds())
}

// In the Request:
TTL: ttlSeconds,
```

---

## Invariants

| ID | Invariant | How to verify | Test name |
|----|-----------|---------------|-----------|
| L1 | Task with TTL auto-cancels after duration | Create with ttl=1, wait 2s, verify state=canceled, reason=ttl_expired | TestStore_CancelExpiredTTL |
| L2 | Task without TTL stays pending indefinitely | Create without ttl, wait, verify still pending | TestStore_CancelExpiredTTL_NoTTL |
| L3 | Claimed task NOT affected by TTL | Create with ttl=1, claim, wait, verify still claimed | TestStore_CancelExpiredTTL_ClaimedIgnored |
| L4 | TTL checker runs periodically | Broker starts checker, expired tasks get canceled via GetState | TestBroker_TaskTTLCheckerRuns |
| L5 | Status shows queue health | Create tasks, status includes oldest_pending_age + stale_count | TestBroker_StatusQueueHealth |
| L6 | task.stale event fires | Create task, wait > threshold, subscribe, verify event | TestBroker_TaskStaleEvent |
| L7 | Schema migration preserves existing tasks | Create task on v1 schema, run migration, verify task intact | TestStore_TaskSchemaMigration |
| L8 | --ttl flag works end-to-end | CLI create with --ttl, verify TTL stored via protocol | TestBroker_CreateTaskWithTTL |
| L9 | TTL validation: negative rejected | Create with ttl=-1, verify error | TestStore_CreateTaskTTLValidation |
| L10 | TTL validation: exceeds max rejected | Create with ttl > MaxTaskTTL, verify error | TestStore_CreateTaskTTLValidation |
| L11 | Concurrent TTL expiry + claim race: claim wins | TTL fires while claim in progress — claimed task stays claimed | TestStore_CancelExpiredTTL_ClaimRace |
| L12 | CancelExpiredTTL is idempotent | Run twice, second returns 0 | TestStore_CancelExpiredTTL_Idempotent |

---

## Tests (TDD — write first, see fail, then implement)

### Unit tests (`internal/tasks/store_test.go` — additions)

Each test uses `newTestStore(t)` which creates an in-memory SQLite DB with same pragma setup as production.

```
TestStore_CancelExpiredTTL              — create with ttl=1, sleep 2s, CancelExpiredTTL(), verify Get() returns state=canceled + failure_reason=ttl_expired
TestStore_CancelExpiredTTL_NoTTL        — create without ttl, sleep 2s, CancelExpiredTTL() returns 0, Get() returns state=pending
TestStore_CancelExpiredTTL_ClaimedIgnored — create with ttl=1, Claim(), sleep 2s, CancelExpiredTTL() returns 0, Get() returns state=claimed
TestStore_CancelExpiredTTL_Idempotent   — create with ttl=1, sleep 2s, CancelExpiredTTL() returns 1, call again returns 0
TestStore_CancelExpiredTTL_ClaimRace    — create with ttl=1, start goroutine that claims, sleep past TTL, CancelExpiredTTL() — if claimed, stays claimed
TestStore_CreateTaskTTLValidation       — Create with TTL=-1 → error; TTL=0 → ok (no TTL); TTL=MaxTaskTTL+1 → error
TestStore_QueueHealth                   — create 3 tasks at staggered times, verify oldest_pending_age > 0, pending_count=3
TestStore_QueueHealth_Empty             — no pending tasks → age=0, stale_count=0, pending_count=0
TestStore_TaskSchemaMigration           — create v1 table (no ttl column), insert task, call NewStore (runs migration), verify task intact with TTL=0
```

### Integration tests (`internal/broker/broker_test.go` — additions)

```
TestBroker_CreateTaskWithTTL            — send task.create with TTL=60 via protocol, verify response includes ttl=60
TestBroker_TaskTTLCheckerRuns           — use startTestBrokerWithTTL(t, 500ms) pattern, create task with ttl=1, sleep 2s, broker.store.Get() returns state=canceled (proves goroutine ran, not just SQL filter)
TestBroker_StatusQueueHealth            — create 2 tasks, check status, verify queue_health key present with pending_count=2
TestBroker_TaskStaleEvent               — subscribe to task.events, create task, use short stale threshold, verify task.stale event fires
```

**CRITICAL:** `TestBroker_TaskTTLCheckerRuns` must query state directly (not via List), same lesson learned from message TTL test. The test needs `startTestBrokerWithTTL` to set `TaskTTLCheckPeriod: 500ms`. Existing helper takes message TTL period — either extend it to accept task TTL period too, or create a new helper.

---

## Acceptance Criteria

- [ ] All unit tests pass: `go test ./internal/tasks/ -v -count=1`
- [ ] All integration tests pass: `go test ./internal/broker/ -v -count=1 -timeout=120s`
- [ ] Existing tests still pass: `go test ./... -race -count=1 -timeout=120s`
- [ ] `go vet ./...` — zero warnings
- [ ] CLI: `waggle task create '{"desc":"test"}' --ttl 5s` stores TTL
- [ ] CLI: `waggle status` shows `queue_health` with `oldest_pending_age_seconds`, `stale_count`, `pending_count`
- [ ] Schema migration: existing tasks survive `NewStore()` on upgraded DB

## Smoke Test

```bash
cd $(mktemp -d) && git init
WAGGLE_PROJECT_ID=smoke-41 waggle start --foreground &
sleep 2

# Create task with short TTL
waggle connect --name cli
waggle task create '{"desc":"expires soon"}' --type test --ttl 5
waggle task list
# Must show task with state=pending

# Wait for TTL + checker
sleep 10

waggle task list --state canceled
# Must show task with state=canceled, failure_reason=ttl_expired

# Queue health in status
waggle task create '{"desc":"stale task"}' --type test
sleep 1
waggle status
# Must show queue_health with pending_count >= 1

# Task without TTL stays
waggle task create '{"desc":"no ttl"}' --type test
sleep 10
waggle task list --state pending
# Must still show "no ttl" task

# v1 regression
waggle task create '{"desc":"v1 test"}' --type test
waggle task claim --type test
waggle task list --state claimed

# v2 regression
WAGGLE_AGENT_NAME=alice waggle send bob "regression"
WAGGLE_AGENT_NAME=bob waggle inbox

waggle stop
```

## Do NOT

- Modify message TTL (that's Task 48, already done)
- Add TTL to claimed tasks (lease checker handles those — `CancelExpiredTTL` only targets `state = 'pending'`)
- Change the wire protocol for existing commands
- Add new fields to `protocol.Request` — reuse existing `TTL int`
- Break existing tests
- Use `List()` in TTL checker tests — query state directly to prove the goroutine ran
