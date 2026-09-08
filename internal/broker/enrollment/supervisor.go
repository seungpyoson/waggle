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

// Connector reports the provider mechanics this supervision was started with.
// Enrollment admission checks provider support through it; it is never a
// transport and never opens one.
func (s *Supervisor) Connector() Connector { return s.connector }

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
			// An owner interruption is the owner's own signal, not a discovery
			// failure to report back to it as fatal.
			if cause := lifetime.Interrupt.Err(); cause != nil && errors.Is(err, cause) {
				return
			}
			reportFatal(fmt.Errorf("enrollment discovery: %w", err))
			return
		}
	}
}

// discover expires elapsed deadlines at most once, then pages observable
// enrollments under read snapshots and registers absent workers. A key still
// registered with the owner (joined but not yet completed) is skipped until its
// completion commits; no second worker can exist for an enrollment.
func (s *Supervisor) discover(lifetime brokerstate.WorkLifetime) error {
	if err := s.expire(lifetime); err != nil {
		return err
	}
	var after string
	for {
		var page []messages.Enrollment
		if err := s.owner.Do(lifetime.Interrupt, func(op *brokerstate.Operation) error {
			return op.Read(lifetime.Interrupt, func(tx *brokerstate.ReadTx) error {
				var err error
				page, err = messages.NewView(tx, s.limits, time.Now()).Enrollments(after, messages.ObservableEnrollments)
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

// expire runs the deadline transition once per discovery pass, and only when a
// read snapshot reports an elapsed row. The snapshot is a hint, never the
// decision: Expire reapplies both predicates under its own fence.
func (s *Supervisor) expire(lifetime brokerstate.WorkLifetime) error {
	var expirable bool
	if err := s.owner.Do(lifetime.Interrupt, func(op *brokerstate.Operation) error {
		return op.Read(lifetime.Interrupt, func(tx *brokerstate.ReadTx) error {
			var err error
			expirable, err = messages.NewView(tx, s.limits, time.Now()).Expirable()
			return err
		})
	}); err != nil {
		return err
	}
	if !expirable {
		return nil
	}
	return s.owner.Do(lifetime.Interrupt, func(op *brokerstate.Operation) error {
		return op.Write(lifetime.Interrupt, func(tx *brokerstate.WriteTx) error {
			_, err := messages.NewStore(tx, s.limits, time.Now()).Apply(messages.Expire{})
			return err
		})
	})
}

func (s *Supervisor) register(lifetime brokerstate.WorkLifetime, id string) error {
	w := &worker{owner: s.owner, connector: s.connector, limits: s.limits, transport: s.transport, id: id, wake: make(chan struct{}, 1)}
	s.mu.Lock()
	if _, present := s.workers[id]; present {
		s.mu.Unlock()
		return nil
	}
	// Publish before registering: a worker that joins immediately must find
	// its own index entry to remove, never a later one written behind it.
	s.workers[id] = w
	s.mu.Unlock()
	err := s.owner.StartWork(lifetime.Interrupt, "enrollment:"+id, brokerstate.ManagedWork{
		Run: func(lifetime brokerstate.WorkLifetime, reportFatal func(error)) {
			defer s.forget(id, w)
			w.run(lifetime, reportFatal)
		},
		Complete: w.complete,
	})
	if err != nil {
		s.forget(id, w)
		if errors.Is(err, brokerstate.ErrDuplicateWork) {
			return nil // previous worker joined but its completion has not committed yet
		}
		return err
	}
	s.Wake(id)
	return nil
}

// forget removes only this incarnation's index entry. The map is a resource
// index, never a readiness ledger.
func (s *Supervisor) forget(id string, w *worker) {
	s.mu.Lock()
	if s.workers[id] == w {
		delete(s.workers, id)
	}
	s.mu.Unlock()
}
