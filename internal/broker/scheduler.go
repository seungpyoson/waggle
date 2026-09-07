package broker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/driver"
	"github.com/seungpyoson/waggle/internal/messages"
)

// Wakeups carry no work or receipt state. The periodic scan is necessary even
// when a commit's notification is lost or the broker restarts.
func (b *Broker) wakeup() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

func (b *Broker) schedule(ctx context.Context) error {
	return b.owner.Do(ctx, func(lifetime *brokerstate.Operation) error {
		subscriptions := &subscriptions{handles: make(map[string]*driver.Handle), lifetime: lifetime}
		return b.scheduleOwned(ctx, subscriptions)
	})
}

func (b *Broker) scheduleOwned(ctx context.Context, subscriptions *subscriptions) error {
	tick := time.NewTicker(b.config.Messaging.ScanPeriod)
	defer tick.Stop()
	var after string // keyset position for a bounded observation scan, not routing state
	b.wakeup()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-b.stopCh:
			return nil
		case <-tick.C:
		case <-b.wake:
		}
		err := b.owner.Do(ctx, func(op *brokerstate.Operation) error {
			var page []messages.Enrollment
			if err := op.Write(ctx, func(tx *brokerstate.WriteTx) error {
				s := messages.NewStore(tx, b.config.Messaging, time.Now())
				if _, err := s.Apply(messages.Expire{}); err != nil {
					return err
				}
				var err error
				page, err = s.Enrollments(after, messages.ObservableEnrollments)
				return err
			}); err != nil {
				return err
			}
			if err := subscriptions.reap(ctx, op, b); err != nil {
				return err
			}
			if err := nativeBatch(page, func(e messages.Enrollment) error { return b.observeEnrollment(ctx, op, subscriptions, e) }); err != nil {
				return err
			}
			after = ""
			if len(page) == b.config.Messaging.ScanLimit {
				after = page[len(page)-1].ID
			}
			var selected messages.Result
			if err := op.Write(ctx, func(tx *brokerstate.WriteTx) error {
				var err error
				selected, err = messages.NewStore(tx, b.config.Messaging, time.Now()).Apply(messages.Select{})
				return err
			}); err != nil {
				return err
			}
			// Each admitted attempt spans selection, native input, and persistence.
			// Subscriptions belong to the enclosing owner lifetime.
			return nativeBatch(selected.Dispatches, func(d messages.Dispatch) error { return b.submit(ctx, op, subscriptions, d) })
		})
		if errors.Is(err, brokerstate.ErrAdmissionClosed) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("native scheduler: %w", err)
		}
	}
}

// Each input is an immutable member of a database-bounded batch. This starts
// no secondary queue and returns only after every invocation (including close)
// has joined. Per-recipient barriers are reserved in SQLite before this point.
func nativeBatch[T any](items []T, work func(T) error) error {
	errs := make([]error, len(items))
	var joined sync.WaitGroup
	for i, item := range items {
		joined.Add(1)
		go func() { defer joined.Done(); errs[i] = work(item) }()
	}
	joined.Wait()
	return errors.Join(errs...)
}

func (b *Broker) observeEnrollment(ctx context.Context, op *brokerstate.Operation, subscriptions *subscriptions, e messages.Enrollment) (result error) {
	switch e.State {
	case "failed", "retired":
		return nil // Terminal enrollments cannot be reactivated.
	case "pending", "bound", "disconnected":
	default:
		return fmt.Errorf("invalid canonical enrollment state for %s", e.ID)
	}
	native, err := subscriptions.observe(ctx, b, e)
	var view driver.Readiness
	if err == nil {
		view, err = native.Probe(ctx, op)
	}
	var command messages.Command
	if errors.Is(err, brokerstate.ErrFenced) || errors.Is(err, brokerstate.ErrExpiredCapability) {
		return err
	}
	if err != nil {
		if closeErr := subscriptions.remove(e.ID); closeErr != nil {
			return closeErr
		}
		// Transport failure establishes unavailable verification, never native
		// withdrawal. Provider text is excluded from canonical diagnostics.
		command = messages.Unavailable{ID: e.ID, Evidence: "native readiness verification failed before input submission: " + err.Error()}
	} else {
		if view.Conversation != e.Conversation || view.Evidence == "" {
			return fmt.Errorf("native driver returned uncorrelated readiness for %s", e.ID)
		}
		switch view.Availability {
		case driver.Idle, driver.Busy:
			command = messages.Bind{ID: e.ID, Conversation: e.Conversation, Endpoint: e.Endpoint, Evidence: view.Evidence}
		case driver.Unavailable:
			command = messages.Unavailable{ID: e.ID, Evidence: view.Evidence}
		default:
			return fmt.Errorf("native driver returned invalid availability for %s", e.ID)
		}
	}
	err = op.Write(context.WithoutCancel(ctx), func(tx *brokerstate.WriteTx) error {
		_, err := messages.NewStore(tx, b.config.Messaging, time.Now()).Apply(command)
		return err
	})
	if errors.Is(err, messages.ErrConflict) {
		// Retirement or expiry may win while the native probe is in flight.
		// Reject that stale observation without undoing the terminal state.
		log.Printf("rejected stale native readiness for incarnation %s: %v", e.ID, err)
		return subscriptions.remove(e.ID)
	}
	return err
}

