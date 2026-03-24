# Task 6: Task Dependencies — Block/Unblock Logic

**Files:**
- Create: `internal/tasks/deps.go`
- Create: `internal/tasks/deps_test.go`
- Depends on: Task 5 (store)

Resolves dependencies on task completion. Unblocks waiting tasks when all deps are met. Fails blocked tasks when a dep fails/cancels.

- [ ] **Step 1: Write failing tests**

```go
// internal/tasks/deps_test.go
package tasks

import "testing"

func TestDeps_UnblockOnComplete(t *testing.T) {
    s := newTestStore(t)
    dep, _ := s.Create(CreateParams{Payload: `{"dep":true}`})
    child, _ := s.Create(CreateParams{Payload: `{"child":true}`, DependsOn: []int64{dep.ID}})

    got, _ := s.Get(child.ID)
    if !got.Blocked {
        t.Fatal("child should be blocked")
    }

    claimed, _ := s.Claim("w", ClaimFilter{})
    s.Complete(claimed.ID, claimed.ClaimToken, `{}`)

    unblocked, err := ResolveDeps(s, dep.ID)
    if err != nil {
        t.Fatal(err)
    }
    if len(unblocked) != 1 || unblocked[0] != child.ID {
        t.Errorf("unblocked = %v, want [%d]", unblocked, child.ID)
    }

    got, _ = s.Get(child.ID)
    if got.Blocked {
        t.Error("child should be unblocked")
    }
}

func TestDeps_FailOnDepFailure(t *testing.T) {
    s := newTestStore(t)
    dep, _ := s.Create(CreateParams{Payload: `{}`})
    child, _ := s.Create(CreateParams{Payload: `{}`, DependsOn: []int64{dep.ID}})

    claimed, _ := s.Claim("w", ClaimFilter{})
    s.Fail(claimed.ID, claimed.ClaimToken, "error")

    failed, err := FailDependents(s, dep.ID)
    if err != nil {
        t.Fatal(err)
    }
    if len(failed) != 1 || failed[0] != child.ID {
        t.Errorf("failed = %v", failed)
    }

    got, _ := s.Get(child.ID)
    if got.State != StateFailed {
        t.Errorf("state = %q", got.State)
    }
    if got.FailureReason != "dependency_failed" {
        t.Errorf("reason = %q", got.FailureReason)
    }
}

func TestDeps_MultipleDeps(t *testing.T) {
    s := newTestStore(t)
    dep1, _ := s.Create(CreateParams{Payload: `{}`})
    dep2, _ := s.Create(CreateParams{Payload: `{}`})
    child, _ := s.Create(CreateParams{Payload: `{}`, DependsOn: []int64{dep1.ID, dep2.ID}})

    c1, _ := s.Claim("w", ClaimFilter{})
    s.Complete(c1.ID, c1.ClaimToken, `{}`)
    unblocked, _ := ResolveDeps(s, dep1.ID)
    if len(unblocked) != 0 {
        t.Error("should not unblock with one dep remaining")
    }

    got, _ := s.Get(child.ID)
    if !got.Blocked {
        t.Error("child should still be blocked")
    }

    c2, _ := s.Claim("w", ClaimFilter{})
    s.Complete(c2.ID, c2.ClaimToken, `{}`)
    unblocked, _ = ResolveDeps(s, dep2.ID)
    if len(unblocked) != 1 {
        t.Errorf("expected 1 unblocked, got %d", len(unblocked))
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/tasks/ -v -run TestDeps`
Expected: FAIL — `ResolveDeps` not defined

- [ ] **Step 3: Implement deps.go**

Implement two functions:
- `ResolveDeps(store, completedID)` — find blocked tasks that depend on completedID, check if ALL their deps are now completed, unblock if so
- `FailDependents(store, failedID)` — find blocked tasks that depend on failedID, mark them as failed with reason `dependency_failed`

**IMPORTANT: Do NOT use `LIKE` for dependency queries** — `LIKE '%1%'` would match task IDs 1, 11, 21, etc. Use SQLite's `json_each()` function to correctly query the JSON array:

```sql
SELECT DISTINCT t.id, t.depends_on FROM tasks t, json_each(t.depends_on) j
WHERE t.state = 'pending' AND t.blocked = 1 AND j.value = ?
```

For checking if all deps are completed, parse the `depends_on` JSON in Go and query each dep's state individually.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/tasks/ -v -run TestDeps`
Expected: PASS (3/3)

- [ ] **Step 5: Write failing tests for cycle detection**

```go
func TestDeps_CycleDetection_Direct(t *testing.T) {
    s := newTestStore(t)
    a, _ := s.Create(CreateParams{Payload: `{}`})
    // B depends on A — fine
    b, _ := s.Create(CreateParams{Payload: `{}`, DependsOn: []int64{a.ID}})

    // Try to make A depend on B — should fail (cycle: A -> B -> A)
    err := ValidateDeps(s, []int64{b.ID}, a.ID)
    if err == nil {
        t.Fatal("expected cycle detection error")
    }
}

func TestDeps_CycleDetection_Indirect(t *testing.T) {
    s := newTestStore(t)
    a, _ := s.Create(CreateParams{Payload: `{}`})
    b, _ := s.Create(CreateParams{Payload: `{}`, DependsOn: []int64{a.ID}})
    c, _ := s.Create(CreateParams{Payload: `{}`, DependsOn: []int64{b.ID}})

    // Try to make A depend on C — should fail (cycle: A -> B -> C -> A)
    err := ValidateDeps(s, []int64{c.ID}, a.ID)
    if err == nil {
        t.Fatal("expected cycle detection error for indirect cycle")
    }
}

func TestDeps_NoCycleFalsePositive(t *testing.T) {
    s := newTestStore(t)
    a, _ := s.Create(CreateParams{Payload: `{}`})
    b, _ := s.Create(CreateParams{Payload: `{}`})

    // C depends on both A and B — no cycle
    err := ValidateDeps(s, []int64{a.ID, b.ID}, 0)
    if err != nil {
        t.Fatalf("false positive cycle detection: %v", err)
    }
}

func TestDeps_DepsExist(t *testing.T) {
    s := newTestStore(t)

    // Depend on non-existent task
    err := ValidateDeps(s, []int64{999}, 0)
    if err == nil {
        t.Fatal("expected error for non-existent dependency")
    }
}
```

- [ ] **Step 6: Implement ValidateDeps**

`ValidateDeps(store, dependsOn []int64, selfID int64)` — called before task creation:
1. Verify all referenced task IDs exist
2. For each dep, walk the dependency chain (DFS) checking if `selfID` appears anywhere in the chain
3. If `selfID` is 0 (new task, no ID yet), skip cycle check (new tasks can't be in existing chains)
4. Return error describing the cycle if found

This is called from `store.Create` when `DependsOn` is non-empty. Simple DFS — at this scale the dep graph is small.

- [ ] **Step 7: Run cycle detection tests**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/tasks/ -v -run "TestDeps_Cycle|TestDeps_NoCycle|TestDeps_DepsExist"`
Expected: PASS (4/4)

- [ ] **Step 8: Commit**

```bash
git add internal/tasks/deps.go internal/tasks/deps_test.go
python3 ~/.claude/lib/safe_git.py commit -m "feat: task dependencies — block/unblock resolution"
```
