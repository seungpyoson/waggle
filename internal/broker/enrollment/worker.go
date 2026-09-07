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
		case <-timer.C:
		}
		// Both sources use the same floor. A pending timer and coalesced wake
		// cannot produce adjacent checks, and further wakes cannot restart this
		// fixed wait or postpone the check indefinitely.
		if wait := time.Until(w.lastCheck.Add(w.limits.QueueCheckInterval)); wait > 0 {
			select {
			case <-lifetime.Stop:
				return
			case <-time.After(wait):
			}
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
			if cause := lifetime.Interrupt.Err(); cause != nil && errors.Is(err, cause) && !errors.Is(err, driver.ErrCloseFailed) {
				return
			}
			reportFatal(err)
			return
		}
		timer.Reset(w.limits.IdleCheckInterval)
	}
}

// check is one admitted workflow: snapshot the enrollment and whether work may
// be queued, observe readiness, persist only changed evidence, select at most
// one message for this recipient, submit it, persist the outcome. An idle bound
// worker never leaves the snapshot. done reports a terminal enrollment.
func (w *worker) check(lifetime brokerstate.WorkLifetime) (done bool, err error) {
	err = w.owner.Do(lifetime.Interrupt, func(op *brokerstate.Operation) error {
		var e messages.Enrollment
		var pending bool
		if err := op.Read(lifetime.Interrupt, func(tx *brokerstate.ReadTx) error {
			view := messages.NewView(tx, w.limits, time.Now())
			var err error
			if e, err = view.Enrollment(w.id); err != nil {
				return err
			}
			if e.State != "bound" {
				return nil
			}
			pending, err = view.Pending(w.id)
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
		observed := false
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
				observed = true
			}
			if !w.bound {
				return nil
			}
		}
		// The snapshot is a hint, never the decision. Enter the intent
		// transaction when it saw queued work, or when this check just changed
		// canonical readiness and so may have made queued work eligible; that
		// transaction rechecks every constraint under its own fence.
		if !pending && !observed {
			return nil
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
		err = op.Write(context.WithoutCancel(lifetime.Interrupt), func(tx *brokerstate.WriteTx) error {
			_, err := messages.NewStore(tx, w.limits, time.Now()).Apply(messages.Observe{
				Attempt: d.Envelope.Message.Attempt, Recipient: w.id,
				Kind: kind, ProviderRef: outcome.ProviderRef, Evidence: outcome.Evidence,
			})
			return err
		})
		if errors.Is(err, messages.ErrConflict) {
			// The canonical store rejected contradictory evidence: the recipient's
			// own authenticated receipt already decided this attempt while the call
			// was in flight. Its decision stands and the intent is never resubmitted.
			return nil
		}
		return err
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
		if errors.Is(err, driver.ErrCloseFailed) {
			return nil, err
		}
		var fatal *fatalError
		if errors.As(err, &fatal) {
			return nil, fatal.err
		}
		if errors.Is(err, errBackoff) {
			return nil, nil // nothing observed; nothing to write
		}
		// A call the owner cancelled itself is the owner's own signal. Recording
		// it would attribute the broker's shutdown to the provider.
		if cause := lifetime.Interrupt.Err(); cause != nil && errors.Is(err, cause) {
			return nil, err
		}
		evidence := "native readiness verification failed before input submission: " + err.Error()
		if e.State != "bound" && e.Evidence == evidence {
			return nil, nil // unchanged observation: no write
		}
		return messages.Unavailable{ID: e.ID, Evidence: evidence}, nil
	}
	if view.Conversation != e.Conversation || view.Evidence == "" {
		return w.unusable(e, "native readiness uncorrelated with the enrolled conversation")
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
	return w.unusable(e, "native readiness reported an invalid availability")
}

// unusable turns a readiness answer this worker cannot act on into an
// observation of unavailability. A provider that answers for another thread, or
// with a value outside the pinned contract, is a provider-side condition, not a
// canonical persistence, fence or unresolved-close failure: the enrollment
// becomes disconnected with diagnostics and the broker keeps serving. The
// transport is dropped under the same reconnect backoff a failed probe uses, so
// a persistent anomaly cannot become a reconnect loop.
func (w *worker) unusable(e messages.Enrollment, evidence string) (messages.Command, error) {
	w.bound = false
	if err := w.dropTransport("an unusable readiness answer"); err != nil {
		return nil, err
	}
	if e.State != "bound" && e.Evidence == evidence {
		return nil, nil // unchanged observation: no write
	}
	return messages.Unavailable{ID: e.ID, Evidence: evidence}, nil
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
		if closeErr := w.dropTransport("a failed probe"); closeErr != nil {
			return driver.Readiness{}, &fatalError{closeErr}
		}
	}
	return view, err
}

// dropTransport applies the reconnect backoff and joins this worker's transport
// after an anomaly. It is the single path back to reconnection, so no anomaly
// can reopen sooner than a failed probe would. An unresolved close is the only
// fatal outcome: the owner retains ownership rather than claiming quiescence.
func (w *worker) dropTransport(reason string) error {
	w.nextOpen = time.Now().Add(w.limits.ReconnectBackoff)
	if w.native == nil {
		return nil
	}
	err := w.native.Close()
	w.native = nil
	if err != nil {
		return fmt.Errorf("close native transport for %s after %s: %w", w.id, reason, err)
	}
	return nil
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
