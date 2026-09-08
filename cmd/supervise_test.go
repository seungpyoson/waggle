package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/brokerstate/statetest"
	"github.com/seungpyoson/waggle/internal/config"
)

// syncBuffer is the supervisor's report sink for tests that poll the report
// while the supervisor is still writing it. The supervisor writes from its own
// goroutine, so an unguarded bytes.Buffer would be a data race in the test, not
// in the code under test.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func superviseOwner(t *testing.T) *brokerstate.Owner {
	t.Helper()
	o, err := brokerstate.Acquire(t.Context(), config.NewOwnershipConfig(filepath.Join(t.TempDir(), "state.db"), config.CreateStore), statetest.Process{})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestSuperviseReportsDeadlineThenEventualRelease(t *testing.T) {
	o := superviseOwner(t)
	join := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(join) }) }
	defer release()
	if err := o.StartWork(t.Context(), "stalled", brokerstate.ManagedWork{Run: func(lifetime brokerstate.WorkLifetime, _ func(error)) {
		<-lifetime.Stop
		<-join
	}}); err != nil {
		t.Fatal(err)
	}
	serve := func(ctx context.Context) error { <-o.Draining(); return nil }
	signals := make(chan os.Signal, 2)
	var report syncBuffer
	done := make(chan error, 1)
	go func() { done <- supervise(t.Context(), signals, serve, o, 50*time.Millisecond, &report) }()
	signals <- syscall.SIGTERM
	deadline := time.After(config.Defaults.StartupTimeout)
	for !strings.Contains(report.String(), "shutdown deadline missed") {
		select {
		case <-deadline:
			t.Fatalf("deadline was not reported while draining: %q", report.String())
		case err := <-done:
			t.Fatalf("supervisor returned before finalization: %v", err)
		case <-time.After(config.Defaults.ShutdownPollInterval):
		}
	}
	release()
	err := <-done
	if !errors.Is(err, ErrShutdownDeadlineMissed) {
		t.Fatalf("missed deadline must be part of the exit result: %v", err)
	}
	if !strings.Contains(report.String(), "released") {
		t.Fatalf("eventual release not reported: %q", report.String())
	}
	if errors.Is(err, brokerstate.ErrFinalizationFailed) {
		t.Fatalf("clean late release reported as failure: %v", err)
	}
}

func TestSuperviseSecondSignalInterruptsAdmittedNativeCall(t *testing.T) {
	o := superviseOwner(t)
	if err := o.StartWork(t.Context(), "native", brokerstate.ManagedWork{Run: func(lifetime brokerstate.WorkLifetime, _ func(error)) {
		<-lifetime.Stop
		<-lifetime.Interrupt.Done() // an admitted native call that only escalation cancels
	}}); err != nil {
		t.Fatal(err)
	}
	serve := func(ctx context.Context) error { <-o.Draining(); return nil }
	signals := make(chan os.Signal, 2)
	var report bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- supervise(t.Context(), signals, serve, o, time.Second, &report) }()
	signals <- syscall.SIGINT
	time.Sleep(config.Defaults.ShutdownPollInterval)
	select {
	case err := <-done:
		t.Fatalf("orderly draining cancelled native I/O: %v", err)
	default:
	}
	signals <- syscall.SIGINT
	err := <-done
	if err == nil || !strings.Contains(err.Error(), "escalated") {
		t.Fatalf("escalation not reported: %v", err)
	}
	if errors.Is(err, ErrShutdownDeadlineMissed) || errors.Is(err, brokerstate.ErrFinalizationFailed) {
		t.Fatalf("escalated but clean release misreported: %v", err)
	}
}

