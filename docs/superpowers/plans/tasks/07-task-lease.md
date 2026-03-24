# Task 7: Lease Checker — Expiry and Re-queue

**Files:**
- Create: `internal/tasks/lease.go`
- Create: `internal/tasks/lease_test.go`
- Modify: `internal/tasks/store.go` (add Heartbeat method)
- Depends on: Task 5 (store)

Background goroutine that periodically checks for expired leases and re-queues tasks.

- [ ] **Step 1: Write failing tests**

```go
// internal/tasks/lease_test.go
package tasks

import (
    "testing"
    "time"
)

func TestRequeueExpired(t *testing.T) {
    s := newTestStore(t)
    s.Create(CreateParams{Payload: `{}`})
    claimed, _ := s.Claim("w", ClaimFilter{})

    // Manually expire the lease
    s.db.Exec(`UPDATE tasks SET lease_expires_at = ? WHERE id = ?`,
        time.Now().UTC().Add(-1*time.Minute).Format(time.RFC3339), claimed.ID)

    requeued, err := RequeueExpired(s)
    if err != nil {
        t.Fatal(err)
    }
    if len(requeued) != 1 {
        t.Fatalf("expected 1 requeued, got %d", len(requeued))
    }

    got, _ := s.Get(claimed.ID)
    if got.State != StatePending {
        t.Errorf("state = %q", got.State)
    }
    if got.RetryCount != 1 {
        t.Errorf("retry_count = %d", got.RetryCount)
    }
}

func TestRequeueExpired_MaxRetries(t *testing.T) {
    s := newTestStore(t)
    s.Create(CreateParams{Payload: `{}`, MaxRetries: 1})
    claimed, _ := s.Claim("w", ClaimFilter{})

    s.db.Exec(`UPDATE tasks SET retry_count = 1, lease_expires_at = ? WHERE id = ?`,
        time.Now().UTC().Add(-1*time.Minute).Format(time.RFC3339), claimed.ID)

    RequeueExpired(s)

    got, _ := s.Get(claimed.ID)
    if got.State != StateFailed {
        t.Errorf("state = %q, want failed", got.State)
    }
    if got.FailureReason != "max_retries_exceeded" {
        t.Errorf("reason = %q", got.FailureReason)
    }
}

func TestRequeueExpired_NotExpired(t *testing.T) {
    s := newTestStore(t)
    s.Create(CreateParams{Payload: `{}`})
    s.Claim("w", ClaimFilter{})

    requeued, _ := RequeueExpired(s)
    if len(requeued) != 0 {
        t.Error("should not requeue non-expired task")
    }
}

func TestHeartbeat(t *testing.T) {
    s := newTestStore(t)
    s.Create(CreateParams{Payload: `{}`})
    claimed, _ := s.Claim("w", ClaimFilter{})

    oldExpiry := claimed.LeaseExpiresAt

    err := s.Heartbeat(claimed.ID, claimed.ClaimToken)
    if err != nil {
        t.Fatal(err)
    }

    got, _ := s.Get(claimed.ID)
    if got.LeaseExpiresAt == oldExpiry {
        t.Error("lease was not renewed")
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/tasks/ -v -run "TestRequeue|TestHeartbeat"`
Expected: FAIL

- [ ] **Step 3: Implement lease.go**

`RequeueExpired(store)` — query claimed tasks where `lease_expires_at < now()`. For each:
- If retry_count+1 >= max_retries → set state=failed, reason=max_retries_exceeded
- Otherwise → set state=pending, blocked=0, clear claim fields, increment retry_count

- [ ] **Step 4: Add Heartbeat method to store.go**

`Heartbeat(id, token)` — read lease_duration for the task, compute new expiry = now + duration, update lease_expires_at. Validate claim_token.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/tasks/ -v -run "TestRequeue|TestHeartbeat"`
Expected: PASS (4/4)

- [ ] **Step 6: Commit**

```bash
git add internal/tasks/lease.go internal/tasks/lease_test.go internal/tasks/store.go
python3 ~/.claude/lib/safe_git.py commit -m "feat: lease checker — expiry, re-queue, heartbeat"
```
