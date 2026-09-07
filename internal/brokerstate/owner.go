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
	s.recordFatal(err)
	s.interruptWith(err)
}

// recordFatal stores a permanent failure and unblocks waiters without entering
// the one-shot shutdown transition. beginShutdown reports its own ingress
// failure through this half: routing that through reportFatal would re-enter
// shutdownOnce.Do from inside its own function and deadlock every caller.
func (s *ownerState) recordFatal(err error) {
	s.mu.Lock()
	s.fatal = errors.Join(s.fatal, err)
	s.mu.Unlock()
	s.failOnce.Do(func() { close(s.failed) })
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
		// Listener close happens outside the lifecycle mutex. Draining has
		// already begun here, so the failure is recorded and interrupts native
		// I/O directly instead of re-entering this one-shot transition.
		if err := s.closeListener(); err != nil {
			failure := fmt.Errorf("close broker ingress: %w", err)
			s.recordFatal(failure)
			s.cancel(failure)
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
