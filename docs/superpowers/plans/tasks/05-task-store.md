# Task 5: Task Store — SQLite CRUD

**Files:**
- Create: `internal/tasks/store.go`
- Create: `internal/tasks/store_test.go`
- Dependency: `go get modernc.org/sqlite`

Core task persistence: create, claim (atomic), complete, fail, cancel, list, get. SQLite with WAL mode.

- [ ] **Step 1: Add SQLite dependency**

Run: `cd ~/Projects/Claude/waggle && go get modernc.org/sqlite`

- [ ] **Step 2: Write failing tests for create and get**

```go
// internal/tasks/store_test.go
package tasks

import (
    "path/filepath"
    "testing"
)

func newTestStore(t *testing.T) *Store {
    t.Helper()
    db := filepath.Join(t.TempDir(), "test.db")
    s, err := NewStore(db)
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { s.Close() })
    return s
}

func TestStore_CreateAndGet(t *testing.T) {
    s := newTestStore(t)

    task, err := s.Create(CreateParams{
        Payload:  `{"desc":"fix bug"}`,
        Type:     "code-edit",
        Tags:     []string{"auth"},
        Priority: 0,
    })
    if err != nil {
        t.Fatal(err)
    }
    if task.ID == 0 {
        t.Fatal("expected non-zero ID")
    }
    if task.State != StatePending {
        t.Errorf("state = %q, want %q", task.State, StatePending)
    }

    got, err := s.Get(task.ID)
    if err != nil {
        t.Fatal(err)
    }
    if got.Payload != `{"desc":"fix bug"}` {
        t.Errorf("payload = %q", got.Payload)
    }
}

func TestStore_IdempotencyKey(t *testing.T) {
    s := newTestStore(t)

    t1, _ := s.Create(CreateParams{Payload: `{"a":1}`, IdempotencyKey: "key-1"})
    t2, _ := s.Create(CreateParams{Payload: `{"a":2}`, IdempotencyKey: "key-1"})

    if t1.ID != t2.ID {
        t.Errorf("idempotency failed: got IDs %d and %d", t1.ID, t2.ID)
    }
}

func TestStore_GetNotFound(t *testing.T) {
    s := newTestStore(t)
    _, err := s.Get(999)
    if err == nil {
        t.Fatal("expected error for missing task")
    }
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/tasks/ -v`
Expected: FAIL — `NewStore` not defined

- [ ] **Step 4: Implement Store with schema, create, get**

Implement `internal/tasks/store.go` with:
- State constants: `StatePending`, `StateClaimed`, `StateCompleted`, `StateFailed`, `StateCanceled`
- `Task` struct matching the SQLite schema
- `CreateParams` struct
- `NewStore(dbPath)` — opens DB, sets WAL mode, runs migration
- `Create(CreateParams)` — inserts task, respects idempotency key
- `Get(id)` — retrieves task by ID
- `scanTask` helper for row scanning with nullable fields
- `migrate(db)` — creates table and indexes (see spec for schema)

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/tasks/ -v`
Expected: PASS (3/3)

- [ ] **Step 6: Write failing tests for claim**

```go
func TestStore_Claim(t *testing.T) {
    s := newTestStore(t)
    created, _ := s.Create(CreateParams{Payload: `{"a":1}`})

    claimed, err := s.Claim("worker-1", ClaimFilter{})
    if err != nil {
        t.Fatal(err)
    }
    if claimed.ID != created.ID {
        t.Errorf("claimed wrong task")
    }
    if claimed.State != StateClaimed {
        t.Errorf("state = %q", claimed.State)
    }
    if claimed.ClaimToken == "" {
        t.Error("no claim token")
    }
    if claimed.ClaimedBy != "worker-1" {
        t.Errorf("claimed_by = %q", claimed.ClaimedBy)
    }
}

func TestStore_ClaimEmpty(t *testing.T) {
    s := newTestStore(t)
    _, err := s.Claim("worker-1", ClaimFilter{})
    if err == nil {
        t.Fatal("expected error for empty queue")
    }
}

func TestStore_ClaimPriority(t *testing.T) {
    s := newTestStore(t)
    s.Create(CreateParams{Payload: `{"low":true}`, Priority: 0})
    s.Create(CreateParams{Payload: `{"high":true}`, Priority: 10})

    claimed, _ := s.Claim("w", ClaimFilter{})
    if claimed.Payload != `{"high":true}` {
        t.Errorf("expected high priority task, got %q", claimed.Payload)
    }
}

func TestStore_ClaimWithTypeFilter(t *testing.T) {
    s := newTestStore(t)
    s.Create(CreateParams{Payload: `{"a":1}`, Type: "test"})
    s.Create(CreateParams{Payload: `{"b":2}`, Type: "code-edit"})

    claimed, _ := s.Claim("w", ClaimFilter{Type: "code-edit"})
    if claimed.Type != "code-edit" {
        t.Errorf("type = %q", claimed.Type)
    }
}

func TestStore_ClaimSkipsBlocked(t *testing.T) {
    s := newTestStore(t)
    dep, _ := s.Create(CreateParams{Payload: `{"dep":true}`})
    s.Create(CreateParams{Payload: `{"blocked":true}`, DependsOn: []int64{dep.ID}})

    claimed, _ := s.Claim("w", ClaimFilter{})
    if claimed.ID != dep.ID {
        t.Error("claimed blocked task instead of dependency")
    }
}
```

- [ ] **Step 7: Implement Claim**

Implement `ClaimFilter` struct and `Claim(worker, filter)` method:
- `BEGIN IMMEDIATE` transaction
- SELECT eligible task (state=pending, blocked=0, optional type filter)
- ORDER BY priority DESC, created_at ASC, LIMIT 1
- Generate random claim token (16 bytes hex)
- UPDATE to claimed state with token, worker, timestamp, lease expiry
- Commit and return task via Get

- [ ] **Step 8: Run tests to verify claim passes**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/tasks/ -v`
Expected: PASS (8/8)

