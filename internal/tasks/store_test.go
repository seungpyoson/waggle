package tasks

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
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

func TestStore_ClaimConcurrent(t *testing.T) {
	s := newTestStore(t)
	// Create 10 tasks
	for i := 0; i < 10; i++ {
		s.Create(CreateParams{Payload: `{}`})
	}

	// 10 workers claim concurrently
	var wg sync.WaitGroup
	claimed := make(map[int64]bool)
	var mu sync.Mutex

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			task, err := s.Claim("worker", ClaimFilter{})
			if err != nil {
				t.Errorf("worker %d: %v", workerID, err)
				return
			}
			mu.Lock()
			if claimed[task.ID] {
				t.Errorf("task %d claimed twice", task.ID)
			}
			claimed[task.ID] = true
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	if len(claimed) != 10 {
		t.Errorf("expected 10 unique claims, got %d", len(claimed))
	}
}

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
	if got.Result != `{"commit":"abc"}` {
		t.Errorf("result = %q", got.Result)
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
	if got.FailureReason != "out of memory" {
		t.Errorf("failure_reason = %q", got.FailureReason)
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

func TestStore_ListFilterType(t *testing.T) {
	s := newTestStore(t)
	s.Create(CreateParams{Payload: `{}`, Type: "test"})
	s.Create(CreateParams{Payload: `{}`, Type: "code-edit"})
	s.Create(CreateParams{Payload: `{}`, Type: "test"})

	tasks, _ := s.List(ListFilter{Type: "test"})
	if len(tasks) != 2 {
		t.Fatalf("expected 2 test tasks, got %d", len(tasks))
	}
}

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

func TestStore_Heartbeat(t *testing.T) {
	s := newTestStore(t)
	s.Create(CreateParams{Payload: `{}`})
	claimed, _ := s.Claim("w", ClaimFilter{})

	// Get initial lease expiry
	initialStr := claimed.LeaseExpiresAt
	initial, err := time.Parse(time.RFC3339, initialStr)
	if err != nil {
		t.Fatalf("parsing initial lease: %v", err)
	}

	// Wait to ensure time difference (1 second since timestamps are in seconds)
	time.Sleep(1100 * time.Millisecond)

	// Heartbeat should extend lease
	err = s.Heartbeat(claimed.ID, claimed.ClaimToken)
	if err != nil {
		t.Fatal(err)
	}

	// Check that lease was extended
	got, _ := s.Get(claimed.ID)
	newLease, err := time.Parse(time.RFC3339, got.LeaseExpiresAt)
	if err != nil {
		t.Fatalf("parsing new lease: %v", err)
	}

	if !newLease.After(initial) {
		t.Errorf("lease was not extended: initial=%s, new=%s", initialStr, got.LeaseExpiresAt)
	}
}

func TestStore_HeartbeatInvalidToken(t *testing.T) {
	s := newTestStore(t)
	s.Create(CreateParams{Payload: `{}`})
	claimed, _ := s.Claim("w", ClaimFilter{})

	err := s.Heartbeat(claimed.ID, "wrong-token")
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestStore_SchemaVersion(t *testing.T) {
	// Create a store, close it, reopen it
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s1, err := NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	s1.Close()

	// Reopen should succeed
	s2, err := NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	// Should be able to use it
	_, err = s2.Create(CreateParams{Payload: `{}`})
	if err != nil {
		t.Fatal(err)
	}
}

