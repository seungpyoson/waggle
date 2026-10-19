# Native Messaging Unit 1: Owner Lifecycle, Enrollment Workers, Command Supervision — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the single global scheduler, the deadline-bound finalizer and the Serve-then-shutdown command sequence with owner-registered enrollment workers, a deadline-free finalizer with bounded waiters, and a command supervisor that reports at the deadline while draining continues.

**Architecture:** `brokerstate.Owner` gains `StartWork`, `BeginShutdown`, `Interrupt`, `Wait`; resources (`Resource`, `Operation.Open/Use`) are deleted. A new `internal/broker/enrollment` package owns one worker per observable enrollment; each worker privately owns its transport, observes readiness, selects at most one message for its recipient, submits, and persists the outcome through `Owner.Do`. Provider mechanics move under `internal/broker/enrollment/internal/`. `cmd/start.go` uses a supervisor that selects on ingress completion, the shutdown deadline, a second signal, and finalization.

**Tech Stack:** Go 1.26, modernc.org/sqlite, coder/websocket, cobra. Tests use the standard `testing` package with `-race`.

**Spec:** `docs/native-messaging-plan.md` (revision 6, lines 67-144 "Exclusive broker ownership", "Managed workers, operation admission, and finalization", "Reservation and scheduling contract") and the assessment at `/Volumes/Dev-OWC-Envoy/waggle-native-builds/work/build--native-messaging/reviews/independent-assessment-20260907/assessment.md` (findings F1-F8, Unit 1).

## Global Constraints

- Zero hardcodes: every interval, deadline and limit lives in `internal/config/`.
- One way to do each thing: no fallback branch, no compatibility shim, no second queue/readiness ledger. Delete what a replacement supersedes in the same task.
- Fail loud: errors propagate with context. Native probe/transport failures are observations (non-fatal); canonical persistence failures and unresolved close errors are fatal and retain ownership.
- Never block the host: `--help` for every command must work from a non-repository directory with no broker, Git or network.
- Native-call contexts derive from `WorkLifetime.Interrupt` plus the configured request deadline, never from `Stop`, a caller's command context, or a waiter deadline.
- A committed intent is submitted at most once; nothing in this unit adds a retry, second attempt or resubmission.
- Build/test environment: run every Go command with
  `export GOCACHE=/Volumes/Dev-OWC-Envoy/waggle-native-builds/work/build--native-messaging/reviews/independent-assessment-20260907/gocache GOTMPDIR=/Volumes/Dev-OWC-Envoy/waggle-native-builds/work/build--native-messaging/reviews/independent-assessment-20260907/gotmp TMPDIR=/Volumes/Dev-OWC-Envoy/waggle-native-builds/work/build--native-messaging/reviews/independent-assessment-20260907/gotmp GOPROXY=off GOFLAGS=-mod=mod`
  from the checkout `/Users/spson/Projects/Claude/waggle`. Broker socket tests create sockets under the package directory (see `shortBrokerSocketPath`); the checkout path is short enough, Dev Envoy paths are not.
- Do not touch `.worktrees/codex-native`, `.tmp-envoy`, installed integrations, real provider sessions, or user data.
- Commit after each task with a conventional message and the trailer
  `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` and `Claude-Session: https://claude.ai/code/session_01HGU6FAQiaEWgLV3A2dRf6P`. Never `git add -A`; add named paths so `.tmp-envoy` and scratch never enter the index.
- Tasks 1-3 change a shared API. Package-level tests must pass at each commit; the whole-module `go build ./...` is required green again at the end of Task 3 and at every later task.

---

### Task 0: Checkpoint the assessed working tree

**Files:**
- Commit every currently modified, deleted and untracked path listed by `git status --porcelain --untracked-files=all` except `.tmp-envoy`.

- [ ] **Step 1: Confirm the tree matches the assessed manifest**

Run: `python3 /Volumes/Dev-OWC-Envoy/waggle-native-builds/work/build--native-messaging/reviews/independent-assessment-20260907/drift_check.py`
Expected: `TOTAL_DRIFT 0`.

- [ ] **Step 2: Stage everything except the scratch symlink**

```bash
cd /Users/spson/Projects/Claude/waggle
git add -A -- . ':(exclude).tmp-envoy'
git status --porcelain | grep -c . 
git ls-files --error-unmatch .tmp-envoy 2>/dev/null && echo "ERROR: .tmp-envoy staged" 
```
Expected: the second command prints a count; the third prints nothing (the symlink is not tracked).

- [ ] **Step 3: Commit the checkpoint**

```bash
git commit -m "wip: native messaging working tree as assessed (handoff-986wfmwv)

Checkpoint of the stopped implementation before Unit 1. Contains the
intentionally failing TestShutdownWaiterTimeoutPreservesFinalization.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HGU6FAQiaEWgLV3A2dRf6P"
```

- [ ] **Step 4: Add this plan and commit**

```bash
git add docs/superpowers/plans/2026-09-07-native-messaging-unit1-lifecycle-workers.md
git commit -m "docs: add Unit 1 lifecycle/workers implementation plan

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HGU6FAQiaEWgLV3A2dRf6P"
```

---

### Task 1: Owner lifecycle API (StartWork, BeginShutdown, Interrupt, Wait)

**Files:**
- Modify: `internal/brokerstate/owner.go` (full rewrite of lifecycle; keep `Do`, `Operation`, error values)
- Modify: `internal/brokerstate/acquire.go:182-186` (constructor fields)
- Modify: `internal/brokerstate/endpoint.go` (Bind without the lifecycle mutex across I/O; per-file removal progress; `Draining` accessor used by Serve)
- Delete: `internal/brokerstate/resource.go`, `internal/brokerstate/resource_test.go`
- Modify: `internal/brokerstate/owner_test.go`, `internal/brokerstate/admission_test.go`, `internal/brokerstate/endpoint_test.go`, `internal/brokerstate/statetest/store.go`

**Interfaces:**
- Consumes: existing `Acquire`, `transaction`, `immediate`, `WriteTx`.
- Produces (used by Tasks 3 and 4):
  ```go
  type WorkLifetime struct { Stop <-chan struct{}; Interrupt context.Context }
  type ManagedWork struct { Run func(lifetime WorkLifetime, reportFatal func(error)); Complete func(tx *WriteTx) error }
  func (o *Owner) StartWork(ctx context.Context, key string, work ManagedWork) error // ErrAdmissionClosed | ErrDuplicateWork
  func (o *Owner) Do(ctx context.Context, work func(*Operation) error) error        // unchanged
  func (o *Owner) BeginShutdown(cause error)                                        // idempotent
  func (o *Owner) Interrupt(cause error)                                            // BeginShutdown + cancel Interrupt ctx
  func (o *Owner) Wait(ctx context.Context) error                                   // nil = released; ErrFinalizationFailed | ErrShutdownIncomplete(ctx)
  func (o *Owner) Draining() <-chan struct{}                                        // closed when BeginShutdown ran
  func (o *Owner) Bind(ctx context.Context, paths config.BrokerEndpoints) error     // unchanged signature
  func (o *Owner) Serve(ctx context.Context, handle func(net.Conn)) error           // unchanged signature
  var ErrDuplicateWork, ErrFinalizationFailed, ErrShutdownIncomplete error
  ```
- Deleted: `Owner.Shutdown`, `Owner.Stopping`, `Resource`, `Operation.Open`, `Resource.Use`, `Resource.Close`, `operationState.resources`, `ownedEndpoint.ready`, `config.OwnershipConfig.ShutdownTimeout` stays but is no longer read by the owner (the command uses it as its waiter deadline in Task 4).

- [ ] **Step 1: Write the failing lifecycle tests**

Replace `TestShutdownWaiterTimeoutPreservesFinalization` in `internal/brokerstate/owner_test.go` with the following tests. Keep every other existing test in the file unchanged except `newOwner` cleanup and the `write` helper (shown).

