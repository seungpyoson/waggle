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
	drained        chan struct{}
	stopping       chan struct{}
	shutdownOnce   sync.Once
	shutdownDone   chan struct{}
	shutdownErr    error
	resourceErr    error
	endpoint       *ownedEndpoint
	serviceStarted bool
	predecessor    *ProcessIdentity
	inspector      ProcessInspector
}

// Operation is valid only during its Do callback. A scheduler keeps one
// operation across intent commit, native submit and observation persistence.
// Do callbacks must join work they start before returning.
type Operation struct{ state *operationState }

type operationState struct {
	owner     *ownerState
	mu        sync.Mutex
	active    bool
	resources map[*Resource]struct{}
}

// Do atomically admits work with respect to shutdown. It never holds a SQL
// transaction while invoking work, so provider I/O cannot hold SQLite locks.
func (o *Owner) Do(ctx context.Context, work func(*Operation) error) (result error) {
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
	op := &Operation{state: &operationState{owner: s, active: true, resources: make(map[*Resource]struct{})}}
	defer func() {
		// Serialize invalidation with any transaction already using this operation.
		op.state.mu.Lock()
		op.state.active = false
		resources := make([]*Resource, 0, len(op.state.resources))
		for resource := range op.state.resources {
			resources = append(resources, resource)
		}
		op.state.mu.Unlock()
		// Close joins provider readers and callbacks before admission drains.
		// Cancellation cannot stand in for completion of this work.
		for _, resource := range resources {
			result = errors.Join(result, resource.Close())
		}
		s.mu.Lock()
		s.operations--
		if s.phase == draining && s.operations == 0 {
			close(s.drained)
		}
		s.mu.Unlock()
	}()
	return work(op)
}

// Stopping closes when admission closes. It is an observation, not proof that
// admitted work or a native provider has stopped.
func (o *Owner) Stopping() (<-chan struct{}, error) {
	if o == nil || o.state == nil {
		return nil, ErrExpiredCapability
	}
	return o.state.stopping, nil
}

// Shutdown closes ingress, drains admitted work, removes owned endpoints, and
// releases ownership last. Endpoint cleanup cannot be supplied by callers.
// A deadline bounds the wait even when admitted work ignores cancellation.
func (o *Owner) Shutdown(ctx context.Context) error {
	if o == nil || o.state == nil {
		return ErrExpiredCapability
	}
	s := o.state
	s.shutdownOnce.Do(func() {
		s.mu.Lock()
		s.phase = draining
		close(s.stopping)
		if s.operations == 0 {
			close(s.drained)
		}
		s.mu.Unlock()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.config.ShutdownTimeout)
		go func() {
			defer close(s.shutdownDone)
			defer cancel()
			s.shutdownErr = s.finishShutdown(shutdownCtx)
		}()
	})
	waitCtx, cancel := context.WithTimeout(ctx, s.config.ShutdownTimeout)
	defer cancel()
	select {
	case <-s.shutdownDone:
		return s.shutdownErr
	case <-waitCtx.Done():
		return fmt.Errorf("broker shutdown incomplete; ownership retained: %w", waitCtx.Err())
	}
}

func (s *ownerState) finishShutdown(ctx context.Context) error {
	if err := s.closeListener(); err != nil {
		return fmt.Errorf("close broker ingress; ownership retained: %w", err)
	}
	select {
	case <-s.drained:
	case <-ctx.Done():
		return fmt.Errorf("drain broker; ownership retained: %w", ctx.Err())
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	resourceErr := s.resourceErr
	s.mu.Unlock()
	if resourceErr != nil {
		return fmt.Errorf("resource cleanup failed; ownership retained: %w", resourceErr)
	}
	if err := s.transaction(ctx, func(tx *WriteTx) error {
		// Endpoint cleanup is fenced too. A stale handle must fail before it
		// can touch a successor's files, not merely when releasing the row.
		if err := s.removeEndpoints(); err != nil {
			return fmt.Errorf("cleanup broker; ownership retained: %w", err)
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
		return fmt.Errorf("release broker ownership: %w", err)
	}
	s.mu.Lock()
	s.phase = released
	s.mu.Unlock()
	return s.db.Close()
}