- [ ] **Step 9: Write failing tests for complete, fail, cancel**

```go
func TestStore_Complete(t *testing.T) {
    s := newTestStore(t)
    s.Create(CreateParams{Payload: `{}`})
    claimed, _ := s.Claim("w", ClaimFilter{})

    err := s.Complete(claimed.ID, claimed.ClaimToken, `{"commit":"abc"}`)
    if err != nil {
        t.Fatal(err)
    }
    got, _ := s.Get(claimed.ID)
    if got.State != StateCompleted {
        t.Errorf("state = %q", got.State)
    }
}

func TestStore_CompleteInvalidToken(t *testing.T) {
    s := newTestStore(t)
    s.Create(CreateParams{Payload: `{}`})
    claimed, _ := s.Claim("w", ClaimFilter{})

    err := s.Complete(claimed.ID, "wrong-token", `{}`)
    if err == nil {
        t.Fatal("expected error for invalid token")
    }
}

func TestStore_Fail(t *testing.T) {
    s := newTestStore(t)
    s.Create(CreateParams{Payload: `{}`})
    claimed, _ := s.Claim("w", ClaimFilter{})

    err := s.Fail(claimed.ID, claimed.ClaimToken, "out of memory")
    if err != nil {
        t.Fatal(err)
    }
    got, _ := s.Get(claimed.ID)
    if got.State != StateFailed {
        t.Errorf("state = %q", got.State)
    }
}

func TestStore_CancelPending(t *testing.T) {
    s := newTestStore(t)
    task, _ := s.Create(CreateParams{Payload: `{}`})

    err := s.Cancel(task.ID)
    if err != nil {
        t.Fatal(err)
    }
    got, _ := s.Get(task.ID)
    if got.State != StateCanceled {
        t.Errorf("state = %q", got.State)
    }
}

func TestStore_CancelClaimed(t *testing.T) {
    s := newTestStore(t)
    s.Create(CreateParams{Payload: `{}`})
    claimed, _ := s.Claim("w", ClaimFilter{})

    err := s.Cancel(claimed.ID)
    if err != nil {
        t.Fatal(err)
    }
    got, _ := s.Get(claimed.ID)
    if got.State != StateCanceled {
        t.Errorf("state = %q", got.State)
    }
}
```

- [ ] **Step 10: Implement Complete, Fail, Cancel**

Each method: UPDATE with state check + token check (for complete/fail), return error if 0 rows affected.

- [ ] **Step 11: Write failing tests for List**

```go
func TestStore_List(t *testing.T) {
    s := newTestStore(t)
    s.Create(CreateParams{Payload: `{"a":1}`, Type: "test"})
    s.Create(CreateParams{Payload: `{"b":2}`, Type: "code-edit"})

    tasks, err := s.List(ListFilter{})
    if err != nil {
        t.Fatal(err)
    }
    if len(tasks) != 2 {
        t.Fatalf("expected 2 tasks, got %d", len(tasks))
    }
}

func TestStore_ListFilterState(t *testing.T) {
    s := newTestStore(t)
    s.Create(CreateParams{Payload: `{}`})
    s.Create(CreateParams{Payload: `{}`})
    s.Claim("w", ClaimFilter{})

    tasks, _ := s.List(ListFilter{State: StateClaimed})
    if len(tasks) != 1 {
        t.Fatalf("expected 1 claimed, got %d", len(tasks))
    }
}
```

- [ ] **Step 12: Implement List**

`ListFilter` struct with State, Type, Owner fields. Build dynamic query with optional WHERE clauses.

- [ ] **Step 13: Run all store tests**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/tasks/ -v`
Expected: ALL PASS

- [ ] **Step 14: Write failing test for RequeueAllClaimed (crash recovery)**

```go
func TestStore_RequeueAllClaimed(t *testing.T) {
    s := newTestStore(t)
    s.Create(CreateParams{Payload: `{"a":1}`})
    s.Create(CreateParams{Payload: `{"b":2}`})
    s.Claim("w1", ClaimFilter{})
    s.Claim("w2", ClaimFilter{})

    // Simulate broker restart — re-queue everything claimed
    count, err := s.RequeueAllClaimed()
    if err != nil {
        t.Fatal(err)
    }
    if count != 2 {
        t.Errorf("expected 2 requeued, got %d", count)
    }

    // Both should be pending again
    tasks, _ := s.List(ListFilter{State: StatePending})
    if len(tasks) != 2 {
        t.Errorf("expected 2 pending, got %d", len(tasks))
    }
}
```

- [ ] **Step 15: Implement RequeueAllClaimed**

`RequeueAllClaimed()` — called once on broker startup. Resets all `claimed` tasks to `pending` (blocked=0), clears claim fields, increments retry count. Returns number of tasks re-queued. This handles broker crash recovery per the spec.

- [ ] **Step 16: Run all store tests**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/tasks/ -v`
Expected: ALL PASS

- [ ] **Step 17: Commit**

```bash
git add internal/tasks/store.go internal/tasks/store_test.go
python3 ~/.claude/lib/safe_git.py commit -m "feat: task store — SQLite CRUD with claim, complete, fail, cancel, list"
```
