# Waggle — Follow-up Fixes Prompt

You are fixing 6 bugs found during live testing of the waggle MVP. Each fix must include:
1. A test that **fails before the fix** (proving the bug exists)
2. The fix itself
3. The test **passing after the fix**
4. Full regression: `go test ./... -race -count=1 -timeout=120s`

Create one branch for all fixes: `fix/30-post-mvp-bugs`. One commit per fix. PR at the end.

**Read before starting:**
- `docs/superpowers/specs/2026-03-24-waggle-design.md` (spec)
- `internal/broker/router.go` (router)
- `internal/broker/session.go` (session)
- `internal/tasks/store.go` (store)
- `cmd/task_claim.go` (CLI claim)

---

## Fix 1: Tags filter on claim is broken [HIGH]

**Bug:** `waggle task claim --tags frontend` ignores the tags filter and claims any eligible task.

**Root cause:** `internal/tasks/store.go` line ~344 — the `Claim()` method builds a query that filters by `Type` but completely ignores `filter.Tags`. The `Tags` field in `ClaimFilter` is never used in the SQL query.

**Reproduce (test must fail before fix):**
```go
// internal/tasks/store_test.go
func TestStore_ClaimWithTagsFilter(t *testing.T) {
    s := newTestStore(t)
    s.Create(CreateParams{Payload: `{"a":1}`, Type: "test"})
    s.Create(CreateParams{Payload: `{"b":2}`, Type: "test", Tags: []string{"frontend", "urgent"}})

    // Claim with tags filter should get the tagged task, not the first one
    claimed, err := s.Claim("w", ClaimFilter{Tags: []string{"frontend"}})
    if err != nil {
        t.Fatal(err)
    }
    if claimed.Payload != `{"b":2}` {
        t.Errorf("expected tagged task, got %s", claimed.Payload)
    }
}
```

**Fix:** In `store.go` `Claim()` method, after the type filter block (around line 348), add tags filter:

```go
if len(filter.Tags) > 0 {
    for _, tag := range filter.Tags {
        query += " AND EXISTS (SELECT 1 FROM json_each(tags) WHERE value = ?)"
        args = append(args, tag)
    }
}
```

**IMPORTANT:** Before implementing, verify `json_each` works with `modernc.org/sqlite`:
```go
// Add this test first
func TestStore_JsonEachWorks(t *testing.T) {
    s := newTestStore(t)
    s.Create(CreateParams{Payload: `{}`, Tags: []string{"a", "b", "c"}})
    var count int
    err := s.db.QueryRow(`SELECT count(*) FROM tasks t, json_each(t.tags) j WHERE j.value = 'b'`).Scan(&count)
    if err != nil {
        t.Fatalf("json_each not supported: %v", err)
    }
    if count != 1 {
        t.Fatalf("expected 1, got %d", count)
    }
}
```

If `json_each` is NOT supported, use this Go-side fallback instead:
```go
if len(filter.Tags) > 0 {
    // Fallback: load candidate IDs, filter in Go
    // This is less efficient but works without json_each
    query += " AND tags IS NOT NULL AND tags != '[]'"
}
// After getting taskID, verify tags match before claiming
```

**Verify after fix:**
```bash
go test ./internal/tasks/ -v -run "TestStore_ClaimWithTagsFilter|TestStore_JsonEach" -count=1
go test ./... -race -count=1 -timeout=120s
```

---

## Fix 2: Events subscribe wraps events in Response [MEDIUM]

**Bug:** `waggle events subscribe task.events` outputs:
```json
{"ok":false,"data":{"topic":"task.events","event":"task.created",...}}
```

Should output raw event NDJSON:
```json
{"topic":"task.events","event":"task.created",...}
```

**Root cause:** The subscribe handler in `router.go` uses `s.enc.Encode(evt)` which writes the Event struct. But the session's `readLoop` reads the response and the CLI prints it as a Response (with ok/data wrapping). The issue is that the event gets encoded as a Response by the session, or the CLI reads it through the response parser.

Check both:
1. `internal/broker/router.go` `handleSubscribe()` — how events are written to the connection
2. `cmd/events.go` — how the CLI reads and prints subscribe output

**The fix depends on where the wrapping happens:**

