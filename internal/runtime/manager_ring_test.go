package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
)

func TestRingBuffer_Cap(t *testing.T) {
	store := newTestStore(t)
	manager := NewManager(store, newFakeListenerFactory(), &fakeNotifier{})

	// Add RuntimeRecentErrorCap+1 errors
	cap := config.Defaults.RuntimeRecentErrorCap
	for i := 0; i < cap+1; i++ {
		key := fmt.Sprintf("test-key-%d", i)
		manager.captureDeliveryError(key, fmt.Errorf("error %d", i))
	}

	// Recent errors should only contain cap entries
	errors := manager.RecentErrors()
	if len(errors) != cap {
		t.Fatalf("recent errors count = %d, want %d", len(errors), cap)
	}

	// First error should be gone (oldest evicted)
	for _, e := range errors {
		if e.WatchKey == "test-key-0" {
			t.Fatalf("oldest error should have been evicted, but found: %v", e)
		}
	}
}

func TestRingBuffer_ThreadSafe(t *testing.T) {
	store := newTestStore(t)
	manager := NewManager(store, newFakeListenerFactory(), &fakeNotifier{})

	cap := config.Defaults.RuntimeRecentErrorCap
	var wg sync.WaitGroup
	errors := make(chan string, cap*2)

	for i := 0; i < cap*2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := fmt.Sprintf("worker-%d", idx)
			manager.captureDeliveryError(key, fmt.Errorf("error from %d", idx))
			errors <- key
		}(i)
	}

	wg.Wait()
	close(errors)

	recentErrors := manager.RecentErrors()
	if len(recentErrors) > cap {
		t.Fatalf("concurrent captureDeliveryError caused overflow: got %d, cap is %d", len(recentErrors), cap)
	}
}

func TestRingBuffer_ConcurrentReadWrite(t *testing.T) {
	store := newTestStore(t)
	manager := NewManager(store, newFakeListenerFactory(), &fakeNotifier{})

	cap := config.Defaults.RuntimeRecentErrorCap
	const readers = 4
	const writers = 4
	const iterations = 64

	start := make(chan struct{})
	var wg sync.WaitGroup

	for reader := 0; reader < readers; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < iterations; i++ {
				snapshot := manager.RecentErrors()
				if len(snapshot) > cap {
					t.Errorf("RecentErrors snapshot overflowed: got %d, cap is %d", len(snapshot), cap)
					return
				}
			}
		}()
	}

	for writer := 0; writer < writers; writer++ {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			<-start
			for i := 0; i < iterations; i++ {
				manager.captureDeliveryError(fmt.Sprintf("writer-%d", writer), fmt.Errorf("error %d", i))
			}
		}(writer)
	}

	close(start)
	wg.Wait()

	recentErrors := manager.RecentErrors()
	if len(recentErrors) > cap {
		t.Fatalf("final RecentErrors snapshot overflowed: got %d, cap is %d", len(recentErrors), cap)
	}
	if recentErrors == nil {
		t.Fatal("RecentErrors returned nil snapshot")
	}
}

func TestRingBuffer_Snapshot(t *testing.T) {
	store := newTestStore(t)
	manager := NewManager(store, newFakeListenerFactory(), &fakeNotifier{})

	manager.captureDeliveryError("key-1", errors.New("error 1"))
	snapshot1 := manager.RecentErrors()
	if len(snapshot1) != 1 {
		t.Fatalf("snapshot1 length = %d, want 1", len(snapshot1))
	}

	snapshot1[0].WatchKey = "mutated"
	snapshot1[0].Error = "mutated"

	freshSnapshot := manager.RecentErrors()
	if len(freshSnapshot) != 1 {
		t.Fatalf("fresh snapshot length = %d, want 1", len(freshSnapshot))
	}
	if freshSnapshot[0].WatchKey != "key-1" {
		t.Fatalf("fresh snapshot watch key = %q, want key-1", freshSnapshot[0].WatchKey)
	}
	if freshSnapshot[0].Error != "error 1" {
		t.Fatalf("fresh snapshot error = %q, want error 1", freshSnapshot[0].Error)
	}

	manager.captureDeliveryError("key-2", errors.New("error 2"))
	snapshot2 := manager.RecentErrors()

	// Mutating snapshot1 should not affect later snapshots.
	if len(snapshot1) >= 1 && len(snapshot2) >= 2 {
		if snapshot1[0].WatchKey == snapshot2[1].WatchKey {
			t.Fatalf("snapshots should be independent copies")
		}
	}

	// snapshot1 should still have only one entry
	if len(snapshot1) != 1 {
		t.Fatalf("snapshot1 length = %d, want 1 (should not change after second error)", len(snapshot1))
	}
}

func TestRingBuffer_Empty(t *testing.T) {
	store := newTestStore(t)
	manager := NewManager(store, newFakeListenerFactory(), &fakeNotifier{})

	errors := manager.RecentErrors()
	if len(errors) != 0 {
		t.Fatalf("fresh manager should have no errors, got %d", len(errors))
	}
	if errors == nil {
		t.Fatalf("should return non-nil empty slice, not nil")
	}
}

func TestDaemonState_IncludesRecentErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	paths := config.NewPaths("")
	store, err := NewStore(paths.RuntimeDB)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	manager := NewManager(store, newFakeListenerFactory(), &fakeNotifier{})

	// Capture some errors
	manager.captureDeliveryError("watch-1", errors.New("error 1"))
	manager.captureDeliveryError("watch-2", errors.New("error 2"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunDaemon(ctx, paths, manager)
	}()

	waitFor(t, "daemon running", func() bool {
		state, err := LoadState(paths)
		return err == nil && state.Running
	})

	// Give daemon time to refresh state
	time.Sleep(100 * time.Millisecond)

	state, err := LoadState(paths)
	if err != nil {
		t.Fatal(err)
	}

	if len(state.RecentErrors) != 2 {
		t.Fatalf("state.RecentErrors length = %d, want 2", len(state.RecentErrors))
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not stop")
	}
}