```go
func newOwner(t *testing.T) (*Owner, config.OwnershipConfig) {
	t.Helper()
	cfg := config.NewOwnershipConfig(filepath.Join(t.TempDir(), "state.db"), config.CreateStore)
	o, err := Acquire(t.Context(), cfg, processFixture{status: ProcessAlive})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		o.BeginShutdown(nil)
		if err := o.Wait(context.Background()); err != nil {
			t.Errorf("release fixture owner: %v", err)
		}
	})
	return o, cfg
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
			err = o.StartWork(ctx, "worker", ManagedWork{Run: func(lifetime WorkLifetime, reportFatal func(error)) {
				<-lifetime.Stop
				closes.Add(1)
				close(closing)
				<-join
				if tc.closeErr != nil {
					reportFatal(tc.closeErr)
				}
			}})
			if err != nil {
				t.Fatal(err)
			}
			o.BeginShutdown(errors.New("test stop"))
			select {
			case <-closing:
			case <-ctx.Done():
				t.Fatal("worker did not observe Stop")
			}
			waitCtx, cancelWait := context.WithTimeout(ctx, 50*time.Millisecond)
			err = o.Wait(waitCtx)
			cancelWait()
			if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrShutdownIncomplete) {
				t.Fatalf("unjoined worker must produce a bounded wait error: %v", err)
			}
			if err := o.Do(ctx, func(*Operation) error { return nil }); !errors.Is(err, ErrAdmissionClosed) {
				t.Fatalf("timeout reopened admission: %v", err)
			}
			cfg.Action = config.OpenStore
			if next, err := Acquire(ctx, cfg, processFixture{status: ProcessAlive}); !errors.Is(err, ErrOwnerAlive) {
				if next != nil {
					next.BeginShutdown(nil)
					_ = next.Wait(ctx)
				}
				t.Fatalf("unjoined worker allowed takeover: %v", err)
			}
			release()
			finalErr := o.Wait(ctx)
			if tc.closeErr == nil && !tc.releaseFailure {
				if finalErr != nil {
					t.Fatalf("waiter timeout permanently abandoned finalization: %v", finalErr)
				}
				next, err := Acquire(ctx, cfg, processFixture{status: ProcessAlive})
				if err != nil {
					t.Fatalf("completed finalization did not permit a successor: %v", err)
				}
				next.BeginShutdown(nil)
				if err := next.Wait(ctx); err != nil {
					t.Fatal(err)
				}
				return
			}
			if finalErr == nil || errors.Is(finalErr, context.DeadlineExceeded) || !errors.Is(finalErr, ErrFinalizationFailed) {
				t.Fatalf("actual finalization failure was lost behind waiter timeout: %v", finalErr)
			}
			if tc.closeErr != nil && !errors.Is(finalErr, tc.closeErr) {
				t.Fatalf("worker error was not preserved: %v", finalErr)
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
			if err := o.Wait(ctx); !errors.Is(err, ErrFinalizationFailed) || closes.Load() != 1 {
				t.Fatalf("waiter retried failed finalization: err=%v closes=%d", err, closes.Load())
			}
			if next, err := Acquire(ctx, cfg, processFixture{status: ProcessAlive}); !errors.Is(err, ErrOwnerAlive) {
				if next != nil {
					next.BeginShutdown(nil)
					_ = next.Wait(ctx)
				}
				t.Fatalf("failed finalization allowed takeover: %v", err)
			}
		})
	}
}

func TestStartWorkRejectsDuplicatesAndLosesToDraining(t *testing.T) {
	o, _ := newOwner(t)
	started := make(chan struct{})
	run := func(lifetime WorkLifetime, _ func(error)) { close(started); <-lifetime.Stop }
	if err := o.StartWork(t.Context(), "a", ManagedWork{Run: run}); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := o.StartWork(t.Context(), "a", ManagedWork{Run: func(WorkLifetime, func(error)) { t.Error("duplicate worker ran") }}); !errors.Is(err, ErrDuplicateWork) {
		t.Fatalf("duplicate registration: %v", err)
	}
	o.BeginShutdown(nil)
	if err := o.StartWork(t.Context(), "b", ManagedWork{Run: func(WorkLifetime, func(error)) { t.Error("worker admitted during draining") }}); !errors.Is(err, ErrAdmissionClosed) {
		t.Fatalf("draining registration: %v", err)
	}
	if err := o.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerCompletionRunsAfterJoinAndFailureRetainsOwnership(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint("fail=", fail), func(t *testing.T) {
			cfg := config.NewOwnershipConfig(filepath.Join(t.TempDir(), "state.db"), config.CreateStore)
			o, err := Acquire(t.Context(), cfg, processFixture{status: ProcessAlive})
			if err != nil {
				t.Fatal(err)
			}
			defer o.state.db.Close()
			if err := write(o, func(tx *WriteTx) error { _, err := tx.Exec("CREATE TABLE completion (joined INTEGER)"); return err }); err != nil {
				t.Fatal(err)
			}
			joined := make(chan struct{})
			completion := errors.New("completion denied")
			err = o.StartWork(t.Context(), "w", ManagedWork{
				Run: func(lifetime WorkLifetime, _ func(error)) { <-lifetime.Stop; close(joined) },
				Complete: func(tx *WriteTx) error {
					select {
					case <-joined:
					default:
						t.Error("completion ran before Run joined")
					}
					if fail {
						return completion
					}
					_, err := tx.Exec("INSERT INTO completion VALUES (1)")
					return err
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			o.BeginShutdown(nil)
			err = o.Wait(t.Context())
			cfg.Action = config.OpenStore
			if fail {
				if !errors.Is(err, ErrFinalizationFailed) || !errors.Is(err, completion) {
					t.Fatalf("completion failure not reported: %v", err)
				}
				if _, err := Acquire(t.Context(), cfg, processFixture{status: ProcessAlive}); !errors.Is(err, ErrOwnerAlive) {
					t.Fatalf("failed completion allowed takeover: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			next, err := Acquire(t.Context(), cfg, processFixture{status: ProcessAlive})
			if err != nil {
				t.Fatal(err)
			}
			var n int
			if err := write(next, func(tx *WriteTx) error { return tx.Scan("SELECT count(*) FROM completion", nil, &n) }); err != nil || n != 1 {
				t.Fatalf("completion not committed before release: n=%d err=%v", n, err)
			}
			next.BeginShutdown(nil)
			if err := next.Wait(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInterruptCancelsNativeContextWithoutStopCancellingIt(t *testing.T) {
	o, _ := newOwner(t)
	observed := make(chan error, 1)
	if err := o.StartWork(t.Context(), "w", ManagedWork{Run: func(lifetime WorkLifetime, _ func(error)) {
		<-lifetime.Stop
		observed <- lifetime.Interrupt.Err()
		<-lifetime.Interrupt.Done()
	}}); err != nil {
		t.Fatal(err)
	}
	o.BeginShutdown(nil)
	if err := <-observed; err != nil {
		t.Fatalf("orderly draining cancelled the native context: %v", err)
	}
	waitCtx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := o.Wait(waitCtx); !errors.Is(err, ErrShutdownIncomplete) {
		t.Fatalf("worker waiting on Interrupt should still be draining: %v", err)
	}
	cause := errors.New("operator escalation")
	o.Interrupt(cause)
	if err := o.Wait(t.Context()); err != nil {
		t.Fatalf("interrupted worker that joined cleanly must release: %v", err)
	}
}

func TestReportFatalInterruptsAndRetainsOwnership(t *testing.T) {
	cfg := config.NewOwnershipConfig(filepath.Join(t.TempDir(), "state.db"), config.CreateStore)
	o, err := Acquire(t.Context(), cfg, processFixture{status: ProcessAlive})
	if err != nil {
		t.Fatal(err)
	}
	defer o.state.db.Close()
	fatal := errors.New("canonical persistence failed")
	if err := o.StartWork(t.Context(), "w", ManagedWork{Run: func(lifetime WorkLifetime, reportFatal func(error)) {
		reportFatal(fatal)
		<-lifetime.Interrupt.Done()
	}}); err != nil {
		t.Fatal(err)
	}
	err = o.Wait(t.Context())
	if !errors.Is(err, ErrFinalizationFailed) || !errors.Is(err, fatal) {
		t.Fatalf("fatal report lost: %v", err)
	}
	if err := o.Do(t.Context(), func(*Operation) error { return nil }); !errors.Is(err, ErrAdmissionClosed) {
		t.Fatalf("fatal report left admission open: %v", err)
	}
	select {
	case <-o.Draining():
	default:
		t.Fatal("fatal report did not begin draining")
	}
}
```

Add `"fmt"` to the imports of owner_test.go; remove `"io"` if unused after the resource deletion.

Update `admission_test.go`:

```go
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
```

Delete `resource_test.go`. In `endpoint_test.go`, replace every `o.Shutdown(ctx)` with `o.BeginShutdown(nil); err := o.Wait(ctx)` and add this test (a package-internal seam pauses Bind at its linearization point):

```go
func TestBindLosingToDrainingClosesUnpublishedEndpoint(t *testing.T) {
	o, _ := newOwner(t)
	if err := write(o, func(tx *WriteTx) error { _, err := tx.Exec("UPDATE cutover SET state='active' WHERE singleton=1"); return err }); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	paths := config.BrokerEndpoints{Socket: filepath.Join(dir, "b.sock"), PID: filepath.Join(dir, "b.pid")}
	o.state.beforePublish = func() { o.BeginShutdown(errors.New("race")) }
	if err := o.Bind(t.Context(), paths); !errors.Is(err, ErrAdmissionClosed) {
		t.Fatalf("bind published after draining began: %v", err)
	}
	for _, path := range []string{paths.Socket, paths.PID} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("unpublished endpoint left %s: %v", path, err)
		}
	}
	if err := o.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
}
```

Update `statetest/store.go` cleanup:

```go
	t.Cleanup(func() {
		o.BeginShutdown(nil)
		if err := o.Wait(context.Background()); err != nil {
			t.Error(err)
		}
	})
```
and delete `statetest.Operation` (no caller after Task 2).

- [ ] **Step 2: Run the package tests to verify they fail to compile**

Run: `go test -race -count=1 ./internal/brokerstate/ 2>&1 | head -20`
Expected: compile errors naming `StartWork`, `BeginShutdown`, `Wait`, `ManagedWork`, `WorkLifetime`, `ErrDuplicateWork`.

- [ ] **Step 3: Rewrite owner.go**

Replace the file with:

```go
// Package brokerstate owns canonical database access and the admitted lifetime
// of work that can change it. Domain packages receive scoped capabilities,
// never the underlying database or a commit/release operation.
package brokerstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
)

var (
	ErrFenced              = errors.New("canonical ownership generation is no longer current")
	ErrAdmissionClosed     = errors.New("broker admission is closed")
	ErrExpiredCapability   = errors.New("ownership capability is outside its admitted lifetime")
	ErrSchemaVersion       = errors.New("canonical schema requires explicit offline conversion")
	ErrPrepared            = errors.New("canonical store is prepared; activation is required")
	ErrOwnerAlive          = errors.New("recorded broker process is still alive")
	ErrIdentityUnavailable = errors.New("process incarnation cannot be verified")
	ErrDuplicateWork       = errors.New("managed work key is already registered")
	ErrFinalizationFailed  = errors.New("broker finalization failed; ownership retained")
	ErrShutdownIncomplete  = errors.New("broker shutdown incomplete; ownership retained")
)

type lifecycle uint8

const (
	serving lifecycle = iota + 1
	draining
	released
)

// Owner can only be initialized by Acquire. Copying the exported handle does
// not duplicate authority: its private state and admission count remain shared.
type Owner struct{ state *ownerState }

type ownerState struct {
	db             *sql.DB
	identity       string
	generation     int64
	config         config.OwnershipConfig
	mu             sync.Mutex
	phase          lifecycle
	operations     int
	workers        map[string]struct{}
	drained        chan struct{}
	stop           chan struct{}
	stopCause      error
	interrupt      context.Context
	cancel         context.CancelCauseFunc
	shutdownOnce   sync.Once
	finalDone      chan struct{}
	finalErr       error
	fatal          error
	failed         chan struct{}
	failOnce       sync.Once
	endpoint       *ownedEndpoint
	beforePublish  func() // test seam: pauses Bind at its publication point
	serviceStarted bool
	predecessor    *ProcessIdentity
	inspector      ProcessInspector
}

// WorkLifetime carries the two owner signals. Stop means start no new
// workflows; Interrupt cancels native I/O on escalation or fatal failure.
type WorkLifetime struct {
	Stop      <-chan struct{}
	Interrupt context.Context
}

// ManagedWork is passive until StartWork schedules it. Run returns only after
// every physical resource it owns has closed and joined. Complete, when set,
// is the SQL-only accounting for this work; it runs after Run joins, including
// during draining, and the work stays registered until it commits.
type ManagedWork struct {
	Run      func(lifetime WorkLifetime, reportFatal func(error))
	Complete func(tx *WriteTx) error
}

// Operation is valid only during its Do callback. Do callbacks must join work
// they start before returning.
type Operation struct{ state *operationState }

type operationState struct {
	owner  *ownerState
	mu     sync.Mutex
	active bool
}

// Do atomically admits work with respect to shutdown. It never holds a SQL
// transaction while invoking work, so provider I/O cannot hold SQLite locks.
func (o *Owner) Do(ctx context.Context, work func(*Operation) error) error {
	if o == nil || o.state == nil || work == nil {
		return ErrExpiredCapability
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s := o.state
	s.mu.Lock()
	if s.phase != serving {
		s.mu.Unlock()
		return ErrAdmissionClosed
	}
	s.operations++
	s.mu.Unlock()
	op := &Operation{state: &operationState{owner: s, active: true}}
	defer func() {
		op.state.mu.Lock()
		op.state.active = false
		op.state.mu.Unlock()
		s.mu.Lock()
		s.operations--
		s.settle()
		s.mu.Unlock()
	}()
	return work(op)
}

// StartWork inserts a unique key and schedules Run in one decision against
// draining. Duplicate keys never create another worker.
func (o *Owner) StartWork(ctx context.Context, key string, work ManagedWork) error {
	if o == nil || o.state == nil || work.Run == nil || key == "" {
		return ErrExpiredCapability
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s := o.state
	s.mu.Lock()
	if s.phase != serving {
		s.mu.Unlock()
		return ErrAdmissionClosed
	}
	if _, exists := s.workers[key]; exists {
		s.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrDuplicateWork, key)
	}
	s.workers[key] = struct{}{}
	lifetime := WorkLifetime{Stop: s.stop, Interrupt: s.interrupt}
	s.mu.Unlock()
	go s.runWork(key, work, lifetime)
	return nil
}

func (s *ownerState) runWork(key string, work ManagedWork, lifetime WorkLifetime) {
	work.Run(lifetime, s.reportFatal)
	if work.Complete != nil {
		if err := s.transaction(context.Background(), work.Complete); err != nil {
			// The registry entry is retained: draining cannot complete and
			// Wait reports this failure instead of a caller deadline.
			s.reportFatal(fmt.Errorf("complete work %s: %w", key, err))
			return
		}
	}
	s.mu.Lock()
	delete(s.workers, key)
	s.settle()
	s.mu.Unlock()
}

// settle closes drained once nothing admitted remains during draining.
// The caller holds s.mu.
func (s *ownerState) settle() {
	if s.phase != draining || s.operations != 0 || len(s.workers) != 0 {
		return
	}
	select {
	case <-s.drained:
	default:
		close(s.drained)
	}
}

// reportFatal records a permanent failure, begins draining and interrupts
// native I/O. Ownership is retained until process exit and fenced reacquisition.
func (s *ownerState) reportFatal(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	s.fatal = errors.Join(s.fatal, err)
	s.mu.Unlock()
	s.failOnce.Do(func() { close(s.failed) })
	s.interruptWith(err)
}

// Draining closes when admission closes. It is an observation, not proof that
// admitted work or a native provider has stopped.
func (o *Owner) Draining() <-chan struct{} {
	if o == nil || o.state == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return o.state.stop
}

// BeginShutdown closes normal admission, closes ingress and starts the sole
// finalizer. It never cancels Interrupt and never waits.
func (o *Owner) BeginShutdown(cause error) {
	if o == nil || o.state == nil {
		return
	}
	o.state.beginShutdown(cause)
}

func (s *ownerState) beginShutdown(cause error) {
	s.shutdownOnce.Do(func() {
		s.mu.Lock()
		s.phase = draining
		s.stopCause = cause
		close(s.stop)
		s.settle()
		s.mu.Unlock()
		// Listener close happens outside the lifecycle mutex.
		if err := s.closeListener(); err != nil {
			s.reportFatal(fmt.Errorf("close broker ingress: %w", err))
		}
		go s.finalize()
	})
}

// Interrupt begins draining, then cancels active native I/O without waiting
// for any worker mutex. It proves nothing about provider retention.
func (o *Owner) Interrupt(cause error) {
	if o == nil || o.state == nil {
		return
	}
	o.state.interruptWith(cause)
}

func (s *ownerState) interruptWith(cause error) {
	s.beginShutdown(cause)
	s.cancel(cause)
}

// Wait bounds only the caller. A deadline never changes owner progress and a
// later Wait can return success after release, or the permanent failure.
func (o *Owner) Wait(ctx context.Context) error {
	if o == nil || o.state == nil {
		return ErrExpiredCapability
	}
	s := o.state
	select {
	case <-s.finalDone:
		return s.finalErr
	default:
	}
	select {
	case <-s.finalDone:
		return s.finalErr
	case <-s.failed:
		s.mu.Lock()
		fatal := s.fatal
		s.mu.Unlock()
		return fmt.Errorf("%w: %w", ErrFinalizationFailed, fatal)
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", ErrShutdownIncomplete, ctx.Err())
	}
}

// finalize is the sole finalizer: quiescent → endpoints cleaned → released.
// It has no deadline. A recorded fatal failure retains ownership.
func (s *ownerState) finalize() {
	defer close(s.finalDone)
	select {
	case <-s.drained:
	case <-s.failed:
	}
	s.mu.Lock()
	fatal := s.fatal
	s.mu.Unlock()
	if fatal != nil {
		s.finalErr = fmt.Errorf("%w: %w", ErrFinalizationFailed, fatal)
		return
	}
	if err := s.transaction(context.Background(), func(tx *WriteTx) error {
		// Endpoint cleanup is fenced too. A stale handle must fail before it
		// can touch a successor's files, not merely when releasing the row.
		if err := s.removeEndpoints(); err != nil {
			return fmt.Errorf("cleanup broker: %w", err)
		}
		res, err := tx.Exec(`UPDATE broker_owner SET released_at = ? WHERE singleton = 1
			AND instance_id = ? AND generation = ? AND released_at IS NULL`,
			time.Now().UTC().Format(time.RFC3339Nano), s.identity, s.generation)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrFenced
		}
		return nil
	}); err != nil {
		s.finalErr = fmt.Errorf("%w: release broker ownership: %w", ErrFinalizationFailed, err)
		return
	}
	s.mu.Lock()
	s.phase = released
	s.mu.Unlock()
	if err := s.db.Close(); err != nil {
		s.finalErr = fmt.Errorf("close canonical store after release: %w", err)
	}
}
```

In `acquire.go`, replace the constructor return with:

```go
	interrupt, cancel := context.WithCancelCause(context.Background())
	return &Owner{state: &ownerState{
		db: db, identity: instance, generation: generation, config: cfg, phase: serving,
		workers: make(map[string]struct{}),
		drained: make(chan struct{}), stop: make(chan struct{}), finalDone: make(chan struct{}), failed: make(chan struct{}),
		interrupt: interrupt, cancel: cancel,
		predecessor: predecessor, inspector: inspector,
	}}, nil
```

Delete `resource.go`. Delete `Stopping` (it is replaced by `Draining`).

- [ ] **Step 4: Rewrite Bind, endpoint removal and Serve in endpoint.go**

Replace the type definitions, `Bind`, `closeListener`, `removeEndpoints` and the Accept-loop exit check with:

```go
type ownedFile struct {
	path     string
	identity os.FileInfo
	removed  bool // cleanup progress survives a later failure; never re-deleted
}

type ownedEndpoint struct {
	listener *net.UnixListener
	files    []ownedFile
}

// Bind is the only IPC constructor. Filesystem work and listening happen
// outside the lifecycle mutex; publication is one decision against draining.
// If draining wins, the created endpoint is closed and removed unpublished.
func (o *Owner) Bind(ctx context.Context, paths config.BrokerEndpoints) error {
	if err := paths.Validate(); err != nil {
		return err
	}
	return o.Do(ctx, func(op *Operation) error {
		s := o.state
		s.mu.Lock()
		bound := s.endpoint != nil
		s.mu.Unlock()
		if bound {
			return fmt.Errorf("broker endpoints already bound")
		}
		if err := op.Write(ctx, func(tx *WriteTx) error { return tx.RequireActive() }); err != nil {
			return err
		}
		if err := s.reclaimEndpoints(ctx, paths); err != nil {
			return err
		}
		ep, err := createEndpoint(paths)
		if err != nil {
			return err
		}
		if s.beforePublish != nil {
			s.beforePublish()
		}
		s.mu.Lock()
		if s.phase != serving || s.endpoint != nil {
			s.mu.Unlock()
			return errors.Join(ErrAdmissionClosed, ep.discard())
		}
		s.endpoint = ep
		s.mu.Unlock()
		return nil
	})
}

func createEndpoint(paths config.BrokerEndpoints) (_ *ownedEndpoint, err error) {
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: paths.Socket, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("bind broker socket: %w", err)
	}
	listener.SetUnlinkOnClose(false)
	ep := &ownedEndpoint{listener: listener}
	defer func() {
		if err != nil {
			err = errors.Join(err, ep.discard())
		}
	}()
	info, err := os.Lstat(paths.Socket)
	if err != nil {
		return nil, fmt.Errorf("record broker socket identity: %w", err)
	}
	ep.files = append(ep.files, ownedFile{path: paths.Socket, identity: info})
	if err = os.Chmod(paths.Socket, 0700); err != nil {
		return nil, err
	}
	pid, err := os.OpenFile(paths.PID, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("create broker PID file: %w", err)
	}
	info, statErr := pid.Stat()
	if statErr != nil {
		return nil, errors.Join(statErr, pid.Close())
	}
	ep.files = append(ep.files, ownedFile{path: paths.PID, identity: info})
	_, writeErr := fmt.Fprintln(pid, os.Getpid())
	if err = errors.Join(writeErr, pid.Close()); err != nil {
		return nil, err
	}
	return ep, nil
}

// discard closes an unpublished or failed endpoint and removes what it created.
func (ep *ownedEndpoint) discard() error {
	err := ep.listener.Close()
	if errors.Is(err, net.ErrClosed) {
		err = nil
	}
	return errors.Join(err, ep.remove())
}

// remove deletes recorded files in order and records each success, so a
// later failure never repeats or forgets a completed removal.
func (ep *ownedEndpoint) remove() error {
	for i := range ep.files {
		file := &ep.files[i]
		if file.removed {
			continue
		}
		if err := removeOwnedFile(*file); err != nil {
			return err
		}
		file.removed = true
	}
	return nil
}

func (s *ownerState) closeListener() error {
	s.mu.Lock()
	ep := s.endpoint
	s.mu.Unlock()
	if ep == nil {
		return nil // Maintenance ownership has no IPC endpoint.
	}
	err := ep.listener.Close()
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// removeEndpoints runs after quiescence; nothing else touches the endpoint.
func (s *ownerState) removeEndpoints() error {
	s.mu.Lock()
	ep := s.endpoint
	s.mu.Unlock()
	if ep == nil {
		return nil
	}
	return ep.remove()
}
```

In `Serve`, replace `if ep == nil || !ep.ready || handle == nil` with `if ep == nil || handle == nil`, and replace the Accept error branch with:

```go
			if err != nil {
				select {
				case <-s.stop:
					return nil
				default:
					return fmt.Errorf("accept broker connection: %w", err)
				}
			}
```

Keep `reclaimEndpoints` and `removeOwnedFile` unchanged.

- [ ] **Step 5: Run the package tests**

Run: `go test -race -count=1 -v ./internal/brokerstate/ 2>&1 | tail -40`
Expected: all tests PASS, including the three `TestShutdownWaiterTimeoutPreservesFinalization` cases. If `go vet` complains about `%w: %w`, the module's Go version supports it (1.26); do not rewrite the wrapping.

