package brokerstate

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/seungpyoson/waggle/internal/config"
)

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
	ctx, cancel := context.WithCancel(context.Background())
	shutdown := make(chan error, 1)
	go func() { shutdown <- o.Shutdown(ctx) }()
	<-o.state.stopping
	select {
	case <-o.state.drained:
		t.Error("drained while work still running")
	default:
	}
	cancel()
	if err := <-shutdown; !errors.Is(err, context.Canceled) {
		t.Error(err)
	}
	if err := o.Do(t.Context(), func(*Operation) error { t.Error("admitted during shutdown"); return nil }); !errors.Is(err, ErrAdmissionClosed) {
		t.Error(err)
	}
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := o.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	cfg.Action = config.OpenStore
	next, err := Acquire(t.Context(), cfg, processFixture{status: ProcessAlive})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := next.Shutdown(context.Background()); err != nil {
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
			if err := o.Shutdown(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if o.state.phase != released {
		t.Fatal("shutdown did not release ownership")
	}
}
