package brokerstate

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
)

type processFixture struct {
	status ProcessStatus
	err    error
}

func (processFixture) Current(context.Context) (ProcessIdentity, error) {
	return ProcessIdentity{BootID: "test-boot", PID: 123, Start: "test-start"}, nil
}

func (p processFixture) Inspect(context.Context, ProcessIdentity) (ProcessStatus, error) {
	return p.status, p.err
}

func newOwner(t *testing.T) (*Owner, config.OwnershipConfig) {
	t.Helper()
	cfg := config.NewOwnershipConfig(filepath.Join(t.TempDir(), "state.db"), config.CreateStore)
	o, err := Acquire(t.Context(), cfg, processFixture{status: ProcessAlive})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := o.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown fixture owner: %v", err)
		}
	})
	return o, cfg
}

func write(o *Owner, change func(*WriteTx) error) error {
	return o.Do(context.Background(), func(op *Operation) error {
		return op.Write(context.Background(), change)
	})
}

func TestAcquireNeverCreatesMissingOpenStore(t *testing.T) {
	cfg := config.NewOwnershipConfig(filepath.Join(t.TempDir(), "missing.db"), config.OpenStore)
	if _, err := Acquire(t.Context(), cfg, processFixture{}); err == nil {
		t.Fatal("open silently initialized a missing database")
	}
}

func TestAcquireExcludesLiveAndUnverifiablePredecessors(t *testing.T) {
	o, cfg := newOwner(t)
	cfg.Action = config.OpenStore
	for _, fixture := range []processFixture{
		{status: ProcessAlive},
		{err: ErrIdentityUnavailable},
		{},
	} {
		if next, err := Acquire(t.Context(), cfg, fixture); err == nil {
			t.Fatalf("acquired competing owner: %#v", next)
		}
	}
	if err := write(o, func(tx *WriteTx) error { return tx.RequireActive() }); !errors.Is(err, ErrPrepared) {
		t.Fatalf("failed acquisitions changed prepared store or owner: %v", err)
	}
}

func TestOrderlyReleaseIncrementsGeneration(t *testing.T) {
	o, cfg := newOwner(t)
	old := o.state.generation
	if err := o.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	cfg.Action = config.OpenStore
	next, err := Acquire(t.Context(), cfg, processFixture{status: ProcessAlive})
	if err != nil {
		t.Fatal(err)
	}
	defer next.Shutdown(context.Background())
	if next.state.generation != old+1 {
		t.Fatalf("generation = %d, want %d", next.state.generation, old+1)
	}
	if err := o.Do(t.Context(), func(*Operation) error { t.Fatal("released owner admitted work"); return nil }); !errors.Is(err, ErrAdmissionClosed) {
		t.Fatalf("released owner Do = %v", err)
	}
}

func TestStaleOpenConnectionCannotWriteOrRelease(t *testing.T) {
	cfg := config.NewOwnershipConfig(filepath.Join(t.TempDir(), "state.db"), config.CreateStore)
	o, err := Acquire(t.Context(), cfg, processFixture{status: ProcessAlive})
	if err != nil {
		t.Fatal(err)
	}
	defer o.state.db.Close() // test-only stale-handle cleanup; cannot release a successor's row
	if err := write(o, func(tx *WriteTx) error { _, err := tx.Exec("CREATE TABLE evidence (value TEXT)"); return err }); err != nil {
		t.Fatal(err)
	}
	// Simulate the fault explicitly through an independent raw connection.
	db, err := sql.Open("sqlite", cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE broker_owner SET generation = generation + 1, instance_id = 'successor' WHERE singleton = 1"); err != nil {
		t.Fatal(err)
	}
	called := false
	err = write(o, func(tx *WriteTx) error {
		called = true
		_, err := tx.Exec("INSERT INTO evidence VALUES ('stale')")
		return err
	})
	if !errors.Is(err, ErrFenced) || called {
		t.Fatalf("stale write = %v, callback ran = %v", err, called)
	}
	if err := o.Shutdown(t.Context()); !errors.Is(err, ErrFenced) {
		t.Fatalf("stale release = %v", err)
	}
	var rows int
	if err := db.QueryRow("SELECT count(*) FROM evidence").Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("stale data: count=%d err=%v", rows, err)
	}
	var released sql.NullString
	if err := db.QueryRow("SELECT released_at FROM broker_owner").Scan(&released); err != nil || released.Valid {
		t.Fatalf("stale owner released successor: %v %v", released, err)
	}
}