- [ ] **Step 6: Commit**

```bash
git add internal/brokerstate
git commit -m "feat(brokerstate): owner-registered work, deadline-free finalizer, bounded waiters

Adds StartWork/BeginShutdown/Interrupt/Wait/Draining, removes Shutdown(ctx),
Stopping and the Resource wrappers. Bind no longer holds the lifecycle mutex
across filesystem work and closes an unpublished endpoint when draining wins.
Late finalization after a waiter timeout now releases; close/completion
failures are reported permanently and retain ownership.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HGU6FAQiaEWgLV3A2dRf6P"
```

---

### Task 2: Move provider mechanics behind the broker boundary and remove ownership handles from drivers

**Files:**
- Create: `internal/broker/enrollment/internal/driver/driver.go` (from `internal/driver/driver.go` minus `Handle`/`Open`)
- Move: `internal/driver/codex/*` → `internal/broker/enrollment/internal/driver/codex/*`
- Move: `internal/native/*` → `internal/broker/enrollment/internal/native/*`
- Create: `internal/broker/enrollment/types.go` (exported aliases)
- Delete: `internal/driver/`, `internal/native/`
- Modify: `architecture_test.go:71-81` (delete the `*ast.FuncDecl` case)

**Interfaces:**
- Produces:
  ```go
  // package driver (internal/broker/enrollment/internal/driver)
  type Connector interface { Check(messages.Enrollment) error; Open(context.Context, messages.Enrollment) (Driver, error) }
  type Driver interface { Probe(context.Context) (Readiness, error); Submit(context.Context, messages.Envelope) Outcome; Close() error }
  // package codex
  func Attach(ctx context.Context, target messages.Enrollment, version string, cfg config.NativeConfig, dial func(context.Context, string, string) (net.Conn, error), observe func(context.Context, Event) error) (driver.Driver, error)
  // package native
  func New(cfg config.ProviderConfig) (*Connector, error); func (c *Connector) Open(ctx context.Context, e messages.Enrollment) (driver.Driver, error)
  // package enrollment (types.go)
  type Connector = driver.Connector; type Driver = driver.Driver; type Readiness = driver.Readiness; type Outcome = driver.Outcome
  type Availability = driver.Availability; type Possession = driver.Possession
  const ( Unavailable = driver.Unavailable; Idle = driver.Idle; Busy = driver.Busy )
  const ( NotSubmitted = driver.NotSubmitted; Accepted = driver.Accepted; Held = driver.Held; Refused = driver.Refused; Uncertain = driver.Uncertain )
  func NewConnector(cfg config.ProviderConfig) (Connector, error)
  ```

- [ ] **Step 1: Move the packages with history**

```bash
cd /Users/spson/Projects/Claude/waggle
mkdir -p internal/broker/enrollment/internal
git mv internal/driver internal/broker/enrollment/internal/driver
git mv internal/native internal/broker/enrollment/internal/native
```

- [ ] **Step 2: Rewrite driver.go without ownership handles**

`internal/broker/enrollment/internal/driver/driver.go`:

```go
// Package driver defines native input observations. Only the broker may
// translate observations into canonical message transitions.
package driver

import (
	"context"
	"errors"

	"github.com/seungpyoson/waggle/internal/messages"
)

var ErrPeerIdentity = errors.New("native peer ownership could not be verified")

// Connector validates configured provider support before enrollment. Open
// binds one driver to the exact enrollment; it must never create or resume a
// native session. A failed Open has not submitted input and must join any
// work it started. Errors are safe diagnostics: exclude native response
// bodies and credentials. Only an enrollment worker calls Open.
type Connector interface {
	Check(messages.Enrollment) error
	Open(context.Context, messages.Enrollment) (Driver, error)
}

type Availability uint8

const (
	Unavailable Availability = iota + 1
	Idle
	Busy
)

type Readiness struct {
	Conversation string
	Availability Availability
	TurnID       string
	Evidence     string
}

type Possession uint8

const (
	NotSubmitted Possession = iota + 1
	Accepted
	Held
	Refused
	Uncertain
)

type Outcome struct {
	Possession  Possession
	ProviderRef string
	Evidence    string
}

// Implementations have no queue, database, model selection or permission
// policy. Identity is fixed by Connector.Open, never supplied again by a
// caller. Neither Close nor a context deadline proves native withdrawal.
type Driver interface {
	Probe(context.Context) (Readiness, error)
	Submit(context.Context, messages.Envelope) Outcome
	Close() error
}
```

- [ ] **Step 3: Update codex and native to the new signatures**

In `codex/attachment.go` remove the `brokerstate` import, delete the exported `Attach` wrapper and rename `attach` to `Attach` with the signature in Interfaces (returns `(driver.Driver, error)`). In `native/connector.go` remove the `brokerstate` import and change `Open` to `func (c *Connector) Open(ctx context.Context, e messages.Enrollment) (driver.Driver, error)`; its body is unchanged apart from dropping the `lifetime` argument in the `codex.Attach` call. Fix import paths in every moved file with:

```bash
grep -rl 'waggle/internal/driver\|waggle/internal/native' internal cmd architecture_test.go | xargs sed -i '' \
  -e 's#github.com/seungpyoson/waggle/internal/driver#github.com/seungpyoson/waggle/internal/broker/enrollment/internal/driver#g' \
  -e 's#github.com/seungpyoson/waggle/internal/native#github.com/seungpyoson/waggle/internal/broker/enrollment/internal/native#g'
```

- [ ] **Step 4: Update the codex tests**

In `codex/attachment_test.go`: delete `TestAttachmentRequiresOwnershipBeforeDial`; remove the `brokerstate` and `statetest` imports; change `attachPeer` to:

```go
func attachPeer(t *testing.T, target messages.Enrollment, dial func(context.Context, string, string) (net.Conn, error)) driver.Driver {
	t.Helper()
	c, err := Attach(t.Context(), target, "test-build", config.NewNativeConfig(), dial, func(context.Context, Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return c
}
```
Replace every `op := statetest.Operation(t, statetest.New(t, "SELECT 1"))` + `Attach(t.Context(), op, ...)` with `Attach(t.Context(), ...)`, every `c, op := attachPeer(...)` with `c := attachPeer(...)`, every `c.Submit(t.Context(), op, envelope)` with `c.Submit(t.Context(), envelope)` and every `c.Probe(t.Context(), op)` with `c.Probe(t.Context())` in `attachment_test.go` and `thread_test.go`.

- [ ] **Step 5: Add the exported alias file**

`internal/broker/enrollment/types.go`:

```go
// Package enrollment implements owner-registered workers that own a native
// transport for exactly one reserved enrollment. It exposes the worker start
// entrypoint and observation types; it never returns a callable transport.
package enrollment

import (
	"github.com/seungpyoson/waggle/internal/broker/enrollment/internal/driver"
	"github.com/seungpyoson/waggle/internal/broker/enrollment/internal/native"
	"github.com/seungpyoson/waggle/internal/config"
)

type (
	Connector    = driver.Connector
	Driver       = driver.Driver
	Readiness    = driver.Readiness
	Outcome      = driver.Outcome
	Availability = driver.Availability
	Possession   = driver.Possession
)

const (
	Unavailable = driver.Unavailable
	Idle        = driver.Idle
	Busy        = driver.Busy
)

const (
	NotSubmitted = driver.NotSubmitted
	Accepted     = driver.Accepted
	Held         = driver.Held
	Refused      = driver.Refused
	Uncertain    = driver.Uncertain
)

// NewConnector selects the configured production provider mechanics.
func NewConnector(cfg config.ProviderConfig) (Connector, error) {
	return native.New(cfg)
}
```

- [ ] **Step 6: Update the architecture guard**

In `architecture_test.go` delete the entire `case *ast.FuncDecl:` block (lines 71-81). The Go internal-package rule now enforces that only `internal/broker/enrollment` reaches provider constructors.

- [ ] **Step 7: Run the moved packages' tests**

Run: `go test -race -count=1 ./internal/broker/enrollment/... 2>&1 | tail -20`
Expected: `ok` for `internal/driver/codex` and `internal/native` under their new paths; `enrollment` has no tests yet.

- [ ] **Step 8: Commit**

```bash
git add internal/broker/enrollment internal/driver internal/native architecture_test.go
git commit -m "refactor: move provider mechanics under internal/broker/enrollment/internal

Drivers no longer take an ownership Operation; only the enrollment worker
may open a transport. Deletes driver.Handle/Open.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HGU6FAQiaEWgLV3A2dRf6P"
```

---

### Task 3: Enrollment workers replace the global scheduler; broker rewiring

**Files:**
- Modify: `internal/config/messaging.go` (intervals)
- Modify: `internal/messages/model.go:91` and `store.go:364-427` (`Select{Recipient}`)
- Create: `internal/broker/enrollment/worker.go`, `internal/broker/enrollment/supervisor.go`
- Modify: `internal/broker/broker.go` (New/Serve/Shutdown; maintenance as managed work), `internal/broker/messaging.go:74-77`, `internal/broker/router.go:496`
- Delete: `internal/broker/scheduler.go`
- Modify tests: `internal/broker/native_fixture_test.go`, `broker_test.go` helpers, `scheduler_test.go`, `subscription_test.go`, `messaging_test.go`, `ownership_test.go`
- Create: `internal/broker/progress_test.go` (E1, E6)

**Interfaces:**
- Consumes: Task 1 owner API; Task 2 `enrollment.Connector`/`Driver`.
- Produces:
  ```go
  // config
  type MessagingConfig struct { ...; DiscoveryInterval, QueueCheckInterval, ProbeInterval, ReconnectBackoff time.Duration; ... } // ScanPeriod removed
  // messages
  type Select struct{ Recipient string } // "" = every bound recipient (tests/inspection); worker always sets it
  // enrollment
  func Start(ctx context.Context, owner *brokerstate.Owner, connector Connector, limits config.MessagingConfig, transport config.NativeConfig) (*Supervisor, error)
  func (s *Supervisor) Wake(recipient string) // "" wakes discovery
  // broker
  func New(ctx context.Context, owner *brokerstate.Owner, cfg config.BrokerConfig, provider config.ProviderConfig) (*Broker, error)
  func newWithConnector(ctx context.Context, owner *brokerstate.Owner, cfg config.BrokerConfig, transport config.NativeConfig, connector enrollment.Connector) (*Broker, error)
  func (b *Broker) Serve(ctx context.Context) error
  func (b *Broker) Shutdown(ctx context.Context) error // BeginShutdown + Wait
  ```

- [ ] **Step 1: Config intervals**

In `internal/config/messaging.go` replace `ScanPeriod` with four fields and validate them:

