package tasks

import (
	"testing"
	"time"
)

// TestRequeue_ExpiredLease verifies that expired leases are re-queued
func TestRequeue_ExpiredLease(t *testing.T) {
	s := newTestStore(t)

	// Create and claim a task
	s.Create(CreateParams{Payload: `{"test":"data"}`})
	claimed, err := s.Claim("worker-1", ClaimFilter{})
	if err != nil {
		t.Fatal(err)
	}

	// Manually expire the lease by setting lease_expires_at to the past
	pastTime := time.Now().Add(-1 * time.Minute).UTC().Format(time.RFC3339)
	_, err = s.db.Exec(`
		UPDATE tasks
		SET lease_expires_at = ?
		WHERE id = ?
	`, pastTime, claimed.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Requeue expired leases
	count, err := s.RequeueExpiredLeases()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 requeued, got %d", count)
	}

	// Verify task is back to pending
	task, _ := s.Get(claimed.ID)
	if task.State != StatePending {
		t.Errorf("state = %q, want %q", task.State, StatePending)
	}
	if task.RetryCount != 1 {
		t.Errorf("retry_count = %d, want 1", task.RetryCount)
	}
}

// TestRequeue_MaxRetriesExceeded verifies that tasks exceeding max retries are marked as failed
func TestRequeue_MaxRetriesExceeded(t *testing.T) {
	s := newTestStore(t)

	// Create a task with max_retries=3
	s.Create(CreateParams{Payload: `{"test":"data"}`, MaxRetries: 3})
	claimed, _ := s.Claim("worker-1", ClaimFilter{})

	// Set retry_count to 2 (one more retry will exceed max)
	_, err := s.db.Exec(`
		UPDATE tasks
		SET retry_count = 2
		WHERE id = ?
	`, claimed.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Expire the lease
	pastTime := time.Now().Add(-1 * time.Minute).UTC().Format(time.RFC3339)
	_, err = s.db.Exec(`
		UPDATE tasks
		SET lease_expires_at = ?
		WHERE id = ?
	`, pastTime, claimed.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Requeue expired leases
	count, err := s.RequeueExpiredLeases()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 processed, got %d", count)
	}

	// Verify task is marked as failed
	task, _ := s.Get(claimed.ID)
	if task.State != StateFailed {
		t.Errorf("state = %q, want %q", task.State, StateFailed)
	}
	if task.FailureReason != "max_retries_exceeded" {
		t.Errorf("failure_reason = %q, want %q", task.FailureReason, "max_retries_exceeded")
	}
	if task.RetryCount != 3 {
		t.Errorf("retry_count = %d, want 3", task.RetryCount)
	}
}

// TestHeartbeat_ExtendsLease verifies that heartbeat extends the lease
func TestHeartbeat_ExtendsLease(t *testing.T) {
	s := newTestStore(t)
	s.Create(CreateParams{Payload: `{}`})
	claimed, _ := s.Claim("w", ClaimFilter{})

	// Get initial lease expiry
	initialStr := claimed.LeaseExpiresAt
	initial, err := time.Parse(time.RFC3339, initialStr)
	if err != nil {
		t.Fatalf("parsing initial lease: %v", err)
	}

	// Wait to ensure time difference
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

// TestHeartbeat_InvalidToken verifies that heartbeat fails with invalid token
func TestHeartbeat_InvalidToken(t *testing.T) {
	s := newTestStore(t)
	s.Create(CreateParams{Payload: `{}`})
	claimed, _ := s.Claim("w", ClaimFilter{})

	err := s.Heartbeat(claimed.ID, "wrong-token")
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

// TestLeaseChecker_Goroutine verifies that the lease checker goroutine works
func TestLeaseChecker_Goroutine(t *testing.T) {
	s := newTestStore(t)

	// Create and claim a task
	s.Create(CreateParams{Payload: `{"test":"data"}`})
	claimed, _ := s.Claim("worker-1", ClaimFilter{})

	// Manually expire the lease
	pastTime := time.Now().Add(-1 * time.Minute).UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		UPDATE tasks
		SET lease_expires_at = ?
		WHERE id = ?
	`, pastTime, claimed.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Start lease checker with short interval
	stop := make(chan struct{})
	defer close(stop)
	go StartLeaseChecker(s, 100*time.Millisecond, stop)

	// Wait for lease checker to run
	time.Sleep(300 * time.Millisecond)

	// Verify task was re-queued
	task, _ := s.Get(claimed.ID)
	if task.State != StatePending {
		t.Errorf("state = %q, want %q", task.State, StatePending)
	}
}

