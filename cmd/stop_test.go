package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
)

func TestAwaitEndpointRemoval(t *testing.T) {
	for _, remove := range []bool{false, true} {
		name := "retained"
		if remove {
			name = "removed after acknowledgement"
		}
		t.Run(name, func(t *testing.T) {
			pid := filepath.Join(t.TempDir(), "broker.pid")
			if err := os.WriteFile(pid, []byte("123\n"), 0600); err != nil {
				t.Fatal(err)
			}
			poll := config.Defaults.ShutdownPollInterval
			removed := make(chan error, 1)
			if remove {
				go func() { time.Sleep(poll); removed <- os.Remove(pid) }()
			}
			err := awaitEndpointRemoval(t.Context(), pid, poll, 3*poll)
			_, statErr := os.Lstat(pid)
			if remove {
				if removeErr := <-removed; removeErr != nil {
					t.Fatal(removeErr)
				}
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("retained PID reported stopped: %v", err)
			}
			if err == nil {
				if !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("success while PID exists: %v", statErr)
				}
			}
		})
	}
}

func TestAwaitEndpointRemovalAbsentAndCanceled(t *testing.T) {
	pid := filepath.Join(t.TempDir(), "broker.pid")
	if err := awaitEndpointRemoval(t.Context(), pid, config.Defaults.ShutdownPollInterval, config.Defaults.ShutdownTimeout); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pid, []byte("123\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := awaitEndpointRemoval(ctx, pid, config.Defaults.ShutdownPollInterval, config.Defaults.ShutdownTimeout); !errors.Is(err, context.Canceled) {
		t.Fatalf("ignored cancellation: %v", err)
	}
}