```go
type MessagingConfig struct {
	PendingTTL         time.Duration
	DefaultDeadline    time.Duration
	MaxDeadline        time.Duration
	DiscoveryInterval  time.Duration // reservation/enrollment discovery scan
	QueueCheckInterval time.Duration // per-worker minimum between queue checks
	ProbeInterval      time.Duration // per-worker readiness re-observation while bound
	ReconnectBackoff   time.Duration // per-worker wait after a failed open/probe
	EnrollmentCapacity int
	QueueCapacity      int
	ScanLimit          int
	DefaultHops        int
	MaxHops            int
	MaxBodyBytes       int
	MaxEvidenceBytes   int
	MaxFieldBytes      int
}

func NewMessagingConfig() MessagingConfig {
	return MessagingConfig{
		PendingTTL: time.Minute, DefaultDeadline: 5 * time.Minute, MaxDeadline: time.Hour,
		DiscoveryInterval: 500 * time.Millisecond, QueueCheckInterval: 500 * time.Millisecond,
		ProbeInterval: 500 * time.Millisecond, ReconnectBackoff: time.Second,
		EnrollmentCapacity: 256, QueueCapacity: 100, ScanLimit: 100, DefaultHops: 8, MaxHops: 32,
		MaxBodyBytes: 64 * 1024, MaxEvidenceBytes: 16 * 1024,
		MaxFieldBytes: Defaults.MaxFieldLength,
	}
}

func (c MessagingConfig) Validate() error {
	if c.PendingTTL <= 0 || c.DefaultDeadline <= 0 || c.MaxDeadline < c.DefaultDeadline {
		return fmt.Errorf("messaging requires positive periods and a bounded default deadline")
	}
	if c.DiscoveryInterval <= 0 || c.QueueCheckInterval <= 0 || c.ProbeInterval <= 0 || c.ReconnectBackoff <= 0 {
		return fmt.Errorf("enrollment worker intervals must be positive")
	}
	if c.EnrollmentCapacity <= 0 || c.QueueCapacity <= 0 || c.ScanLimit <= 0 || c.DefaultHops <= 0 || c.MaxHops < c.DefaultHops || c.MaxBodyBytes <= 0 || c.MaxEvidenceBytes <= 0 || c.MaxFieldBytes <= 0 {
		return fmt.Errorf("messaging limits must be positive and default hops must not exceed the maximum")
	}
	return nil
}
```
Update `internal/config/config_test.go` if it references `ScanPeriod`.

- [ ] **Step 2: Recipient-scoped selection**

`internal/messages/model.go`: `type Select struct{ Recipient string }`.
In `store.go` `selectWork`, change the query to take an optional recipient and LIMIT 1 per worker:

```go
func (s *Store) selectWork(c Select) (Result, error) {
	var ids []string
	limit := s.limits.ScanLimit
	if c.Recipient != "" {
		limit = 1
	}
	err := s.tx.Query(`SELECT id FROM (
	 SELECT m.id,m.sequence,row_number() OVER (PARTITION BY m.recipient ORDER BY m.sequence) AS position
	 FROM messages m JOIN conversations c ON c.id=m.conversation_id
	 JOIN enrollments e ON e.id=m.recipient JOIN broker_owner o ON o.singleton=1
	 WHERE e.state='bound' AND e.verified_generation=o.generation AND c.stopped_reason='' AND c.deadline>? AND c.remaining_hops>0
	 AND (?='' OR m.recipient=?)
	 AND NOT EXISTS(SELECT 1 FROM attempts a WHERE a.message_id=m.id)
	 AND NOT EXISTS(SELECT 1 FROM recipient_barrier b WHERE b.recipient=m.recipient)
	) WHERE position=1 ORDER BY sequence LIMIT ?`, []any{s.now.UnixNano(), c.Recipient, c.Recipient, limit}, func(rows *sql.Rows) error {
```
and `case Select: return s.selectWork(c)` in `Apply`. The rest of the function is unchanged. Add to `store_test.go`:

```go
func TestSelectScopedToRecipientLeavesOtherRecipientsUntouched(t *testing.T) {
	f := newFixture(t)
	a := f.bound("a")
	b := f.bound("b")
	c := f.bound("c")
	f.must(Enqueue{Credential: a.Credential, Recipient: b.Enrollment.ID, RequestID: "to-b", Body: "b", Hops: f.limits.DefaultHops})
	f.must(Enqueue{Credential: a.Credential, Recipient: c.Enrollment.ID, RequestID: "to-c", Body: "c", Hops: f.limits.DefaultHops})
	d := f.must(Select{Recipient: b.Enrollment.ID}).Dispatches
	if len(d) != 1 || d[0].Recipient.ID != b.Enrollment.ID {
		t.Fatalf("recipient-scoped selection: %+v", d)
	}
	if f.message(d[0].Envelope.Message.ID).Attempt == "" {
		t.Fatal("intent not committed with selection")
	}
	rest := f.must(Select{}).Dispatches
	if len(rest) != 1 || rest[0].Recipient.ID != c.Enrollment.ID {
		t.Fatalf("scoped selection touched another recipient: %+v", rest)
	}
}
```
(`f.bound` and `f.message` already exist in the fixture; check `store_test.go` for their names and adapt if they differ.)

- [ ] **Step 3: Run messages and config tests**

Run: `go test -race -count=1 ./internal/messages/ ./internal/config/ 2>&1 | tail -5`
Expected: `ok` for both.

- [ ] **Step 4: Write the worker**

`internal/broker/enrollment/worker.go`:

```go
package enrollment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/seungpyoson/waggle/internal/broker/enrollment/internal/driver"
	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/messages"
)

// worker owns one enrollment's physical transport for the lifetime of its
// registration. It holds no queue, readiness flag or receipt state: every
// canonical fact is read and written through fenced transactions.
type worker struct {
	owner     *brokerstate.Owner
	connector driver.Connector
	limits    config.MessagingConfig
	transport config.NativeConfig
	id        string
	wake      chan struct{}
	native    driver.Driver
	lastProbe time.Time
	lastCheck time.Time
	nextOpen  time.Time
	bound     bool
}

var errBackoff = errors.New("native reconnect backoff in effect")

func (w *worker) run(lifetime brokerstate.WorkLifetime, reportFatal func(error)) {
	defer w.closeTransport(reportFatal)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-lifetime.Stop:
			return
		case <-w.wake:
			// Coalesced hint. The minimum interval still applies.
			if wait := w.limits.QueueCheckInterval - time.Since(w.lastCheck); wait > 0 {
				select {
				case <-lifetime.Stop:
					return
				case <-time.After(wait):
				}
			}
		case <-timer.C:
		}
		w.lastCheck = time.Now()
		done, err := w.check(lifetime)
		if done || errors.Is(err, brokerstate.ErrAdmissionClosed) {
			return
		}
		if err != nil {
			reportFatal(err)
			return
		}
		timer.Reset(w.limits.QueueCheckInterval)
	}
}

// check is one admitted workflow: read the enrollment, observe readiness,
// persist only changed evidence, select at most one message for this
// recipient, submit it, persist the outcome. done reports a terminal enrollment.
func (w *worker) check(lifetime brokerstate.WorkLifetime) (done bool, err error) {
	err = w.owner.Do(lifetime.Interrupt, func(op *brokerstate.Operation) error {
		var e messages.Enrollment
		if err := op.Write(lifetime.Interrupt, func(tx *brokerstate.WriteTx) error {
			var err error
			e, err = messages.NewStore(tx, w.limits, time.Now()).Enrollment(w.id)
			return err
		}); err != nil {
			return err
		}
		switch e.State {
		case "retired", "failed":
			done = true
			return nil
		case "pending", "bound", "disconnected":
		default:
			return fmt.Errorf("invalid canonical enrollment state for %s", w.id)
		}
		if !w.bound || e.State != "bound" || time.Since(w.lastProbe) >= w.limits.ProbeInterval {
			command, err := w.observe(lifetime, e)
			if err != nil {
				return err
			}
			if command != nil {
				err = op.Write(context.WithoutCancel(lifetime.Interrupt), func(tx *brokerstate.WriteTx) error {
					_, err := messages.NewStore(tx, w.limits, time.Now()).Apply(command)
					return err
				})
				if errors.Is(err, messages.ErrConflict) {
					// Retirement or expiry won while the probe was in flight.
					done = true
					return nil
				}
				if err != nil {
					return err
				}
			}
			if !w.bound {
				return nil
			}
		}
		var selected messages.Result
		if err := op.Write(lifetime.Interrupt, func(tx *brokerstate.WriteTx) error {
			var err error
			selected, err = messages.NewStore(tx, w.limits, time.Now()).Apply(messages.Select{Recipient: w.id})
			return err
		}); err != nil {
			return err
		}
		if len(selected.Dispatches) == 0 {
			return nil
		}
		d := selected.Dispatches[0]
		callCtx, cancel := context.WithTimeout(lifetime.Interrupt, w.transport.RequestTimeout)
		outcome := w.native.Submit(callCtx, d.Envelope)
		cancel()
		kind, err := kindOf(outcome.Possession, d.Envelope.Message.Attempt)
		if err != nil {
			return err
		}
		// Cancelling native I/O must not cancel persistence of what was observed.
		return op.Write(context.WithoutCancel(lifetime.Interrupt), func(tx *brokerstate.WriteTx) error {
			_, err := messages.NewStore(tx, w.limits, time.Now()).Apply(messages.Observe{
				Attempt: d.Envelope.Message.Attempt, Recipient: w.id,
				Kind: kind, ProviderRef: outcome.ProviderRef, Evidence: outcome.Evidence,
			})
			return err
		})
	})
	return done, err
}

func kindOf(p driver.Possession, attempt string) (string, error) {
	switch p {
	case driver.NotSubmitted:
		return "not_submitted", nil
	case driver.Accepted:
		return "accepted", nil
	case driver.Held:
		return "held", nil
	case driver.Refused:
		return "refused", nil
	case driver.Uncertain:
		return "uncertain", nil
	}
	return "", fmt.Errorf("native driver returned invalid possession for attempt %s; intent remains uncertain", attempt)
}

// observe opens the transport when absent, probes, and returns the canonical
// command to persist or nil when nothing changed. Transport failure is an
// observation of unavailability, never native withdrawal.
func (w *worker) observe(lifetime brokerstate.WorkLifetime, e messages.Enrollment) (messages.Command, error) {
	view, err := w.probe(lifetime, e)
	w.lastProbe = time.Now()
	if err != nil {
		w.bound = false
		var fatal *fatalError
		if errors.As(err, &fatal) {
			return nil, fatal.err
		}
		if errors.Is(err, errBackoff) {
			return nil, nil // nothing observed; nothing to write
		}
		evidence := "native readiness verification failed before input submission: " + err.Error()
		if e.State != "bound" && e.Evidence == evidence {
			return nil, nil // unchanged observation: no write
		}
		return messages.Unavailable{ID: e.ID, Evidence: evidence}, nil
	}
	if view.Conversation != e.Conversation || view.Evidence == "" {
		w.bound = false
		return nil, fmt.Errorf("native driver returned uncorrelated readiness for %s", e.ID)
	}
	switch view.Availability {
	case driver.Idle, driver.Busy:
		w.bound = true
		if e.State == "bound" && e.Evidence == view.Evidence {
			return nil, nil // unchanged observation: no write
		}
		return messages.Bind{ID: e.ID, Conversation: e.Conversation, Endpoint: e.Endpoint, Evidence: view.Evidence}, nil
	case driver.Unavailable:
		w.bound = false
		if e.State != "bound" && e.Evidence == view.Evidence {
			return nil, nil
		}
		return messages.Unavailable{ID: e.ID, Evidence: view.Evidence}, nil
	}
	return nil, fmt.Errorf("native driver returned invalid availability for %s", e.ID)
}

func (w *worker) probe(lifetime brokerstate.WorkLifetime, e messages.Enrollment) (driver.Readiness, error) {
	if w.native == nil {
		if time.Now().Before(w.nextOpen) {
			return driver.Readiness{}, errBackoff
		}
		native, err := w.connector.Open(lifetime.Interrupt, e)
		if err != nil {
			w.nextOpen = time.Now().Add(w.limits.ReconnectBackoff)
			return driver.Readiness{}, err
		}
		w.native = native
	}
	ctx, cancel := context.WithTimeout(lifetime.Interrupt, w.transport.RequestTimeout)
	defer cancel()
	view, err := w.native.Probe(ctx)
	if err != nil {
		w.nextOpen = time.Now().Add(w.limits.ReconnectBackoff)
		closeErr := w.native.Close()
		w.native = nil
		if closeErr != nil {
			return driver.Readiness{}, &fatalError{fmt.Errorf("close native transport for %s after failed probe: %w", w.id, closeErr)}
		}
	}
	return view, err
}

// fatalError marks an unresolved physical cleanup failure. It is never
// converted into a readiness observation.
type fatalError struct{ err error }

func (f *fatalError) Error() string { return f.err.Error() }
func (f *fatalError) Unwrap() error { return f.err }

// closeTransport joins this worker's transport. An unresolved close error is
// fatal: the owner retains ownership rather than claiming quiescence.
func (w *worker) closeTransport(reportFatal func(error)) {
	if w.native == nil {
		return
	}
	err := w.native.Close()
	w.native = nil
	if err != nil {
		reportFatal(fmt.Errorf("close native transport for %s: %w", w.id, err))
	}
}

// complete is the SQL-only accounting fixed at registration: it clears this
// incarnation's current readiness after the transport has joined.
func (w *worker) complete(tx *brokerstate.WriteTx) error {
	_, err := messages.NewStore(tx, w.limits, time.Now()).Apply(messages.Unavailable{ID: w.id, Evidence: "enrollment worker stopped; readiness cleared"})
	if errors.Is(err, messages.ErrConflict) {
		return nil // terminal enrollment: nothing to clear
	}
	return err
}
```

