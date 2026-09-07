package brokerstate

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/seungpyoson/waggle/internal/config"
)

// Adapted from adversarial Probes B/C. Every Bind uses the real PID written by
// createEndpoint. Only the bind-less maintenance process has a different PID.
type bindingProcess struct {
	pid     int
	inspect func(ProcessIdentity) (ProcessStatus, error)
}

func (f bindingProcess) Current(context.Context) (ProcessIdentity, error) {
	return ProcessIdentity{BootID: "binding-boot", PID: f.pid, Start: "binding-start"}, nil
}
func (f bindingProcess) Inspect(_ context.Context, p ProcessIdentity) (ProcessStatus, error) {
	if f.inspect != nil {
		return f.inspect(p)
	}
	if p.PID == f.pid {
		return ProcessAlive, nil
	}
	return ProcessExited, nil
}

func crashedBinding(t *testing.T) (config.OwnershipConfig, config.BrokerEndpoints) {
	t.Helper()
	cfg := config.NewOwnershipConfig(filepath.Join(t.TempDir(), "state.db"), config.CreateStore)
	socket, pid := endpointPaths(t)
	paths := config.BrokerEndpoints{Socket: socket, PID: pid}
	o, err := Acquire(t.Context(), cfg, bindingProcess{pid: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	activateFixture(t, o)
	if err := o.Bind(t.Context(), paths); err != nil {
		t.Fatal(err)
	}
	// Model process death: close the handles without orderly release or unlink.
	if err := o.state.endpoint.listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := o.state.db.Close(); err != nil {
		t.Fatal(err)
	}
	cfg.Action = config.OpenStore
	return cfg, paths
}

func releaseBindingOwner(t *testing.T, o *Owner) {
	t.Helper()
	o.BeginShutdown(nil)
	ctx, cancel := context.WithTimeout(t.Context(), config.Defaults.StartupTimeout)
	defer cancel()
	if err := o.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestReclaimRecordedBindingAfterBindlessGenerations(t *testing.T) {
	for _, transient := range []bool{false, true} {
		name := "maintenance"
		if transient {
			name = "transient inspection failure"
		}
		t.Run(name, func(t *testing.T) {
			cfg, paths := crashedBinding(t)
			calls := 0
			maintenance := bindingProcess{pid: os.Getpid() + 100000, inspect: func(p ProcessIdentity) (ProcessStatus, error) {
				calls++
				if transient && calls == 2 {
					return 0, ErrIdentityUnavailable
				}
				return ProcessExited, nil
			}}
			for range 2 {
				o, err := Acquire(t.Context(), cfg, maintenance)
				if err != nil {
					t.Fatal(err)
				}
				if transient && calls == 1 {
					if err := o.Bind(t.Context(), paths); !errors.Is(err, ErrIdentityUnavailable) {
						t.Fatalf("expected transient inspection failure: %v", err)
					}
				}
				releaseBindingOwner(t, o)
			}
			inspected := 0
			next, err := Acquire(t.Context(), cfg, bindingProcess{pid: os.Getpid(), inspect: func(p ProcessIdentity) (ProcessStatus, error) {
				inspected++
				if p.PID != os.Getpid() {
					t.Errorf("reclaim inspected maintenance PID %d", p.PID)
				}
				return ProcessExited, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer releaseBindingOwner(t, next)
			if err := next.Bind(t.Context(), paths); err != nil {
				t.Fatalf("bind-less generations poisoned reclaim: %v", err)
			}
			if inspected != 1 {
				t.Fatalf("binding owner inspected %d times", inspected)
			}
		})
	}
}

func TestReclaimRejectsLiveBindingAndPIDMismatch(t *testing.T) {
	for _, alive := range []bool{false, true} {
		name := "PID mismatch"
		if alive {
			name = "binding owner still alive"
		}
		t.Run(name, func(t *testing.T) {
			cfg, paths := crashedBinding(t)
			maintenance, err := Acquire(t.Context(), cfg, bindingProcess{pid: os.Getpid() + 100000})
			if err != nil {
				t.Fatal(err)
			}
			releaseBindingOwner(t, maintenance)
			if !alive {
				if err := os.WriteFile(paths.PID, []byte("1\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(paths.PID)
			if err != nil {
				t.Fatal(err)
			}
			next, err := Acquire(t.Context(), cfg, bindingProcess{pid: os.Getpid(), inspect: func(ProcessIdentity) (ProcessStatus, error) {
				if alive {
					return ProcessAlive, nil
				}
				return ProcessExited, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer releaseBindingOwner(t, next)
			if err := next.Bind(t.Context(), paths); err == nil {
				t.Fatal("unsafe endpoint reclaim accepted")
			}
			after, err := os.ReadFile(paths.PID)
			if err != nil || string(after) != string(before) {
				t.Fatalf("refused reclaim changed PID file: %s %v", after, err)
			}
		})
	}
}

func TestBindingRowWithoutFilesIsHarmless(t *testing.T) {
	cfg, paths := crashedBinding(t)
	for _, path := range []string{paths.Socket, paths.PID} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	next, err := Acquire(t.Context(), cfg, bindingProcess{pid: os.Getpid(), inspect: func(ProcessIdentity) (ProcessStatus, error) { return ProcessExited, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer releaseBindingOwner(t, next)
	if err := next.Bind(t.Context(), paths); err != nil {
		t.Fatal(err)
	}
}

func TestBindingRecordIsDurableBeforePublicationAndDeletedOnRelease(t *testing.T) {
	o, cfg := newOwner(t)
	activateFixture(t, o)
	socket, pid := endpointPaths(t)
	paths := config.BrokerEndpoints{Socket: socket, PID: pid}
	raw, err := sql.Open("sqlite", "file:"+cfg.Database+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	o.state.beforePublish = func() {
		var identity string
		var generation int64
		if err := raw.QueryRow("SELECT instance_id, generation FROM endpoint_binding").Scan(&identity, &generation); err != nil {
			t.Fatal(err)
		}
		if identity != o.state.identity || generation != o.state.generation {
			t.Fatal("files published without durable binding owner")
		}
		// This constructor has files but has not published its endpoint yet.
		if err := o.Bind(t.Context(), paths); err == nil {
			t.Fatal("competing Bind overwrote constructor provenance")
		}
	}
	if err := o.Bind(t.Context(), paths); err != nil {
		t.Fatal(err)
	}
	releaseBindingOwner(t, o)
	var count int
	if err := raw.QueryRow("SELECT count(*) FROM endpoint_binding").Scan(&count); err != nil || count != 0 {
		t.Fatalf("released endpoint retained binding: %d %v", count, err)
	}
}

func TestBindingPersistenceFailureCreatesNoFiles(t *testing.T) {
	o, _ := newOwner(t)
	activateFixture(t, o)
	socket, pid := endpointPaths(t)
	if err := write(o, func(tx *WriteTx) error {
		_, err := tx.Exec(`CREATE TRIGGER reject_binding BEFORE INSERT ON endpoint_binding BEGIN SELECT RAISE(ABORT, 'binding denied'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := o.Bind(t.Context(), config.BrokerEndpoints{Socket: socket, PID: pid}); err == nil {
		t.Fatal("Bind ignored failed provenance commit")
	}
	for _, path := range []string{socket, pid} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("file created before binding commit: %s %v", path, err)
		}
	}
}

func TestLosingBindRetainsOwnershipWhenBindingDeleteFails(t *testing.T) {
	cfg := config.NewOwnershipConfig(filepath.Join(t.TempDir(), "state.db"), config.CreateStore)
	o, err := Acquire(t.Context(), cfg, bindingProcess{pid: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	defer o.state.db.Close()
	activateFixture(t, o)
	if err := write(o, func(tx *WriteTx) error {
		_, err := tx.Exec(`CREATE TRIGGER reject_binding_delete BEFORE DELETE ON endpoint_binding BEGIN SELECT RAISE(ABORT, 'binding delete denied'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	socket, pid := endpointPaths(t)
	o.state.beforePublish = func() { o.BeginShutdown(nil) }
	if err := o.Bind(t.Context(), config.BrokerEndpoints{Socket: socket, PID: pid}); !errors.Is(err, ErrAdmissionClosed) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), config.Defaults.StartupTimeout)
	defer cancel()
	if err := o.Wait(ctx); !errors.Is(err, ErrFinalizationFailed) {
		t.Fatalf("failed provenance deletion released owner: %v", err)
	}
	var count int
	if err := o.state.db.QueryRow("SELECT count(*) FROM endpoint_binding").Scan(&count); err != nil || count != 1 {
		t.Fatalf("failed deletion lost provenance: %d %v", count, err)
	}
	for _, path := range []string{socket, pid} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("losing Bind failed to discard %s: %v", path, err)
		}
	}
}
