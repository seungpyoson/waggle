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

func TestSchedulerUsesOneSubscriptionUntilRetirement(t *testing.T) {
	_, b, shutdown := startTestBroker(t)
	defer shutdown()
	a := boundFixture(t, b, "sender")
	r := boundFixture(t, b, "recipient")
	fake := b.native.(*nativeFixture)
	ctx, cancel := context.WithTimeout(t.Context(), config.Defaults.StartupTimeout)
	defer cancel()
	tick := time.NewTicker(config.Defaults.StartupPollInterval)
	defer tick.Stop()
	for {
		fake.mu.Lock()
		probes, opens, closes := fake.probes[r.Enrollment.ID], fake.opens[r.Enrollment.ID], fake.closes[r.Enrollment.ID]
		fake.mu.Unlock()
		if opens != 1 || closes != 0 {
			t.Fatalf("observation replaced subscription: opens=%d closes=%d", opens, closes)
		}
		if probes >= 2 {
			break
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			t.Fatal("scheduler did not revisit enrollment")
		}
	}
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
	b.wakeup()
	for {
		fake.mu.Lock()
		opens, closes := fake.opens[r.Enrollment.ID], fake.closes[r.Enrollment.ID]
		fake.mu.Unlock()
		if opens != 1 || closes > 1 {
			t.Fatalf("dispatch replaced subscription: opens=%d closes=%d", opens, closes)
		}
		if closes == 1 {
			break
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			t.Fatal("retirement did not close subscription")
		}
	}
	if err := b.Shutdown(); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.closes[r.Enrollment.ID] != 1 || fake.closes[a.Enrollment.ID] != 1 {
		t.Fatal("shutdown repeated or skipped subscription cleanup")
	}
}
