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
	// begun stays true once draining starts, so escalation stays available
	// after the deadline has already fired and cleared its timer.
	begun := false
	begin := func(cause error) {
		owner.BeginShutdown(cause)
		if !begun {
			begun = true
			timer = time.After(deadline)
		}
		draining = nil
	}
	for {
		select {
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