Rule encoded above: a probe failure is an observation (`Unavailable`), an unresolved transport close is fatal (`fatalError` → `reportFatal` via `check` returning it). Never fold a close error into evidence text.

- [ ] **Step 5: Write the supervisor**

`internal/broker/enrollment/supervisor.go`:

```go
package enrollment

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/messages"
)

const discoveryKey = "enrollment-discovery"

// Supervisor is owner-tracked discovery work: it registers one worker per
// observable enrollment. Wakeups carry no work; periodic discovery covers a
// lost wakeup. It never holds a transport and never selects a message.
type Supervisor struct {
	owner     *brokerstate.Owner
	connector Connector
	limits    config.MessagingConfig
	transport config.NativeConfig
	scan      chan struct{}
	mu        sync.Mutex
	workers   map[string]*worker
}

func Start(ctx context.Context, owner *brokerstate.Owner, connector Connector, limits config.MessagingConfig, transport config.NativeConfig) (*Supervisor, error) {
	if owner == nil || connector == nil {
		return nil, fmt.Errorf("enrollment supervision requires an owner and a connector")
	}
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	if err := transport.Validate(); err != nil {
		return nil, err
	}
	s := &Supervisor{owner: owner, connector: connector, limits: limits, transport: transport, scan: make(chan struct{}, 1), workers: make(map[string]*worker)}
	if err := owner.StartWork(ctx, discoveryKey, brokerstate.ManagedWork{Run: s.run}); err != nil {
		return nil, err
	}
	s.Wake("")
	return s, nil
}

// Wake hints one recipient's worker, or discovery when the recipient is
// unknown. It never blocks and never carries work.
func (s *Supervisor) Wake(recipient string) {
	s.mu.Lock()
	w := s.workers[recipient]
	s.mu.Unlock()
	target := s.scan
	if w != nil {
		target = w.wake
	}
	select {
	case target <- struct{}{}:
	default:
	}
}

func (s *Supervisor) run(lifetime brokerstate.WorkLifetime, reportFatal func(error)) {
	tick := time.NewTicker(s.limits.DiscoveryInterval)
	defer tick.Stop()
	for {
		select {
		case <-lifetime.Stop:
			return
		case <-s.scan:
		case <-tick.C:
		}
		if err := s.discover(lifetime); err != nil {
			if errors.Is(err, brokerstate.ErrAdmissionClosed) {
				return
			}
			reportFatal(fmt.Errorf("enrollment discovery: %w", err))
			return
		}
	}
}

// discover pages observable enrollments and registers absent workers. A key
// still registered with the owner (joined but not yet completed) is skipped
// until its completion commits; no second worker can exist for an enrollment.
func (s *Supervisor) discover(lifetime brokerstate.WorkLifetime) error {
	var after string
	for {
		var page []messages.Enrollment
		if err := s.owner.Do(lifetime.Interrupt, func(op *brokerstate.Operation) error {
			return op.Write(lifetime.Interrupt, func(tx *brokerstate.WriteTx) error {
				store := messages.NewStore(tx, s.limits, time.Now())
				if _, err := store.Apply(messages.Expire{}); err != nil {
					return err
				}
				var err error
				page, err = store.Enrollments(after, messages.ObservableEnrollments)
				return err
			})
		}); err != nil {
			return err
		}
		for _, e := range page {
			if err := s.register(lifetime, e.ID); err != nil {
				return err
			}
		}
		if len(page) < s.limits.ScanLimit {
			return nil
		}
		after = page[len(page)-1].ID
	}
}

func (s *Supervisor) register(lifetime brokerstate.WorkLifetime, id string) error {
	s.mu.Lock()
	_, present := s.workers[id]
	s.mu.Unlock()
	if present {
		return nil
	}
	w := &worker{owner: s.owner, connector: s.connector, limits: s.limits, transport: s.transport, id: id, wake: make(chan struct{}, 1)}
	err := s.owner.StartWork(lifetime.Interrupt, "enrollment:"+id, brokerstate.ManagedWork{
		Run: func(lifetime brokerstate.WorkLifetime, reportFatal func(error)) {
			defer s.forget(id)
			w.run(lifetime, reportFatal)
		},
		Complete: w.complete,
	})
	if errors.Is(err, brokerstate.ErrDuplicateWork) {
		return nil // previous worker joined but its completion has not committed yet
	}
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.workers[id] = w
	s.mu.Unlock()
	w.wake <- struct{}{}
	return nil
}

func (s *Supervisor) forget(id string) {
	s.mu.Lock()
	delete(s.workers, id)
	s.mu.Unlock()
}
```

- [ ] **Step 6: Rewire the broker**

Replace `internal/broker/broker.go` contents from the `Broker` type through `maintain` with:

```go
// Broker is the main broker orchestrator
type Broker struct {
	config     config.BrokerConfig
	hub        *events.Hub
	owner      *brokerstate.Owner
	enrollment *enrollment.Supervisor
	lockMgr    *locks.Manager
	sessions   map[string]*Session
	mu         sync.RWMutex
}

// New attaches the broker to an acquired canonical owner, binds its endpoints
// and registers maintenance and enrollment work. It has no database
// constructor and no endpoint cleanup authority of its own.
func New(ctx context.Context, owner *brokerstate.Owner, cfg config.BrokerConfig, provider config.ProviderConfig) (*Broker, error) {
	connector, err := enrollment.NewConnector(provider)
	if err != nil {
		return nil, err
	}
	return newWithConnector(ctx, owner, cfg, provider.Transport, connector)
}

func newWithConnector(ctx context.Context, owner *brokerstate.Owner, cfg config.BrokerConfig, transport config.NativeConfig, connector enrollment.Connector) (*Broker, error) {
	if err := config.ValidateDefaults(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	// Complete startup persistence before binding or reporting daemon readiness.
	if err := owner.Do(ctx, func(op *brokerstate.Operation) error {
		return op.Write(ctx, func(tx *brokerstate.WriteTx) error {
			if err := tx.RequireActive(); err != nil {
				return err
			}
			if _, err := tasks.NewStore(tx).RequeueAllClaimed(); err != nil {
				return err
			}
			_, err := messages.NewStore(tx, cfg.Messaging, time.Now()).Apply(messages.Restart{})
			return err
		})
	}); err != nil {
		return nil, fmt.Errorf("recover task claims: %w", err)
	}
	if err := owner.Bind(ctx, cfg.Endpoints); err != nil {
		return nil, err
	}
	b := &Broker{
		config: cfg, owner: owner, hub: events.NewHub(), lockMgr: locks.NewManager(),
		sessions: make(map[string]*Session),
	}
	if err := owner.StartWork(ctx, "maintenance", brokerstate.ManagedWork{Run: b.maintain}); err != nil {
		return nil, err
	}
	supervisor, err := enrollment.Start(ctx, owner, connector, cfg.Messaging, transport)
	if err != nil {
		return nil, err
	}
	b.enrollment = supervisor
	return b, nil
}

// Initialize is unchanged.

// Serve runs ingress until the owner closes the listener. Workers are
// registered separately and joined by the owner, not by this call.
func (b *Broker) Serve(ctx context.Context) error {
	return b.owner.Serve(ctx, func(conn net.Conn) {
		_ = b.owner.Do(ctx, func(lifetime *brokerstate.Operation) error { newSession(conn, b).readLoop(lifetime); return nil })
	})
}

// Shutdown begins orderly draining and waits under the caller's bound only.
func (b *Broker) Shutdown(ctx context.Context) error {
	b.owner.BeginShutdown(nil)
	return b.owner.Wait(ctx)
}

func (b *Broker) maintain(lifetime brokerstate.WorkLifetime, reportFatal func(error)) {
	lease := time.NewTicker(b.config.LeaseCheckPeriod)
	taskTTL := time.NewTicker(b.config.TaskTTLCheckPeriod)
	defer lease.Stop()
	defer taskTTL.Stop()
	for {
		var change func(*brokerstate.WriteTx) error
		var effects []protocol.Event
		select {
		case <-lifetime.Stop:
			return
		case <-lease.C:
			change = func(tx *brokerstate.WriteTx) error { _, err := tasks.NewStore(tx).RequeueExpiredLeases(); return err }
		case <-taskTTL.C:
			change = func(tx *brokerstate.WriteTx) error {
				store := tasks.NewStore(tx)
				if _, err := store.CancelExpiredTTL(); err != nil {
					return err
				}
				health, err := store.QueueHealth(b.config.TaskStaleThreshold)
				if err != nil {
					return err
				}
				if health.StaleCount > 0 {
					effects = append(effects, protocol.Event{
						Topic: "task.events", Event: "task.stale",
						Data: mustMarshal(map[string]any{
							"stale_count": health.StaleCount, "oldest_age_seconds": health.OldestPendingAge,
						}),
						TS: time.Now().UTC().Format(time.RFC3339),
					})
				}
				return nil
			}
		}
		err := b.owner.Do(lifetime.Interrupt, func(op *brokerstate.Operation) error { return op.Write(lifetime.Interrupt, change) })
		if errors.Is(err, brokerstate.ErrAdmissionClosed) {
			return
		}
		if err != nil {
			reportFatal(fmt.Errorf("canonical maintenance: %w", err))
			return
		}
		for _, event := range effects {
			b.hub.Publish(event.Topic, mustMarshal(event))
		}
	}
}
```

