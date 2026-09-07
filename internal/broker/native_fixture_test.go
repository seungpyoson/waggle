package broker

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/seungpyoson/waggle/internal/broker/enrollment"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/messages"
)

// This fixture replaces only native mechanics. Broker construction, RPC,
// ownership, enrollment workers and persistence use their production entrypoints.
type nativeFixture struct {
	mu           sync.Mutex
	blocked      map[string]bool
	opens        map[string]int
	closes       map[string]int
	probes       map[string]int
	submitted    chan messages.Envelope
	outcome      enrollment.Possession
	closeEntered chan struct{}
	closeGate    <-chan struct{}
	openGate     map[string]chan struct{} // stall Open for a conversation
	openEntered  chan string
	closeErr     map[string]error
}

func newNativeFixture() *nativeFixture {
	return &nativeFixture{blocked: make(map[string]bool), opens: make(map[string]int), closes: make(map[string]int), probes: make(map[string]int), submitted: make(chan messages.Envelope, config.NewMessagingConfig().ScanLimit), outcome: enrollment.Accepted, openGate: make(map[string]chan struct{}), closeErr: make(map[string]error)}
}

func (f *nativeFixture) Check(e messages.Enrollment) error {
	if e.Provider != "codex" || e.Conversation == "" || e.Endpoint != "/registered/app-server.sock" {
		return fmt.Errorf("unsupported fixture endpoint")
	}
	return nil
}

func (f *nativeFixture) Open(ctx context.Context, e messages.Enrollment) (enrollment.Driver, error) {
	f.mu.Lock()
	f.opens[e.ID]++
	gate, entered := f.openGate[e.Conversation], f.openEntered
	f.mu.Unlock()
	if gate != nil {
		if entered != nil {
			entered <- e.Conversation
		}
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &fixtureConnection{nativeFixture: f, target: e}, nil
}

type fixtureConnection struct {
	*nativeFixture
	target       messages.Enrollment
	wasSubmitted bool
}

func (c *fixtureConnection) Probe(ctx context.Context) (enrollment.Readiness, error) {
	return c.nativeFixture.Probe(ctx, c.target)
}

func (c *fixtureConnection) Submit(ctx context.Context, envelope messages.Envelope) enrollment.Outcome {
	c.wasSubmitted = true
	return c.nativeFixture.Submit(ctx, c.target, envelope)
}

func (c *fixtureConnection) Close() error {
	c.mu.Lock()
	c.closes[c.target.ID]++
	gate, entered := c.nativeFixture.closeGate, c.nativeFixture.closeEntered
	err := c.closeErr[c.target.ID]
	c.mu.Unlock()
	if c.wasSubmitted && gate != nil {
		entered <- struct{}{}
		<-gate
	}
	return err
}

func (f *nativeFixture) Probe(_ context.Context, e messages.Enrollment) (enrollment.Readiness, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probes[e.ID]++
	availability := enrollment.Idle
	if f.blocked[e.Conversation] {
		availability = enrollment.Unavailable
	}
	return enrollment.Readiness{Conversation: e.Conversation, Availability: availability, Evidence: "deterministic native input availability"}, nil
}

func (f *nativeFixture) Submit(ctx context.Context, _ messages.Enrollment, envelope messages.Envelope) enrollment.Outcome {
	select {
	case f.submitted <- envelope:
	case <-ctx.Done():
		return enrollment.Outcome{Possession: enrollment.NotSubmitted, Evidence: "fixture input cancelled before submission"}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return enrollment.Outcome{Possession: f.outcome, Evidence: "deterministic correlated native response", ProviderRef: envelope.Message.Attempt}
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