// A reported deadline is not a new lifecycle: the next signal is still the
// second one, so it escalates instead of restarting orderly draining.
func TestSuperviseEscalatesOnTheSignalAfterAMissedDeadline(t *testing.T) {
	o := superviseOwner(t)
	if err := o.StartWork(t.Context(), "native", brokerstate.ManagedWork{Run: func(lifetime brokerstate.WorkLifetime, _ func(error)) {
		<-lifetime.Stop
		<-lifetime.Interrupt.Done()
	}}); err != nil {
		t.Fatal(err)
	}
	serve := func(ctx context.Context) error { <-o.Draining(); return nil }
	signals := make(chan os.Signal, 2)
	var report syncBuffer
	done := make(chan error, 1)
	go func() { done <- supervise(t.Context(), signals, serve, o, 50*time.Millisecond, &report) }()
	signals <- syscall.SIGTERM
	deadline := time.After(config.Defaults.StartupTimeout)
	for !strings.Contains(report.String(), "shutdown deadline missed") {
		select {
		case <-deadline:
			t.Fatalf("deadline was not reported while draining: %q", report.String())
		case err := <-done:
			t.Fatalf("supervisor returned before finalization: %v", err)
		case <-time.After(config.Defaults.ShutdownPollInterval):
		}
	}
	signals <- syscall.SIGINT
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "escalated") {
			t.Fatalf("signal after a missed deadline did not escalate: %v", err)
		}
		if !errors.Is(err, ErrShutdownDeadlineMissed) {
			t.Fatalf("missed deadline dropped from the exit result: %v", err)
		}
	case <-time.After(config.Defaults.StartupTimeout):
		t.Fatalf("signal after a missed deadline never interrupted native I/O: %q", report.String())
	}
}

// Cancelling the parent context is a shutdown trigger, not a silent no-op: the
// supervisor begins orderly draining once and returns the normal release result.
func TestSuperviseBeginsShutdownWhenTheParentContextIsCancelled(t *testing.T) {
	o := superviseOwner(t)
	ctx, cancel := context.WithCancel(t.Context())
	serve := func(context.Context) error { <-o.Draining(); return nil }
	done := make(chan error, 1)
	go func() {
		done <- supervise(ctx, make(chan os.Signal, 2), serve, o, config.Defaults.ShutdownTimeout, &syncBuffer{})
	}()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("parent cancellation did not release cleanly: %v", err)
		}
	case <-time.After(config.Defaults.StartupTimeout):
		t.Fatal("parent cancellation never began shutdown")
	}
	if err := o.Wait(t.Context()); err != nil {
		t.Fatalf("ownership retained after parent cancellation: %v", err)
	}
}

// A canonical store that was released but whose local handle did not close is
// not retained ownership. The command warns and still exits successfully;
// anything else keeps its exit failure.
func TestReleasedStoreCloseFailureIsAWarningNotAnExitFailure(t *testing.T) {
	local := errors.New("canonical store handle did not close")
	var report syncBuffer
	released := fmt.Errorf("%w: %w", brokerstate.ErrStoreCloseFailed, local)
	if err := releaseOutcome(released, &report); err != nil {
		t.Fatalf("released ownership reported as an exit failure: %v", err)
	}
	if !strings.Contains(report.String(), local.Error()) {
		t.Fatalf("close failure was not warned about: %q", report.String())
	}
	retained := fmt.Errorf("%w: %w", brokerstate.ErrFinalizationFailed, local)
	if err := releaseOutcome(retained, &report); !errors.Is(err, brokerstate.ErrFinalizationFailed) {
		t.Fatalf("retained ownership was downgraded to a warning: %v", err)
	}
}

func TestSupervisePermanentFailureExitsWithoutRelease(t *testing.T) {
	o := superviseOwner(t)
	fatal := errors.New("canonical persistence failed")
	if err := o.StartWork(t.Context(), "w", brokerstate.ManagedWork{Run: func(lifetime brokerstate.WorkLifetime, reportFatal func(error)) {
		reportFatal(fatal)
		<-lifetime.Interrupt.Done()
	}}); err != nil {
		t.Fatal(err)
	}
	serve := func(ctx context.Context) error { <-o.Draining(); return nil }
	err := supervise(t.Context(), make(chan os.Signal, 1), serve, o, time.Second, &bytes.Buffer{})
	if !errors.Is(err, brokerstate.ErrFinalizationFailed) || !errors.Is(err, fatal) {
		t.Fatalf("permanent failure not reported: %v", err)
	}
	if err := o.Wait(t.Context()); !errors.Is(err, brokerstate.ErrFinalizationFailed) {
		t.Fatalf("ownership was released after a permanent failure: %v", err)
	}
}
