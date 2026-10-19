package brokerstate

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/seungpyoson/waggle/internal/config"
)

// Adapted adversarial Probe D: final failure cannot abandon an admitted write,
// but an unresolved registered closer must not prevent reporting that failure.
func TestFatalFinalizationWaitsForAdmittedOperation(t *testing.T) {
	o, err := Acquire(t.Context(), config.NewOwnershipConfig(filepath.Join(t.TempDir(), "state.db"), config.CreateStore), processFixture{status: ProcessAlive})
	if err != nil {
		t.Fatal(err)
	}
	defer o.state.db.Close()
	ctx, cancel := context.WithTimeout(t.Context(), config.Defaults.StartupTimeout)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- o.Do(ctx, func(op *Operation) error {
			close(entered)
			<-release
			return op.Write(context.WithoutCancel(ctx), func(tx *WriteTx) error {
				_, err := tx.Exec("CREATE TABLE final_outcome (value TEXT); INSERT INTO final_outcome VALUES ('accepted')")
				return err
			})
		})
	}()
	<-entered
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	defer finish()
	closer, closerDone := make(chan struct{}), make(chan struct{})
	fatal := errors.New("unresolved close")
	if err := o.StartWork(ctx, "failed-closer", ManagedWork{Run: func(_ WorkLifetime, report func(error)) {
		defer close(closerDone)
		report(fatal)
		<-closer
	}}); err != nil {
		t.Fatal(err)
	}
	defer func() { close(closer); <-closerDone }()
	select {
	case <-o.Draining():
	case <-ctx.Done():
		t.Fatal("fatal did not drain")
	}
	short, stop := context.WithTimeout(ctx, config.Defaults.ShutdownPollInterval)
	defer stop()
	if err := o.Wait(short); !errors.Is(err, ErrShutdownIncomplete) {
		t.Errorf("Wait abandoned admitted operation: %v", err)
	}
	finish()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := o.Wait(ctx); !errors.Is(err, ErrFinalizationFailed) || !errors.Is(err, fatal) {
		t.Fatal(err)
	}
	var outcome string
	if err := o.state.db.QueryRow("SELECT value FROM final_outcome").Scan(&outcome); err != nil || outcome != "accepted" {
		t.Fatalf("final result missing: %q %v", outcome, err)
	}
}

func TestAdmissionRejectsZeroAndExpiredCapabilities(t *testing.T) {
	var zero Owner
	if err := zero.Do(t.Context(), func(*Operation) error { return nil }); !errors.Is(err, ErrExpiredCapability) {
		t.Fatal(err)
	}
	o, _ := newOwner(t)
	var escaped Operation
	if err := o.Do(t.Context(), func(op *Operation) error { escaped = *op; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := escaped.Write(t.Context(), func(*WriteTx) error { t.Fatal("expired callback invoked"); return nil }); !errors.Is(err, ErrExpiredCapability) {
		t.Fatal(err)
	}
	var tx WriteTx
	if _, err := tx.Exec("SELECT 1"); !errors.Is(err, ErrExpiredCapability) {
		t.Fatal(err)
	}
}

func TestAdmissionDrainIncludesWorkAdmittedBeforeShutdown(t *testing.T) {
	o, cfg := newOwner(t)
	copyOfOwner := *o
	entered, finish, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- copyOfOwner.Do(context.Background(), func(op *Operation) error {
			close(entered)
			<-finish
			return op.Write(context.Background(), func(tx *WriteTx) error {
				_, err := tx.Exec("CREATE TABLE cleanup_evidence (finished INTEGER); INSERT INTO cleanup_evidence VALUES (1)")
				return err
			})
		})
	}()
	<-entered
	o.BeginShutdown(nil)
	<-o.Draining()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := o.Wait(ctx); !errors.Is(err, context.Canceled) || !errors.Is(err, ErrShutdownIncomplete) {
		t.Error(err)
	}
	if err := o.Do(t.Context(), func(*Operation) error { t.Error("admitted during shutdown"); return nil }); !errors.Is(err, ErrAdmissionClosed) {
		t.Error(err)
	}
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := o.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	cfg.Action = config.OpenStore
	next, err := Acquire(t.Context(), cfg, processFixture{status: ProcessAlive})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		next.BeginShutdown(nil)
		if err := next.Wait(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	var finished int
	if err := write(next, func(tx *WriteTx) error {
		return tx.Scan("SELECT finished FROM cleanup_evidence", nil, &finished)
	}); err != nil || finished != 1 {
		t.Fatalf("cleanup was not committed before ownership release: finished=%d err=%v", finished, err)
	}
}

func TestConcurrentShutdownClosesAdmissionOnlyOnce(t *testing.T) {
	o, _ := newOwner(t)
	var wg sync.WaitGroup
	for range 24 {
		wg.Go(func() {
			o.BeginShutdown(nil)
			if err := o.Wait(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if o.state.phase != released {
		t.Fatal("shutdown did not release ownership")
	}
}