func TestTransactionRollsBackEntireDomainChange(t *testing.T) {
	o, _ := newOwner(t)
	if err := write(o, func(tx *WriteTx) error { _, err := tx.Exec("CREATE TABLE domain_state (value INTEGER)"); return err }); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("dependency update failed")
	err := write(o, func(tx *WriteTx) error {
		if _, err := tx.Exec("INSERT INTO domain_state VALUES (1)"); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err := write(o, func(tx *WriteTx) error {
		var n int
		if err := tx.Scan("SELECT count(*) FROM domain_state", nil, &n); err != nil {
			return err
		}
		if n != 0 {
			t.Fatalf("partial transaction committed %d rows", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCapabilitiesExpireIncludingCopies(t *testing.T) {
	o, _ := newOwner(t)
	var retainedOp Operation
	var retainedTx WriteTx
	if err := o.Do(t.Context(), func(op *Operation) error {
		retainedOp = *op
		return op.Write(t.Context(), func(tx *WriteTx) error { retainedTx = *tx; return nil })
	}); err != nil {
		t.Fatal(err)
	}
	if err := retainedOp.Write(t.Context(), func(*WriteTx) error { t.Fatal("escaped operation used"); return nil }); !errors.Is(err, ErrExpiredCapability) {
		t.Fatal(err)
	}
	if _, err := retainedTx.Exec("SELECT 1"); !errors.Is(err, ErrExpiredCapability) {
		t.Fatal(err)
	}
	if err := retainedTx.Scan("SELECT 1", nil, new(int)); !errors.Is(err, ErrExpiredCapability) {
		t.Fatal(err)
	}
	if err := retainedTx.Query("SELECT 1", nil, func(*sql.Rows) error { t.Fatal("escaped query used"); return nil }); !errors.Is(err, ErrExpiredCapability) {
		t.Fatal(err)
	}
}

func TestConcurrentWritesUseSeparateTransactions(t *testing.T) {
	o, _ := newOwner(t)
	if err := write(o, func(tx *WriteTx) error { _, err := tx.Exec("CREATE TABLE writes (value INTEGER)"); return err }); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			err := write(o, func(tx *WriteTx) error { _, err := tx.Exec("INSERT INTO writes VALUES (?)", i); return err })
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if err := write(o, func(tx *WriteTx) error {
		var n int
		if err := tx.Scan("SELECT count(*) FROM writes", nil, &n); err != nil {
			return err
		}
		if n != 12 {
			t.Fatalf("committed rows = %d", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownWaiterTimeoutPreservesFinalization(t *testing.T) {
	closeFailure := errors.New("provider close failed")
	for _, tc := range []struct {
		name           string
		closeErr       error
		releaseFailure bool
	}{
		{name: "late successful close permits release"},
		{name: "late close error retains ownership", closeErr: closeFailure},
		{name: "late transaction error retains ownership", releaseFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			cfg := config.NewOwnershipConfig(filepath.Join(t.TempDir(), "state.db"), config.CreateStore)
			cfg.ShutdownTimeout = 50 * time.Millisecond
			o, err := Acquire(ctx, cfg, processFixture{status: ProcessAlive})
			if err != nil {
				t.Fatal(err)
			}
			defer o.state.db.Close() // Test cleanup also covers intentionally failed finalization.
			if tc.releaseFailure {
				if err := write(o, func(tx *WriteTx) error {
					_, err := tx.Exec(`CREATE TRIGGER deny_release BEFORE UPDATE OF released_at ON broker_owner
						BEGIN SELECT RAISE(ABORT, 'release denied by test'); END`)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			closing, join := make(chan struct{}), make(chan struct{})
			release := sync.OnceFunc(func() { close(join) })
			defer release()
			var closes atomic.Int32
			workDone := make(chan error, 1)
			go func() {
				workDone <- o.Do(ctx, func(op *Operation) error {
					_, err := op.Open(ctx, func() (io.Closer, error) {
						return closeFunc(func() error {
							closes.Add(1)
							close(closing)
							<-join
							return tc.closeErr
						}), nil
					})
					return err
				})
			}()
			select {
			case <-closing:
			case <-ctx.Done():
				t.Fatal("resource did not reach close")
			}
			if err := o.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("unjoined resource must produce a bounded wait error: %v", err)
			}
			if err := o.Do(ctx, func(*Operation) error { return nil }); !errors.Is(err, ErrAdmissionClosed) {
				t.Fatalf("timeout reopened admission: %v", err)
			}
			cfg.Action = config.OpenStore
			if next, err := Acquire(ctx, cfg, processFixture{status: ProcessAlive}); !errors.Is(err, ErrOwnerAlive) {
				if next != nil {
					_ = next.Shutdown(ctx)
				}
				t.Fatalf("unjoined resource allowed takeover: %v", err)
			}
			release()
			select {
			case err := <-workDone:
				if !errors.Is(err, tc.closeErr) {
					t.Fatalf("resource close result: %v, want %v", err, tc.closeErr)
				}
			case <-ctx.Done():
				t.Fatal("resource close did not join")
			}
			// Finalization must finish without a second shutdown request to restart it.
			select {
			case <-o.state.shutdownDone:
			case <-ctx.Done():
				t.Fatal("finalization did not finish after the resource joined")
			}
			finalErr := o.Shutdown(ctx)
			if tc.closeErr == nil && !tc.releaseFailure {
				if finalErr != nil {
					t.Fatalf("waiter timeout permanently abandoned finalization: %v", finalErr)
				}
				next, err := Acquire(ctx, cfg, processFixture{status: ProcessAlive})
				if err != nil {
					t.Fatalf("completed finalization did not permit a successor: %v", err)
				}
				if err := next.Shutdown(ctx); err != nil {
					t.Fatal(err)
				}
				return
			}
			if finalErr == nil || errors.Is(finalErr, context.DeadlineExceeded) {
				t.Fatalf("actual finalization failure was lost behind waiter timeout: %v", finalErr)
			}
			if tc.closeErr != nil && !errors.Is(finalErr, tc.closeErr) {
				t.Fatalf("resource error was not preserved: %v", finalErr)
			}
			if tc.releaseFailure {
				if !strings.Contains(finalErr.Error(), "release denied by test") {
					t.Fatalf("release error was not preserved: %v", finalErr)
				}
				// Removing the injected fault must not cause a later waiter to replay SQL.
				if _, err := o.state.db.Exec("DROP TRIGGER deny_release"); err != nil {
					t.Fatal(err)
				}
			}
			if err := o.Shutdown(ctx); err != finalErr || closes.Load() != 1 {
				t.Fatalf("waiter retried failed finalization: err=%v closes=%d", err, closes.Load())
			}
			if next, err := Acquire(ctx, cfg, processFixture{status: ProcessAlive}); !errors.Is(err, ErrOwnerAlive) {
				if next != nil {
					_ = next.Shutdown(ctx)
				}
				t.Fatalf("failed finalization allowed takeover: %v", err)
			}
		})
	}
}
