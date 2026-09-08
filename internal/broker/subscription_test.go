package broker

import (
	"context"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/brokerstate/statetest"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/messages"
)

// awaitTransport polls one worker's fixture counters. Every wait gets its own
// deadline: a shared budget spanning several sequential waits would report a
// timeout for whichever step happened to run last, not for the step at fault.
func awaitTransport(t *testing.T, fake *nativeFixture, id, reason string, invariant, reached func(opens, closes, probes int) bool) {
	t.Helper()
	deadline := time.After(config.Defaults.StartupTimeout)
	tick := time.NewTicker(config.Defaults.ShutdownPollInterval)
	defer tick.Stop()
	for {
		fake.mu.Lock()
		opens, closes, probes := fake.opens[id], fake.closes[id], fake.probes[id]
		fake.mu.Unlock()
		if !invariant(opens, closes, probes) {
			t.Fatalf("%s: opens=%d closes=%d probes=%d", reason, opens, closes, probes)
		}
		if reached(opens, closes, probes) {
			return
		}
		select {
		case <-tick.C:
		case <-deadline:
			t.Fatalf("%s: opens=%d closes=%d probes=%d", reason, opens, closes, probes)
		}
	}
}

func TestWorkerUsesOneTransportUntilRetirement(t *testing.T) {
	_, b, shutdown := startTestBroker(t)
	defer shutdown()
	a := boundFixture(t, b, "sender")
	r := boundFixture(t, b, "recipient")
	fake := b.native()
	awaitTransport(t, fake, r.Enrollment.ID, "re-observation replaced the transport",
		func(opens, closes, _ int) bool { return opens == 1 && closes == 0 },
		func(_, _, probes int) bool { return probes >= 2 })
	if err := statetest.Write(b.owner, func(tx *brokerstate.WriteTx) error {
		_, err := messages.NewStore(tx, b.config.Messaging, time.Now()).Apply(messages.Enqueue{Credential: a.Credential, Recipient: r.Enrollment.ID, RequestID: "retained-subscription", Body: "one connection", Hops: b.config.Messaging.DefaultHops})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	fake.receive(t)
	if err := statetest.Write(b.owner, func(tx *brokerstate.WriteTx) error {
		_, err := messages.NewStore(tx, b.config.Messaging, time.Now()).Apply(messages.Retire{ID: r.Enrollment.ID, Reason: "test retirement"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	awaitTransport(t, fake, r.Enrollment.ID, "retirement did not close exactly one transport",
		func(opens, closes, _ int) bool { return opens == 1 && closes <= 1 },
		func(_, closes, _ int) bool { return closes == 1 })
	if err := b.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.closes[r.Enrollment.ID] != 1 || fake.closes[a.Enrollment.ID] != 1 {
		t.Fatal("shutdown repeated or skipped subscription cleanup")
	}
}
