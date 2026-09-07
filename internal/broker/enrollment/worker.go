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
			// An owner interruption cancels admitted native I/O and canonical
			// reads. That cancellation is the owner's own signal, not a new
			// failure to report back to it as fatal.
			if cause := lifetime.Interrupt.Err(); cause != nil && errors.Is(err, cause) {
				return
			}
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
