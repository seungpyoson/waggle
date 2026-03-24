package locks

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManager_AcquireAndRelease(t *testing.T) {
	m := NewManager()

	// Acquire lock
	err := m.Acquire("resource1", "owner1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify in list
	locks := m.List()
	if len(locks) != 1 {
		t.Fatalf("expected 1 lock, got %d", len(locks))
	}
	if locks[0].Resource != "resource1" || locks[0].Owner != "owner1" {
		t.Fatalf("unexpected lock: %+v", locks[0])
	}

	// Release lock
	m.Release("resource1", "owner1")

	// Verify empty
	locks = m.List()
	if len(locks) != 0 {
		t.Fatalf("expected 0 locks, got %d", len(locks))
	}
}

func TestManager_AcquireConflict(t *testing.T) {
	m := NewManager()

	// Owner1 acquires
	err := m.Acquire("resource1", "owner1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Owner2 tries to acquire same resource
	err = m.Acquire("resource1", "owner2")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// L5: Error message must include current holder's name
	if !strings.Contains(err.Error(), "owner1") {
		t.Fatalf("error message should contain holder name 'owner1', got: %v", err)
	}
}

func TestManager_SameOwnerReacquire(t *testing.T) {
	m := NewManager()

	// First acquire
	err := m.Acquire("resource1", "owner1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Same owner re-acquires (idempotent)
	err = m.Acquire("resource1", "owner1")
	if err != nil {
		t.Fatalf("expected no error on re-acquire, got %v", err)
	}

	// Should still have only 1 lock
	if m.Count() != 1 {
		t.Fatalf("expected 1 lock, got %d", m.Count())
	}
}

func TestManager_ReleaseWrongOwner(t *testing.T) {
	m := NewManager()

	// Owner1 acquires
	err := m.Acquire("resource1", "owner1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Owner2 tries to release
	m.Release("resource1", "owner2")

	// Lock should remain
	locks := m.List()
	if len(locks) != 1 {
		t.Fatalf("expected lock to remain, got %d locks", len(locks))
	}
	if locks[0].Owner != "owner1" {
		t.Fatalf("expected owner1, got %s", locks[0].Owner)
	}
}

func TestManager_ReleaseAll(t *testing.T) {
	m := NewManager()

	// Owner1 acquires 2 resources
	m.Acquire("resource1", "owner1")
	m.Acquire("resource2", "owner1")

	// Owner2 acquires 1 resource
	m.Acquire("resource3", "owner2")

	// Verify 3 locks
	if m.Count() != 3 {
		t.Fatalf("expected 3 locks, got %d", m.Count())
	}

	// ReleaseAll for owner1
	m.ReleaseAll("owner1")

	// Should have 1 lock remaining (owner2's)
	locks := m.List()
	if len(locks) != 1 {
		t.Fatalf("expected 1 lock, got %d", len(locks))
	}
	if locks[0].Owner != "owner2" {
		t.Fatalf("expected owner2, got %s", locks[0].Owner)
	}
}

func TestManager_AcquiredAtSet(t *testing.T) {
	m := NewManager()

	// Acquire lock
	err := m.Acquire("resource1", "owner1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify AcquiredAt is set and valid RFC3339
	locks := m.List()
	if len(locks) != 1 {
		t.Fatalf("expected 1 lock, got %d", len(locks))
	}

	acquiredAt := locks[0].AcquiredAt
	if acquiredAt == "" {
		t.Fatal("AcquiredAt should not be empty")
	}

	// Parse as RFC3339
	_, err = time.Parse(time.RFC3339, acquiredAt)
	if err != nil {
		t.Fatalf("AcquiredAt should be valid RFC3339, got %s: %v", acquiredAt, err)
	}
}

// TestManager_ConcurrentAccess verifies L6: no race conditions
func TestManager_ConcurrentAccess(t *testing.T) {
	m := NewManager()
	var wg sync.WaitGroup

	// Run concurrent acquires and releases
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			resource := "resource1"
			owner := "owner1"
			m.Acquire(resource, owner)
			time.Sleep(time.Millisecond)
			m.Release(resource, owner)
		}(i)
	}

	wg.Wait()
}

