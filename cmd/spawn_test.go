package cmd

import (
	"testing"

	"github.com/seungpyoson/waggle/internal/config"
	rt "github.com/seungpyoson/waggle/internal/runtime"
)

func TestSpawnRegistersRuntimeWatchInSharedStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	ok, msg := registerSpawnRuntimeWatch("proj-spawn", "worker-1")
	if !ok {
		t.Fatalf("registerSpawnRuntimeWatch failed: %s", msg)
	}

	store, err := rt.OpenStore(config.NewPaths("proj-spawn"))
	if err != nil {
		t.Fatalf("open runtime store: %v", err)
	}
	defer store.Close()

	watches, err := store.ListWatches()
	if err != nil {
		t.Fatalf("list watches: %v", err)
	}
	if len(watches) != 1 {
		t.Fatalf("watch count = %d, want 1", len(watches))
	}
	if watches[0].ProjectID != "proj-spawn" || watches[0].AgentName != "worker-1" || watches[0].Source != "spawn" {
		t.Fatalf("watch = %+v, want proj-spawn/worker-1/spawn", watches[0])
	}
}

func TestSpawnRuntimeWatchRegistrationFailsWithoutRuntimePath(t *testing.T) {
	t.Setenv("HOME", "")

	ok, msg := registerSpawnRuntimeWatch("proj-spawn", "worker-1")
	if ok {
		t.Fatal("expected runtime watch registration to fail without HOME")
	}
	if msg == "" {
		t.Fatal("expected runtime watch registration error message")
	}
}