If the broker wraps it: change the broker to write raw event JSON, not Response-wrapped.
If the CLI wraps it: change the CLI to read raw lines and print them directly instead of parsing as Response.

**Test:**
```go
// internal/broker/broker_test.go
func TestBroker_SubscribeEventFormat(t *testing.T) {
    b, sockPath := startTestBroker(t)
    _ = b

    // Client 1: subscribe
    c1 := connectClient(t, sockPath, "subscriber")
    c1.Send(protocol.Request{Cmd: protocol.CmdSubscribe, Topic: "task.events"})

    // Client 2: create task (triggers event)
    c2 := connectClient(t, sockPath, "creator")
    c2.Send(protocol.Request{
        Cmd:     protocol.CmdTaskCreate,
        Payload: json.RawMessage(`{"desc":"test"}`),
    })

    // Read the event from client 1's connection
    // It should be a raw Event, not wrapped in Response
    line, err := c1.ReadStream()
    if err != nil {
        t.Fatal(err)
    }

    var raw map[string]any
    json.Unmarshal(line, &raw)

    // Must have "topic" at top level, not nested under "data"
    if _, ok := raw["topic"]; !ok {
        t.Errorf("event missing 'topic' at top level: %s", string(line))
    }
    // Must NOT have "ok" field
    if _, ok := raw["ok"]; ok {
        t.Errorf("event should not have 'ok' field: %s", string(line))
    }
}
```

**Verify after fix:**
```bash
go test ./internal/broker/ -v -run TestBroker_SubscribeEventFormat -count=1
# Also manual test:
# Terminal 1: waggle start --foreground
# Terminal 2: waggle events subscribe task.events
# Terminal 3: waggle task create '{"desc":"test"}' --type test
# Terminal 2 should show: {"topic":"task.events","event":"task.created",...}
```

---

## Fix 3: Clean/unclean disconnect [MEDIUM]

**Bug:** Session cleanup uses `strings.HasPrefix(s.name, "cli-")` to decide whether to requeue tasks. Should use a `cleanDisconnect` flag set when the client sends a `disconnect` command.

**Current behavior:** Any non-`cli-` session that disconnects (even cleanly) gets its tasks requeued.

**Root cause:** `internal/broker/session.go` lines 63-72 — uses name prefix instead of disconnect type.

**Fix:**

1. Add `cleanDisconnect bool` field to Session struct:
```go
type Session struct {
    name            string
    conn            net.Conn
    enc             *json.Encoder
    scan            *bufio.Scanner
    broker          *Broker
    cleanDisconnect bool
}
```

2. In `router.go` `handleDisconnect`, set the flag:
```go
func handleDisconnect(s *Session) protocol.Response {
    s.cleanDisconnect = true
    return protocol.OKResponse(nil)
}
```

3. In `session.go` `cleanup()`, use the flag:
```go
func (s *Session) cleanup() {
    if s.name != "" {
        s.broker.lockMgr.ReleaseAll(s.name)

        if !s.cleanDisconnect {
            // Unclean disconnect: worker crashed, requeue its tasks
            count, err := s.broker.store.RequeueByOwner(s.name)
            if err != nil {
                log.Printf("session: error requeuing tasks for %s: %v", s.name, err)
            } else if count > 0 {
                log.Printf("session: requeued %d tasks for crashed session %s", count, s.name)
            }
        }

        s.broker.hub.UnsubscribeAll(s.name)

        s.broker.mu.Lock()
        delete(s.broker.sessions, s.name)
        s.broker.mu.Unlock()
    }
    s.conn.Close()
}
```

