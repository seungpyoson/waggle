package broker

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/driver"
	"github.com/seungpyoson/waggle/internal/messages"
)

// This fixture replaces only native mechanics. Broker construction, RPC,
// ownership, scheduler and persistence use their production entrypoints.
type nativeFixture struct {
	mu           sync.Mutex
	blocked      map[string]bool
	opens        map[string]int
	closes       map[string]int
	probes       map[string]int
	submitted    chan messages.Envelope
	outcome      driver.Possession
	closeEntered chan struct{}
	closeGate    <-chan struct{}
}

func newNativeFixture() *nativeFixture {
	return &nativeFixture{blocked: make(map[string]bool), opens: make(map[string]int), closes: make(map[string]int), probes: make(map[string]int), submitted: make(chan messages.Envelope, config.NewMessagingConfig().ScanLimit), outcome: driver.Accepted}
}
func (f *nativeFixture) Check(e messages.Enrollment) error {
	if e.Provider != "codex" || e.Conversation == "" || e.Endpoint != "/registered/app-server.sock" {
		return fmt.Errorf("unsupported fixture endpoint")
	}
	return nil
}
func (f *nativeFixture) Open(ctx context.Context, lifetime *brokerstate.Operation, e messages.Enrollment) (*driver.Handle, error) {
	return driver.Open(ctx, lifetime, func() (driver.Driver, error) {
		f.mu.Lock()
		f.opens[e.ID]++
		f.mu.Unlock()
		return &fixtureConnection{nativeFixture: f, target: e}, nil
	})
}

type fixtureConnection struct {
	*nativeFixture
	target       messages.Enrollment
	wasSubmitted bool
}

func (c *fixtureConnection) Probe(ctx context.Context) (driver.Readiness, error) {
	return c.nativeFixture.Probe(ctx, c.target)
}
func (c *fixtureConnection) Submit(ctx context.Context, envelope messages.Envelope) driver.Outcome {
	c.wasSubmitted = true
	return c.nativeFixture.Submit(ctx, c.target, envelope)
}
func (c *fixtureConnection) Close() error {
	c.mu.Lock()
	c.closes[c.target.ID]++
	gate, entered := c.nativeFixture.closeGate, c.nativeFixture.closeEntered
	c.mu.Unlock()
	if c.wasSubmitted && gate != nil {
		entered <- struct{}{}
		<-gate
	}
	return nil
}

func (f *nativeFixture) Probe(_ context.Context, e messages.Enrollment) (driver.Readiness, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probes[e.ID]++
	availability := driver.Idle
	if f.blocked[e.Conversation] {
		availability = driver.Unavailable
	}
	return driver.Readiness{Conversation: e.Conversation, Availability: availability, Evidence: "deterministic native input availability"}, nil
}
func (f *nativeFixture) Submit(ctx context.Context, _ messages.Enrollment, envelope messages.Envelope) driver.Outcome {
	select {
	case f.submitted <- envelope:
	case <-ctx.Done():
		return driver.Outcome{Possession: driver.NotSubmitted, Evidence: "fixture input cancelled before submission"}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return driver.Outcome{Possession: f.outcome, Evidence: "deterministic correlated native response", ProviderRef: envelope.Message.Attempt}
}
func (f *nativeFixture) block(conversation string, blocked bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blocked[conversation] = blocked
}
func (f *nativeFixture) receive(t *testing.T) messages.Envelope {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), config.Defaults.StartupTimeout)
	defer cancel()
	select {
	case e := <-f.submitted:
		return e
	case <-ctx.Done():
		t.Fatal("native fixture did not receive input")
		return messages.Envelope{}
	}
}