func (b *Broker) submit(ctx context.Context, op *brokerstate.Operation, subscriptions *subscriptions, d messages.Dispatch) error {
	native, err := subscriptions.forSubmission(d.Recipient.ID)
	if err != nil {
		return err
	} // The committed intent stays uncertain on a broken lifecycle.
	outcome, err := native.Submit(ctx, op, d.Envelope)
	if err != nil {
		return err
	}

	var kind string
	switch outcome.Possession {
	case driver.NotSubmitted:
		kind = "not_submitted"
	case driver.Accepted:
		kind = "accepted"
	case driver.Held:
		kind = "held"
	case driver.Refused:
		kind = "refused"
	case driver.Uncertain:
		kind = "uncertain"
	default:
		return fmt.Errorf("native driver returned invalid possession for attempt %s; intent remains uncertain", d.Envelope.Message.Attempt)
	}
	// Cancelling native I/O must not also cancel persistence of what was
	// observed. Write has its own bounded transaction lifetime and fencing.
	return op.Write(context.WithoutCancel(ctx), func(tx *brokerstate.WriteTx) error {
		_, err := messages.NewStore(tx, b.config.Messaging, time.Now()).Apply(messages.Observe{
			Attempt: d.Envelope.Message.Attempt, Recipient: d.Recipient.ID,
			Kind: kind, ProviderRef: outcome.ProviderRef, Evidence: outcome.Evidence,
		})
		return err
	})
}

// subscriptions contains resources only. Enrollment, availability, and ordering
// remain canonical database state. Observation creates an attachment once;
// submission must use that attachment and cannot open a replacement.
type subscriptions struct {
	mu       sync.Mutex
	handles  map[string]*driver.Handle
	lifetime *brokerstate.Operation
}

func (s *subscriptions) observe(ctx context.Context, b *Broker, e messages.Enrollment) (*driver.Handle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h, attached := s.handles[e.ID]; attached {
		return h, nil
	}
	if len(s.handles) >= b.config.Messaging.EnrollmentCapacity {
		return nil, fmt.Errorf("native subscription capacity invariant violated")
	}
	h, err := b.native.Open(ctx, s.lifetime, e)
	if err != nil {
		return nil, err
	}
	s.handles[e.ID] = h
	return h, nil
}

func (s *subscriptions) forSubmission(id string) (*driver.Handle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, attached := s.handles[id]
	if !attached {
		return nil, fmt.Errorf("selected incarnation %s has no owned subscription; intent remains uncertain", id)
	}
	return h, nil
}

func (s *subscriptions) remove(id string) error {
	s.mu.Lock()
	h, attached := s.handles[id]
	delete(s.handles, id)
	s.mu.Unlock()
	if !attached {
		return nil
	}
	return h.Close()
}

func (s *subscriptions) reap(ctx context.Context, op *brokerstate.Operation, b *Broker) error {
	s.mu.Lock()
	ids := make([]string, 0, len(s.handles))
	for id := range s.handles {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	var terminal []string
	if err := op.Write(ctx, func(tx *brokerstate.WriteTx) error {
		store := messages.NewStore(tx, b.config.Messaging, time.Now())
		for _, id := range ids {
			e, err := store.Enrollment(id)
			if err != nil {
				return err
			}
			switch e.State {
			case "failed", "retired":
				terminal = append(terminal, id)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return nativeBatch(terminal, s.remove)
}
