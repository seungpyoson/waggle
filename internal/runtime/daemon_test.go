package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

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
