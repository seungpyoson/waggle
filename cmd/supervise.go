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

// releaseOutcome separates a released store whose local handle did not close
// from ownership the broker still holds. Only the latter is an exit failure:
// after a released row there is nothing for an operator to recover, so the
// close is warned about and the command still succeeds.
func releaseOutcome(err error, report io.Writer) error {
	if errors.Is(err, brokerstate.ErrStoreCloseFailed) {
		fmt.Fprintln(report, err.Error())
		return nil
	}
	return err
}

// supervise runs ingress and observes shutdown progress concurrently. The
// first signal (or an ingress end, an RPC stop observed through Draining, or
// cancellation of the parent context) begins orderly draining exactly once and
// starts the reporting deadline. A second signal
// escalates to interruption. The deadline never changes owner progress: the
// command reports it, keeps draining, and exits nonzero with the eventual
// result. A permanent finalization failure returns immediately without release.
func supervise(ctx context.Context, signals <-chan os.Signal, serve func(context.Context) error, owner *brokerstate.Owner, deadline time.Duration, report io.Writer) error {
	serveDone := make(chan error, 1)
	go func() { serveDone <- serve(ctx) }()
	finalDone := make(chan error, 1)
	go func() { finalDone <- owner.Wait(context.Background()) }()
	draining := owner.Draining()
	parent := ctx.Done()
	var serveErr, missed, escalated error
	var timer <-chan time.Time
	// begun stays true once draining starts, so escalation stays available
	// after the deadline has already fired and cleared its timer.
	begun := false
	begin := func(cause error) {
		owner.BeginShutdown(cause)
		if !begun {
			begun = true
			timer = time.After(deadline)
		}
		draining, parent = nil, nil
	}
	for {
		select {
		case <-parent:
			begin(ctx.Err())
		case sig := <-signals:
			if !begun {
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
			err = releaseOutcome(err, report)
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