**Tests (must have BOTH):**
```go
// internal/broker/broker_test.go
func TestBroker_CleanDisconnect_KeepsClaim(t *testing.T) {
    b, sockPath := startTestBroker(t)
    _ = b

    // Connect with custom name, create and claim task
    c := connectClient(t, sockPath, "agent-1")
    c.Send(protocol.Request{
        Cmd:     protocol.CmdTaskCreate,
        Payload: json.RawMessage(`{"desc":"keep me"}`),
    })
    claimResp, _ := c.Send(protocol.Request{Cmd: protocol.CmdTaskClaim})
    var claimed struct{ ID int64 `json:"ID"` }
    json.Unmarshal(claimResp.Data, &claimed)

    // Send disconnect (clean) then close
    c.Send(protocol.Request{Cmd: protocol.CmdDisconnect})
    c.Close()
    time.Sleep(200 * time.Millisecond)

    // Check: task should STILL be claimed
    c2 := connectClient(t, sockPath, "checker")
    getResp, _ := c2.Send(protocol.Request{Cmd: protocol.CmdTaskGet, TaskID: claimed.ID})
    var task struct{ State string `json:"State"` }
    json.Unmarshal(getResp.Data, &task)
    c2.Close()

    if task.State != "claimed" {
        t.Errorf("expected claimed, got %s (clean disconnect should keep claim)", task.State)
    }
}

func TestBroker_UncleanDisconnect_RequeuesClaim(t *testing.T) {
    b, sockPath := startTestBroker(t)
    _ = b

    // Connect with custom name, create and claim task
    c := connectClient(t, sockPath, "agent-crash")
    c.Send(protocol.Request{
        Cmd:     protocol.CmdTaskCreate,
        Payload: json.RawMessage(`{"desc":"requeue me"}`),
    })
    claimResp, _ := c.Send(protocol.Request{Cmd: protocol.CmdTaskClaim})
    var claimed struct{ ID int64 `json:"ID"` }
    json.Unmarshal(claimResp.Data, &claimed)

    // Close WITHOUT sending disconnect (unclean — simulates crash)
    c.Close()
    time.Sleep(200 * time.Millisecond)

    // Check: task should be back to pending
    c2 := connectClient(t, sockPath, "checker")
    getResp, _ := c2.Send(protocol.Request{Cmd: protocol.CmdTaskGet, TaskID: claimed.ID})
    var task struct{ State string `json:"State"` }
    json.Unmarshal(getResp.Data, &task)
    c2.Close()

    if task.State != "pending" {
        t.Errorf("expected pending, got %s (unclean disconnect should requeue)", task.State)
    }
}
```

**Verify after fix:**
```bash
go test ./internal/broker/ -v -run "TestBroker_CleanDisconnect|TestBroker_UncleanDisconnect" -count=1
go test ./... -race -count=1 -timeout=120s
```

---

## Fix 4: Status missing task counts [MEDIUM]

**Bug:** `waggle status` returns `{"sessions":1,"locks":0,"subscribers":0,"topics":0}` — no task counts by state.

**Root cause:** `internal/broker/router.go` `handleStatus()` doesn't include task statistics.

**Fix:** In `handleStatus()`, add task counts:
```go
func handleStatus(s *Session) protocol.Response {
    s.broker.mu.RLock()
    sessionCount := len(s.broker.sessions)
    s.broker.mu.RUnlock()

    // Count tasks by state
    allTasks, _ := s.broker.store.List(tasks.ListFilter{})
    taskCounts := map[string]int{}
    for _, t := range allTasks {
        taskCounts[t.State]++
    }

    status := map[string]interface{}{
        "sessions":    sessionCount,
        "locks":       s.broker.lockMgr.Count(),
        "subscribers": s.broker.hub.SubscriberCount(),
        "topics":      s.broker.hub.TopicCount(),
        "tasks":       taskCounts,
    }
    data, _ := json.Marshal(status)
    return protocol.OKResponse(data)
}
```

**Test:**
```go
func TestBroker_StatusIncludesTaskCounts(t *testing.T) {
    b, sockPath := startTestBroker(t)
    _ = b

    c := connectClient(t, sockPath, "status-test")
    // Create 2 pending tasks
    c.Send(protocol.Request{Cmd: protocol.CmdTaskCreate, Payload: json.RawMessage(`{"a":1}`)})
    c.Send(protocol.Request{Cmd: protocol.CmdTaskCreate, Payload: json.RawMessage(`{"a":2}`)})
    // Claim 1
    c.Send(protocol.Request{Cmd: protocol.CmdTaskClaim})

    resp, _ := c.Send(protocol.Request{Cmd: protocol.CmdStatus})
    var status struct {
        Tasks map[string]int `json:"tasks"`
    }
    json.Unmarshal(resp.Data, &status)
    c.Close()

    if status.Tasks == nil {
        t.Fatal("status missing 'tasks' field")
    }
    if status.Tasks["pending"] != 1 {
        t.Errorf("expected 1 pending, got %d", status.Tasks["pending"])
    }
    if status.Tasks["claimed"] != 1 {
        t.Errorf("expected 1 claimed, got %d", status.Tasks["claimed"])
    }
}
```