Remove the `driver` import and the `native`, `wake`, `stopCh`, `wg` fields. Note `readLoop` still receives the per-connection `Operation` (the previous code passed Serve's own operation; a per-connection Do keeps each connection's cleanup admitted until it returns, which is what `Session.cleanup` relies on).

In `internal/broker/messaging.go` replace the wakeup block with:

```go
	switch req.Cmd {
	case protocol.CmdSend, protocol.CmdEnqueue, protocol.CmdReply:
		if m, ok := data.(messages.Message); ok {
			s.broker.enrollment.Wake(m.Recipient)
		}
	case protocol.CmdAck:
		if m, ok := data.(messages.Message); ok {
			s.broker.enrollment.Wake(m.Recipient)
		}
	case protocol.CmdEnroll, protocol.CmdConversationStop, protocol.CmdRetire:
		s.broker.enrollment.Wake("")
	}
```

In `internal/broker/router.go:496` replace `go s.broker.Shutdown()` with `s.broker.owner.BeginShutdown(fmt.Errorf("stop requested by %s", s.name))` (add `fmt` import if missing). Delete `internal/broker/scheduler.go`.

- [ ] **Step 7: Update the broker test fixture and helpers**

`native_fixture_test.go`: implement `enrollment.Connector`/`enrollment.Driver` and add the stall gate used by the progress tests:

```go
type nativeFixture struct {
	mu           sync.Mutex
	blocked      map[string]bool
	opens        map[string]int
	closes       map[string]int
	probes       map[string]int
	submitted    chan messages.Envelope
	outcome      enrollment.Possession
	closeEntered chan struct{}
	closeGate    <-chan struct{}
	openGate     map[string]chan struct{} // stall Open for a conversation
	openEntered  chan string
	closeErr     map[string]error
}

func newNativeFixture() *nativeFixture {
	return &nativeFixture{blocked: make(map[string]bool), opens: make(map[string]int), closes: make(map[string]int), probes: make(map[string]int), submitted: make(chan messages.Envelope, config.NewMessagingConfig().ScanLimit), outcome: enrollment.Accepted, openGate: make(map[string]chan struct{}), closeErr: make(map[string]error)}
}

func (f *nativeFixture) Open(ctx context.Context, e messages.Enrollment) (enrollment.Driver, error) {
	f.mu.Lock()
	f.opens[e.ID]++
	gate, entered := f.openGate[e.Conversation], f.openEntered
	f.mu.Unlock()
	if gate != nil {
		if entered != nil {
			entered <- e.Conversation
		}
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &fixtureConnection{nativeFixture: f, target: e}, nil
}

func (c *fixtureConnection) Close() error {
	c.mu.Lock()
	c.closes[c.target.ID]++
	gate, entered := c.nativeFixture.closeGate, c.nativeFixture.closeEntered
	err := c.closeErr[c.target.ID]
	c.mu.Unlock()
	if c.wasSubmitted && gate != nil {
		entered <- struct{}{}
		<-gate
	}
	return err
}
```
Replace every `driver.` reference with `enrollment.` in this file and remove the `brokerstate` import. `Check` is unchanged.

`broker_test.go`: `newOwnedTestBroker` calls `newWithConnector(t.Context(), owner, cfg, config.NewNativeConfig(), newNativeFixture())`; `serveTestBroker` runs `b.Serve(ctx)` with a cancelable context and its returned func does `b.Shutdown(context.Background())` then `<-serving`; every `t.Cleanup(func(){ owner.Shutdown(...) })` becomes `owner.BeginShutdown(nil); owner.Wait(context.Background())`. Apply the same replacement in `ownership_test.go`, `scheduler_test.go`, `subscription_test.go`. In `scheduler_test.go` `TestSchedulerShutdownRetainsOwnershipUntilDriverCloseJoins` replace `b.owner.Shutdown(deadline)` with `b.owner.BeginShutdown(nil); err := b.owner.Wait(deadline)` and the expected error with `errors.Is(err, brokerstate.ErrShutdownIncomplete)`; the test's `defer shutdown()` must run after `close(gate)`, so move `defer close(gate)` above `defer shutdown()` ordering (defers run LIFO: register `shutdown` first, then `close(gate)`). Rename the file to `worker_test.go`.

- [ ] **Step 8: Write the progress tests (E1, E6)**

`internal/broker/progress_test.go`:

```go
package broker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/messages"
	"github.com/seungpyoson/waggle/internal/protocol"
)

func stallOpen(t *testing.T, fake *nativeFixture, conversation string) (release func()) {
	t.Helper()
	gate := make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	fake.mu.Lock()
	fake.openGate[conversation] = gate
	fake.openEntered = make(chan string, 4)
	fake.mu.Unlock()
	return release
}

// Design line 140: a blocked constructor delays only its enrollment; no
// observation barrier precedes dispatch to another already-bound recipient.
func TestBoundRecipientProgressesWhileAnotherAttachStalls(t *testing.T) {
	socket, b, shutdown := startTestBroker(t)
	fake := b.native()
	a := boundFixture(t, b, "sender")
	r := boundFixture(t, b, "recipient")
	release := stallOpen(t, fake, "stalled")
	defer shutdown()
	defer release()
	enrollFixture(t, b, "stalled")
	select {
	case <-fake.openEntered:
	case <-time.After(config.Defaults.StartupTimeout):
		t.Fatal("no worker attempted the stalled attachment")
	}
	c := connectClient(t, socket)
	defer c.Close()
	resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdSend, Credential: a.Credential, Recipient: r.Enrollment.ID, Message: "progress", IdempotencyKey: "progress", Hops: b.config.Messaging.DefaultHops})
	if !resp.OK {
		t.Fatal(resp.Error)
	}
	var stored messages.Message
	unmarshalResponse(t, resp, &stored)
	select {
	case e := <-fake.submitted:
		if e.Message.ID != stored.ID {
			t.Fatalf("unexpected submission %s", e.Message.ID)
		}
	case <-time.After(config.Defaults.StartupTimeout):
		t.Fatal("bound recipient made no native submission while an unrelated attach stalled")
	}
}

// An unresolved transport close is fatal by design: it begins draining and
// retains ownership. This test owns its broker directly because the shared
// fixture cleanup expects a clean release.
func TestUnresolvedCloseFailureRetainsOwnership(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	socket := shortBrokerSocketPath(t, "waggle-closefail-*")
	database := filepath.Join(t.TempDir(), "state.db")
	owner, err := brokerstate.Acquire(t.Context(), config.NewOwnershipConfig(database, config.CreateStore), statetest.Process{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Initialize(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	fake := newNativeFixture()
	b, err := newWithConnector(t.Context(), owner, config.NewBrokerConfig(config.BrokerEndpoints{Socket: socket, PID: socket + ".pid"}), config.NewNativeConfig(), fake)
	if err != nil {
		t.Fatal(err)
	}
	serving := make(chan error, 1)
	go func() { serving <- b.Serve(t.Context()) }()
	bad := boundFixture(t, b, "bad")
	boundFixture(t, b, "good")
	closeFailure := errors.New("native close failed")
	fake.mu.Lock()
	fake.closeErr[bad.Enrollment.ID] = closeFailure
	fake.mu.Unlock()
	c := connectClient(t, socket)
	defer c.Close()
	if resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdRetire, Recipient: bad.Enrollment.ID, Reason: "test"}); !resp.OK {
		t.Fatal(resp.Error)
	}
	deadline := time.After(config.Defaults.StartupTimeout)
	for {
		fake.mu.Lock()
		closes := fake.closes[bad.Enrollment.ID]
		fake.mu.Unlock()
		if closes == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("retired worker never closed its transport")
		case <-time.After(config.Defaults.StartupPollInterval):
		}
	}
	if err := <-serving; err != nil {
		t.Fatal(err)
	}
	err = b.Shutdown(context.Background())
	if !errors.Is(err, brokerstate.ErrFinalizationFailed) || !errors.Is(err, closeFailure) {
		t.Fatalf("unresolved close must retain ownership: %v", err)
	}
	if _, err := brokerstate.Acquire(t.Context(), config.NewOwnershipConfig(database, config.OpenStore), statetest.Process{}); !errors.Is(err, brokerstate.ErrOwnerAlive) {
		t.Fatalf("retained ownership allowed takeover: %v", err)
	}
}
```

Add `"path/filepath"` and `"github.com/seungpyoson/waggle/internal/brokerstate/statetest"` to the imports of `progress_test.go`. Add to `broker_test.go`: `func (b *Broker) native() *nativeFixture { return b.enrollment.Connector().(*nativeFixture) }` and to `supervisor.go`: `func (s *Supervisor) Connector() Connector { return s.connector }`. Replace every `b.native.(*nativeFixture)` in existing tests with `b.native()`.

- [ ] **Step 9: Build and run the broker package**

Run: `go build ./... && go vet ./... && go test -race -count=1 ./internal/broker/... 2>&1 | tail -30`
Expected: build and vet exit 0; all broker tests PASS including the two progress tests and the previously existing scheduler/subscription/messaging/ownership tests.

- [ ] **Step 10: Commit**

```bash
git add internal/config internal/messages internal/broker
git commit -m "feat(broker): enrollment workers replace the global scheduler

One owner-registered worker per observable enrollment owns its transport,
observes readiness, selects at most one message for its recipient and
persists the outcome. Deletes scheduler.go, subscriptions and nativeBatch.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HGU6FAQiaEWgLV3A2dRf6P"
```

---

### Task 4: Command supervision in `waggle start`

