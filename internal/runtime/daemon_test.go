package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/broker"
	"github.com/seungpyoson/waggle/internal/config"
)

func TestRunDaemon_WritesRunningAndStoppedState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	paths := config.NewPaths("")
	store, err := NewStore(paths.RuntimeDB)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	manager := NewManager(store, newFakeListenerFactory(), &fakeNotifier{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunDaemon(ctx, paths, manager)
	}()

	waitFor(t, "runtime running state", func() bool {
		state, err := LoadState(paths)
		return err == nil && state.Running && state.PID != 0
	})

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunDaemon returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunDaemon did not stop after context cancellation")
	}

	state, err := LoadState(paths)
	if err != nil {
		t.Fatal(err)
	}
	if state.Running {
		t.Fatalf("state.Running = true, want false: %+v", state)
	}
	if state.StoppedAt.IsZero() {
		t.Fatalf("state.StoppedAt not set: %+v", state)
	}
}

func TestRunDaemon_PersistsRecentErrorsWhenOnlyRecentErrorsChange(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	paths := config.NewPaths("")
	store, err := NewStore(paths.RuntimeDB)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	manager := NewManager(store, newFakeListenerFactory(), &fakeNotifier{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunDaemon(ctx, paths, manager)
	}()

	waitFor(t, "runtime running state", func() bool {
		state, err := LoadState(paths)
		return err == nil && state.Running && state.PID != 0
	})

	manager.captureDeliveryError("watch-1", errors.New("boom"))

	waitFor(t, "first persisted recent error", func() bool {
		state, err := LoadState(paths)
		return err == nil && len(state.RecentErrors) == 1 && state.LastError == "watch-1: boom"
	})

	manager.captureDeliveryError("watch-1", errors.New("boom"))

	waitFor(t, "second persisted recent error with identical last_error", func() bool {
		state, err := LoadState(paths)
		return err == nil && len(state.RecentErrors) == 2 && state.LastError == "watch-1: boom"
	})

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunDaemon returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunDaemon did not stop after context cancellation")
	}
}

func TestSaveState_UsesMachineRuntimeStatePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	pathsA := config.NewPaths("project-a")
	pathsB := config.NewPaths("project-b")
	if pathsA.RuntimeState != pathsB.RuntimeState {
		t.Fatalf("runtime state path should be machine-local: %q vs %q", pathsA.RuntimeState, pathsB.RuntimeState)
	}

	if err := SaveState(pathsA, State{Running: true}); err != nil {
		t.Fatal(err)
	}
	if got, want := pathsA.RuntimeState, filepath.Join(home, ".waggle", config.Defaults.RuntimeDirName, config.Defaults.RuntimeStateFile); got != want {
		t.Fatalf("runtime state path = %q, want %q", got, want)
	}
}

func TestAcquireStartLock_IsExclusive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	paths := config.NewPaths("")
	release, err := AcquireStartLock(paths, config.Defaults.RuntimeStartLockStaleThreshold)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = release()
	})

	if _, err := AcquireStartLock(paths, config.Defaults.RuntimeStartLockStaleThreshold); err == nil {
		t.Fatal("expected second lock acquisition to fail")
	}
}

func TestAcquireStartLock_RecoversStaleLock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	paths := config.NewPaths("")
	if err := os.MkdirAll(paths.RuntimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(paths.RuntimeStartLockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * config.Defaults.RuntimeStartLockStaleThreshold)
	if err := os.Chtimes(paths.RuntimeStartLockDir, old, old); err != nil {
		t.Fatal(err)
	}

	release, err := AcquireStartLock(paths, config.Defaults.RuntimeStartLockStaleThreshold)
	if err != nil {
		t.Fatal(err)
	}
	if release == nil {
		t.Fatal("expected release func")
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestIsRunningRequiresMatchingState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	paths := config.NewPaths("")
	if err := os.MkdirAll(paths.RuntimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := broker.WritePID(paths.RuntimePID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(paths.RuntimePID)
	})

	if err := SaveState(paths, State{
		PID:     os.Getpid(),
		Running: false,
	}); err != nil {
		t.Fatal(err)
	}
	if IsRunning(paths) {
		t.Fatal("expected mismatched state to report not running")
	}

	if err := SaveState(paths, State{
		PID:       os.Getpid(),
		Running:   true,
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if !IsRunning(paths) {
		t.Fatal("expected matching pid/state to report running")
	}
}