**Verify:**
```bash
go test ./internal/broker/ -v -run TestBroker_StatusIncludesTaskCounts -count=1
```

---

## Fix 5: Task update command missing from CLI [LOW]

**Bug:** `waggle task update` doesn't exist as a CLI command. The router returns "not yet implemented" but there's no way to reach it.

**Fix:** Create `cmd/task_update.go`:
```go
package cmd

import (
    "github.com/seungpyoson/waggle/internal/protocol"
    "github.com/spf13/cobra"
)

func init() {
    taskCmd.AddCommand(taskUpdateCmd)
}

var taskUpdateCmd = &cobra.Command{
    Use:   "update <id> <message>",
    Short: "Update a task with a progress note (not yet implemented)",
    Args:  cobra.ExactArgs(2),
    RunE: func(cmd *cobra.Command, args []string) error {
        // Task update is deferred to v2
        printErr(protocol.ErrInvalidRequest, "task update is not yet implemented")
        return nil
    },
}
```

**Verify:** `./waggle task --help` should list `update`.

---

## Fix 6: Input validation [LOW]

**Bug:** Negative priority (-999) accepted. No length limits on type/tags fields.

**Fix:** In `router.go` `handleTaskCreate()`, add validation:
```go
// After parsing the request, before creating:
if req.Priority != nil && *req.Priority < 0 {
    return protocol.ErrResponse(protocol.ErrInvalidRequest, "priority must be non-negative")
}
if len(req.Type) > 255 {
    return protocol.ErrResponse(protocol.ErrInvalidRequest, "type exceeds 255 character limit")
}
```

**Test:**
```go
func TestBroker_RejectsNegativePriority(t *testing.T) {
    b, sockPath := startTestBroker(t)
    _ = b
    c := connectClient(t, sockPath, "validator")
    neg := -1
    resp, _ := c.Send(protocol.Request{
        Cmd:      protocol.CmdTaskCreate,
        Payload:  json.RawMessage(`{"desc":"neg"}`),
        Priority: &neg,
    })
    c.Close()
    if resp.OK {
        t.Error("expected rejection for negative priority")
    }
}
```

---

## Execution Order

1. Fix 1 (tags filter) — highest priority, feature is broken
2. Fix 3 (clean/unclean disconnect) — correctness issue
3. Fix 2 (events format) — user-facing format issue
4. Fix 4 (status task counts) — missing feature
5. Fix 5 (task update CLI) — trivial
6. Fix 6 (input validation) — hardening

## Final Verification

After all fixes:
```bash
# Full regression
go test ./... -race -count=1 -timeout=120s
go vet ./...

# Live smoke test
SMOKE_DIR=$(mktemp -d) && mkdir -p "$SMOKE_DIR/.git" && export WAGGLE_ROOT="$SMOKE_DIR"
waggle start --foreground &
sleep 2

# Tags filter
waggle task create '{"desc":"untagged"}' --type test
waggle task create '{"desc":"tagged"}' --type test --tags frontend
waggle task claim --tags frontend
# Should claim the tagged task

# Status with task counts
waggle status
# Should show {"tasks":{"pending":1,"claimed":1},...}

# Events format
waggle events subscribe task.events &
waggle task create '{"desc":"event test"}' --type test
# Event should be raw JSON without {ok:false} wrapper

waggle stop
```

## PR Format
```
Title: #30: Fix 6 post-MVP bugs (tags, events, disconnect, status, validation)
Body:
- Fix 1: Tags filter on claim was completely ignored (HIGH)
- Fix 2: Events subscribe wrapped in Response object
- Fix 3: Clean/unclean disconnect uses flag instead of name prefix hack
- Fix 4: Status now includes task counts by state
- Fix 5: Task update CLI command (stub)
- Fix 6: Input validation (negative priority, field length limits)

Tests: N new tests added, all pass
Regression: go test ./... -race clean
```
