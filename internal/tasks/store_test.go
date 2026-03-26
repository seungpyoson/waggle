package tasks

import (
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/seungpyoson/waggle/internal/config"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)

	// Set pragmas (same as production)
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA busy_timeout=%d", config.Defaults.BusyTimeout.Milliseconds())); err != nil {
		db.Close()
		t.Fatal(err)
	}

	s, err := NewStore(db)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return s
}

// TestStore_CancelExpiredTTL verifies that tasks with expired TTL are canceled
func TestStore_CancelExpiredTTL(t *testing.T) {
	s := newTestStore(t)

	// Create task with TTL=1 second
	task, err := s.Create(CreateParams{
		Payload: `{"desc":"expires soon"}`,
		Type:    "test",
		TTL:     1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for TTL to expire
	time.Sleep(2 * time.Second)

	// Cancel expired tasks
	count, err := s.CancelExpiredTTL()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 canceled task, got %d", count)
	}

	// Verify task is canceled
	updated, err := s.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != StateCanceled {
		t.Errorf("expected state=canceled, got %s", updated.State)
	}
	if updated.FailureReason != "ttl_expired" {
		t.Errorf("expected failure_reason=ttl_expired, got %s", updated.FailureReason)
	}
}

// TestStore_CancelExpiredTTL_NoTTL verifies tasks without TTL are not canceled
func TestStore_CancelExpiredTTL_NoTTL(t *testing.T) {
	s := newTestStore(t)

	// Create task without TTL
	task, err := s.Create(CreateParams{
		Payload: `{"desc":"no ttl"}`,
		Type:    "test",
		TTL:     0,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wait
	time.Sleep(2 * time.Second)

	// Cancel expired tasks
	count, err := s.CancelExpiredTTL()
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 canceled tasks, got %d", count)
	}

	// Verify task is still pending
	updated, err := s.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != StatePending {
		t.Errorf("expected state=pending, got %s", updated.State)
	}
}

// TestStore_CancelExpiredTTL_ClaimedIgnored verifies claimed tasks are not canceled by TTL
func TestStore_CancelExpiredTTL_ClaimedIgnored(t *testing.T) {
	s := newTestStore(t)

	// Create task with TTL=1 second
	task, err := s.Create(CreateParams{
		Payload: `{"desc":"claimed task"}`,
		Type:    "test",
		TTL:     1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Claim the task
	_, err = s.Claim("worker-1", ClaimFilter{Type: "test"})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for TTL to expire
	time.Sleep(2 * time.Second)

	// Cancel expired tasks
	count, err := s.CancelExpiredTTL()
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 canceled tasks (claimed task should be ignored), got %d", count)
	}

	// Verify task is still claimed
	updated, err := s.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != StateClaimed {
		t.Errorf("expected state=claimed, got %s", updated.State)
	}
}

// TestStore_CancelExpiredTTL_Idempotent verifies CancelExpiredTTL is idempotent
func TestStore_CancelExpiredTTL_Idempotent(t *testing.T) {
	s := newTestStore(t)

	// Create task with TTL=1 second
	_, err := s.Create(CreateParams{
		Payload: `{"desc":"idempotent test"}`,
		Type:    "test",
		TTL:     1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for TTL to expire
	time.Sleep(2 * time.Second)

	// First call should cancel 1 task
	count, err := s.CancelExpiredTTL()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("first call: expected 1 canceled task, got %d", count)
	}

	// Second call should cancel 0 tasks
	count, err = s.CancelExpiredTTL()
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("second call: expected 0 canceled tasks, got %d", count)
	}
}

// TestStore_CancelExpiredTTL_ClaimRace verifies claim wins over TTL expiry
func TestStore_CancelExpiredTTL_ClaimRace(t *testing.T) {
	s := newTestStore(t)

	// Create task with TTL=1 second
	_, err := s.Create(CreateParams{
		Payload: `{"desc":"race test"}`,
		Type:    "test",
		TTL:     1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Start goroutine that claims the task
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(500 * time.Millisecond)
		s.Claim("worker-1", ClaimFilter{Type: "test"})
	}()

	// Wait for TTL to expire
	time.Sleep(2 * time.Second)

	// Try to cancel expired tasks
	count, err := s.CancelExpiredTTL()
	if err != nil {
		t.Fatal(err)
	}

	wg.Wait()

	// If the task was claimed, it should not be canceled
	// Count could be 0 (claimed) or 1 (not claimed yet)
	// We verify the final state is either claimed or canceled
	tasks, err := s.List(ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].State != StateClaimed && tasks[0].State != StateCanceled {
		t.Errorf("expected state=claimed or canceled, got %s", tasks[0].State)
	}
	// If claimed, count should be 0
	if tasks[0].State == StateClaimed && count != 0 {
		t.Errorf("task is claimed but count=%d (expected 0)", count)
	}
}

// TestStore_CreateTaskTTLValidation verifies TTL validation
func TestStore_CreateTaskTTLValidation(t *testing.T) {
	s := newTestStore(t)

	// Negative TTL should fail
	_, err := s.Create(CreateParams{
		Payload: `{"desc":"negative ttl"}`,
		Type:    "test",
		TTL:     -1,
	})
	if err == nil {
		t.Error("expected error for negative TTL")
	}

	// TTL=0 should succeed (no TTL)
	_, err = s.Create(CreateParams{
		Payload: `{"desc":"no ttl"}`,
		Type:    "test",
		TTL:     0,
	})
	if err != nil {
		t.Errorf("TTL=0 should succeed: %v", err)
	}

	// TTL exceeding max should fail
	_, err = s.Create(CreateParams{
		Payload: `{"desc":"too long ttl"}`,
		Type:    "test",
		TTL:     config.Defaults.MaxTaskTTL + 1,
	})
	if err == nil {
		t.Error("expected error for TTL exceeding max")
	}
}

// TestStore_QueueHealth verifies queue health metrics
func TestStore_QueueHealth(t *testing.T) {
	s := newTestStore(t)

	// Create 3 tasks at staggered times
	for i := 0; i < 3; i++ {
		_, err := s.Create(CreateParams{
			Payload: fmt.Sprintf(`{"desc":"task %d"}`, i),
			Type:    "test",
		})
		if err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			time.Sleep(100 * time.Millisecond)
		}
	}

	// Wait a bit to ensure age > 0
	time.Sleep(1 * time.Second)

	// Get queue health
	health, err := s.QueueHealth(5 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	if health.PendingCount != 3 {
		t.Errorf("expected pending_count=3, got %d", health.PendingCount)
	}
	if health.OldestPendingAge <= 0 {
		t.Errorf("expected oldest_pending_age > 0, got %d", health.OldestPendingAge)
	}
}

// TestStore_QueueHealth_Empty verifies queue health with no pending tasks
func TestStore_QueueHealth_Empty(t *testing.T) {
	s := newTestStore(t)

	// Get queue health with no tasks
	health, err := s.QueueHealth(5 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	if health.PendingCount != 0 {
		t.Errorf("expected pending_count=0, got %d", health.PendingCount)
	}
	if health.OldestPendingAge != 0 {
		t.Errorf("expected oldest_pending_age=0, got %d", health.OldestPendingAge)
	}
	if health.StaleCount != 0 {
		t.Errorf("expected stale_count=0, got %d", health.StaleCount)
	}
}

// TestStore_TaskSchemaMigration verifies schema migration preserves existing tasks
func TestStore_TaskSchemaMigration(t *testing.T) {
	// Create v1 schema (without ttl column)
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	// Set pragmas
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA busy_timeout=%d", config.Defaults.BusyTimeout.Milliseconds())); err != nil {
		t.Fatal(err)
	}

	// Create v1 schema (no ttl column)
	schema := fmt.Sprintf(`
	CREATE TABLE schema_version (version INTEGER NOT NULL);
	INSERT INTO schema_version (version) VALUES (1);

	CREATE TABLE tasks (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		idempotency_key TEXT UNIQUE,
		type            TEXT,
		tags            TEXT,
		payload         TEXT NOT NULL,
		priority        INTEGER DEFAULT 0,
		state           TEXT NOT NULL DEFAULT 'pending',
		blocked         BOOLEAN DEFAULT FALSE,
		depends_on      TEXT,
		claim_token     TEXT,
		claimed_by      TEXT,
		claimed_at      TEXT,
		lease_expires_at TEXT,
		lease_duration  INTEGER DEFAULT %d,
		max_retries     INTEGER DEFAULT %d,
		retry_count     INTEGER DEFAULT 0,
		result          TEXT,
		failure_reason  TEXT,
		created_at      TEXT NOT NULL DEFAULT (strftime('%%Y-%%m-%%dT%%H:%%M:%%SZ', 'now')),
		updated_at      TEXT NOT NULL DEFAULT (strftime('%%Y-%%m-%%dT%%H:%%M:%%SZ', 'now'))
	);
	`, int(config.Defaults.LeaseDuration.Seconds()), config.Defaults.MaxRetries)

	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}

	// Insert a task
	_, err = db.Exec(`
		INSERT INTO tasks (payload, type, state)
		VALUES (?, ?, ?)
	`, `{"desc":"v1 task"}`, "test", "pending")
	if err != nil {
		t.Fatal(err)
	}

	// Now create store (should run migration)
	s, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}

	// Verify task is intact
	tasks, err := s.List(ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Payload != `{"desc":"v1 task"}` {
		t.Errorf("task payload mismatch: %s", tasks[0].Payload)
	}
	if tasks[0].TTL != 0 {
		t.Errorf("expected TTL=0 for migrated task, got %d", tasks[0].TTL)
	}
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

func TestStore_ClaimWithTagsFilter(t *testing.T) {
	s := newTestStore(t)
	s.Create(CreateParams{Payload: `{"a":1}`, Tags: []string{"backend", "api"}})
	s.Create(CreateParams{Payload: `{"b":2}`, Tags: []string{"frontend", "ui"}})
	s.Create(CreateParams{Payload: `{"c":3}`, Tags: []string{"frontend", "api"}})

	// Claim with single tag filter
	claimed, err := s.Claim("w", ClaimFilter{Tags: []string{"frontend"}})
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	// Should get first frontend task (b or c)
	hasTag := false
	for _, tag := range claimed.Tags {
		if tag == "frontend" {
			hasTag = true
			break
		}
	}
	if !hasTag {
		t.Errorf("expected frontend task, got tags = %v", claimed.Tags)
	}

	// Claim with multiple tag filter (AND logic)
	claimed2, err := s.Claim("w2", ClaimFilter{Tags: []string{"frontend", "api"}})
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	// Should get task c which has both tags
	hasFrontend := false
	hasAPI := false
	for _, tag := range claimed2.Tags {
		if tag == "frontend" {
			hasFrontend = true
		}
		if tag == "api" {
			hasAPI = true
		}
	}
	if !hasFrontend || !hasAPI {
		t.Errorf("expected frontend+api task, got tags = %v", claimed2.Tags)
	}

	// Claim with non-matching tag should fail
	_, err = s.Claim("w3", ClaimFilter{Tags: []string{"nonexistent"}})
	if err == nil {
		t.Error("expected error for non-matching tag filter")
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

	// Verify retry_count was incremented
	for _, task := range tasks {
		if task.RetryCount != 1 {
			t.Errorf("task %d: retry_count = %d, want 1", task.ID, task.RetryCount)
		}
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
	// Create a store with in-memory DB
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	// Set pragmas
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec(fmt.Sprintf("PRAGMA busy_timeout=%d", config.Defaults.BusyTimeout.Milliseconds()))

	s1, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}

	// Should be able to use it
	_, err = s1.Create(CreateParams{Payload: `{}`})
	if err != nil {
		t.Fatal(err)
	}
}

