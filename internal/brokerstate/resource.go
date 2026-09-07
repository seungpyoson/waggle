package brokerstate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Resource is an owner-minted handle to an external effect. Its creating
// operation owns its lifetime; each use also requires an admitted operation
// from that same owner. Copies share close and expiry state.
type Resource struct{ state *resourceState }

type resourceState struct {
	mu       sync.Mutex
	lifetime *operationState
	closer   io.Closer
	closed   bool
	closeErr error
}

// Open fences before opening and registers the resource before returning.
// A failed factory must join everything it started. A successful factory's
// Close must join its readers and callbacks, even if cancellation is ignored.
func (op *Operation) Open(ctx context.Context, open func() (io.Closer, error)) (*Resource, error) {
	if op == nil || op.state == nil || open == nil {
		return nil, ErrExpiredCapability
	}
	s := op.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return nil, ErrExpiredCapability
	}
	if err := s.owner.transaction(ctx, func(*WriteTx) error { return nil }); err != nil {
		return nil, err
	}
	closer, err := open()
	if err != nil {
		return nil, err
	}
	if closer == nil {
		return nil, fmt.Errorf("owned resource factory returned no resource")
	}
	r := &Resource{state: &resourceState{lifetime: s, closer: closer}}
	s.resources[r] = struct{}{}
	return r, nil
}

// Use serializes effects with Close. The generation check ends before external
// I/O; the admitted resource lifetime remains held until that I/O and Close join.
func (r *Resource) Use(ctx context.Context, op *Operation, effect func() error) error {
	if r == nil || r.state == nil || op == nil || op.state == nil || effect == nil {
		return ErrExpiredCapability
	}
	s := r.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.lifetime.owner != op.state.owner {
		return ErrExpiredCapability
	}
	if err := op.Write(ctx, func(*WriteTx) error { return nil }); err != nil {
		return err
	}
	return effect()
}

func (r *Resource) Close() error {
	if r == nil || r.state == nil {
		return ErrExpiredCapability
	}
	s := r.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		s.closeErr = s.closer.Close()
		if s.closeErr != nil {
			s.lifetime.owner.mu.Lock()
			s.lifetime.owner.resourceErr = errors.Join(s.lifetime.owner.resourceErr, s.closeErr)
			s.lifetime.owner.mu.Unlock()
		}
		s.lifetime.mu.Lock()
		delete(s.lifetime.resources, r)
		s.lifetime.mu.Unlock()
	}
	return s.closeErr
}