**Files:**
- Create: `cmd/supervise.go`, `cmd/supervise_test.go`
- Modify: `cmd/start.go:57-117`

**Interfaces:**
- Consumes: Task 1 owner API, Task 3 `broker.New(ctx, owner, cfg, provider)` and `Broker.Serve(ctx)`.
- Produces:
  ```go
  var ErrShutdownDeadlineMissed = errors.New("shutdown deadline missed; ownership retained while draining continues")
  func supervise(ctx context.Context, signals <-chan os.Signal, serve func(context.Context) error, owner *brokerstate.Owner, deadline time.Duration, report io.Writer) error
  ```

- [ ] **Step 1: Write the failing supervisor tests**

`cmd/supervise_test.go`:

```go
package cmd

import (
	"bytes"
	"context"
	"errors"
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
	var report bytes.Buffer
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
		case <-time.After(config.Defaults.StartupPollInterval):
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
	time.Sleep(config.Defaults.StartupPollInterval)
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
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -race -count=1 -run TestSupervise ./cmd/ 2>&1 | head`
Expected: compile error, `supervise` and `ErrShutdownDeadlineMissed` undefined.

- [ ] **Step 3: Write the supervisor**

`cmd/supervise.go`:

```go
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/seungpyoson/waggle/internal/brokerstate"
)

var ErrShutdownDeadlineMissed = errors.New("shutdown deadline missed; ownership retained while draining continues")

// supervise runs ingress and observes shutdown progress concurrently. The
// first signal (or an ingress end, or an RPC stop observed through Draining)
// begins orderly draining and starts the reporting deadline. A second signal
// escalates to interruption. The deadline never changes owner progress: the
// command reports it, keeps draining, and exits nonzero with the eventual
// result. A permanent finalization failure returns immediately without release.
func supervise(ctx context.Context, signals <-chan os.Signal, serve func(context.Context) error, owner *brokerstate.Owner, deadline time.Duration, report io.Writer) error {
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- serve(serveCtx) }()
	finalDone := make(chan error, 1)
	go func() { finalDone <- owner.Wait(context.Background()) }()
	draining := owner.Draining()
	var serveErr, missed, escalated error
	var timer <-chan time.Time
	begin := func(cause error) {
		owner.BeginShutdown(cause)
		if timer == nil {
			timer = time.After(deadline)
		}
		draining = nil
	}
	for {
		select {
		case sig := <-signals:
			if timer == nil {
				begin(fmt.Errorf("received %s", sig))
				continue
			}
			escalated = fmt.Errorf("shutdown escalated by second %s; active native calls interrupted", sig)
			fmt.Fprintln(report, escalated.Error())
			owner.Interrupt(escalated)
		case <-draining:
			begin(errors.New("stop requested"))
		case err := <-serveDone:
			serveDone = nil
			serveErr = err
			if err != nil {
				owner.Interrupt(err)
				begin(err)
			} else {
				begin(errors.New("ingress ended"))
			}
		case <-timer:
			timer = nil
			missed = ErrShutdownDeadlineMissed
			fmt.Fprintln(report, missed.Error())
		case err := <-finalDone:
			if err == nil {
				if missed != nil {
					fmt.Fprintln(report, "broker released after the missed shutdown deadline")
				}
			} else {
				fmt.Fprintln(report, err.Error())
			}
			return errors.Join(serveErr, err, missed, escalated)
		}
	}
}
```

In `cmd/start.go` replace lines 57-117 (from the `native.New` call to the end of RunE) with:

```go
		provider := config.ProviderConfig{CodexEndpoint: codexAppServer, ClientVersion: Version, Transport: config.NewNativeConfig()}
		if err := provider.Validate(); err != nil {
			return errors.Join(err, report(err))
		}
		projectID, err := config.ResolveProjectID(cmd.Context())
		if err != nil {
			return errors.Join(err, report(err))
		}
		paths = config.NewPaths(projectID)
		if paths.DataDir == "" {
			err := fmt.Errorf("cannot determine paths: HOME not set")
			return errors.Join(err, report(err))
		}
		if !foreground {
			daemonArgs := []string{os.Args[0], "start", "--foreground"}
			if initialize {
				daemonArgs = append(daemonArgs, "--initialize")
			}
			daemonArgs = append(daemonArgs, "--codex-app-server", codexAppServer)
			if err := broker.StartDaemon(paths.DataDir, filepath.Dir(paths.Socket), paths.Log, projectID, daemonArgs); err != nil {
				return err
			}
			printJSON(map[string]any{"ok": true, "message": "broker started"})
			return nil
		}
		if err := broker.EnsureDirs(paths.DataDir, filepath.Dir(paths.Socket)); err != nil {
			return errors.Join(err, report(err))
		}
		action := config.OpenStore
		if initialize {
			action = config.CreateStore
		}
		owner, err := brokerstate.Acquire(cmd.Context(), config.NewOwnershipConfig(paths.DB, action), brokerstate.OSProcessInspector{})
		if err != nil {
			return errors.Join(err, report(err))
		}
		// Startup failures before service release ownership under the same bounded wait.
		fail := func(err error) error {
			owner.BeginShutdown(err)
			waitCtx, cancel := context.WithTimeout(context.Background(), config.Defaults.ShutdownTimeout)
			defer cancel()
			return errors.Join(err, owner.Wait(waitCtx), report(err))
		}
		if initialize {
			if err := broker.Initialize(cmd.Context(), owner); err != nil {
				return fail(err)
			}
		}
		b, err := broker.New(cmd.Context(), owner, config.NewBrokerConfig(config.BrokerEndpoints{Socket: paths.Socket, PID: paths.PID}), provider)
		if err != nil {
			return fail(err)
		}
		if err := report(nil); err != nil {
			return fail(err)
		}
		signals := make(chan os.Signal, 2)
		signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(signals)
		return supervise(cmd.Context(), signals, b.Serve, owner, config.Defaults.ShutdownTimeout, cmd.ErrOrStderr())
	},
}
```
Remove the `native` import from `cmd/start.go` and add `"time"` if the compiler asks for it. Keep `TestStartRejectsInvalidNativeConfigurationBeforeCreatingState` passing: `provider.Validate()` still rejects a relative endpoint before any state is created.

- [ ] **Step 4: Run the cmd tests**

Run: `go test -race -count=1 ./cmd/ 2>&1 | tail -10`
Expected: `ok`, including the three supervisor tests and the existing help/degraded-state tests.

- [ ] **Step 5: Commit**

```bash
git add cmd/supervise.go cmd/supervise_test.go cmd/start.go
git commit -m "feat(start): supervise shutdown concurrently with ingress

Reports the shutdown deadline while draining continues, escalates on a
second signal, exits nonzero after a missed deadline, and returns a
permanent finalization failure without release.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HGU6FAQiaEWgLV3A2dRf6P"
```

---

### Task 5: Whole-module verification, degraded-state help, and docs

**Files:**
- Modify: `CLAUDE.md` "Code Structure" block (add `broker/enrollment/` line, remove nothing else)
- Modify: `docs/native-messaging-plan.md` lines 22 and 236-238 (record the user's R6-1 decision)

- [ ] **Step 1: Full build, vet, race suite**

Run:
```bash
go build -o /Volumes/Dev-OWC-Envoy/waggle-native-builds/work/build--native-messaging/reviews/independent-assessment-20260907/waggle-unit1 . && go vet ./... && go test -race -count=1 ./... 2>&1 | tee /Volumes/Dev-OWC-Envoy/waggle-native-builds/work/build--native-messaging/reviews/independent-assessment-20260907/unit1-full-race.log | tail -30
```
Expected: every package `ok`; `e2e_test.go` `TestE2E_TaskRoundTrip` passes (it builds the binary and starts a broker; if it fails on socket path length under the checkout, report it rather than changing paths).

- [ ] **Step 2: Degraded-state help**

Run from a non-repository directory on Dev Envoy with no PATH, no HOME network:
```bash
cd /Volumes/Dev-OWC-Envoy/waggle-native-builds/work/build--native-messaging/reviews/independent-assessment-20260907/gotmp && env -i HOME=/nonexistent PATH=/nonexistent /Volumes/Dev-OWC-Envoy/waggle-native-builds/work/build--native-messaging/reviews/independent-assessment-20260907/waggle-unit1 --help >/dev/null && for c in start stop status enroll send enqueue reply ack inbox whoami sessions retire conversation task lock unlock events install uninstall version; do env -i HOME=/nonexistent PATH=/nonexistent /Volumes/Dev-OWC-Envoy/waggle-native-builds/work/build--native-messaging/reviews/independent-assessment-20260907/waggle-unit1 $c --help >/dev/null || echo "FAIL $c"; done; echo done
```
Expected: `done` with no `FAIL` lines. (If a command name does not exist, drop it from the list; do not add commands.)

- [ ] **Step 3: Whitespace and docs**

Run: `git diff --check HEAD~4` → no output.
In `CLAUDE.md` under `internal/`, add after the `broker/` line:
```
│   └── enrollment/ # Owner-registered per-enrollment workers; provider mechanics under internal/
```
In `docs/native-messaging-plan.md` line 22, replace the sentence beginning "If the native credential scope is actually lost" through "not a guessed receipt." with:

```
If the native credential scope is lost, a later incarnation that the broker has natively verified as bound to the same provider conversation (exact Codex thread ID on the registered binding; exact Claude session ID and socket) inherits the unresolved barrier and may acknowledge the earlier attempt with its own credential after reading the envelope through the read-only inbox. No credential is minted for the old incarnation, no receipt is guessed, and no input is resubmitted. This successor receipt authority is the user's decision of 2026-09-07 (assessment finding R6-1); it replaces the previous permanent-block rule and remains an M1 proof obligation.
```
and in the paragraph at line 236 replace "Re-enrollment of the same provider conversation remains blocked across all historical incarnations and endpoint bindings until actual consumption/non-retention evidence arrives." with "A fresh supported registration for the same provider conversation whose current incarnation is not `bound` supersedes it (the predecessor is retired with reason `superseded` and its unsent queue is cancelled with an explicit report to senders); a `bound` predecessor still blocks. The successor binds already blocked by the unresolved message, diagnostics name it, and dispatch to the successor starts only after it is acknowledged or resolved by attempt-specific provider evidence." Update the `Status:` line to "revision 7" and describe the change in one clause.

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md docs/native-messaging-plan.md
git commit -m "docs: record successor receipt authority decision; document enrollment package

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HGU6FAQiaEWgLV3A2dRf6P"
```

- [ ] **Step 5: Report**

Write `/Volumes/Dev-OWC-Envoy/waggle-native-builds/work/build--native-messaging/reviews/independent-assessment-20260907/unit1-result.md` listing: commits (hashes), the full race-suite log path and its final summary lines, the help check output, and anything skipped or unconfirmed. Nothing in this unit enables a provider; say so explicitly.
