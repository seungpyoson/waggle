package broker

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/brokerstate/statetest"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/messages"
	"github.com/seungpyoson/waggle/internal/protocol"
)

func stallOpen(t *testing.T, fake *nativeFixture, conversation string) (release func()) {
	t.Helper()
	gate := make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	fake.mu.Lock()
	fake.openGate[conversation] = gate
	fake.openEntered = make(chan string, 4)
	fake.mu.Unlock()
	return release
}

// Design line 140: a blocked constructor delays only its enrollment; no
// observation barrier precedes dispatch to another already-bound recipient.
func TestBoundRecipientProgressesWhileAnotherAttachStalls(t *testing.T) {
	socket, b, shutdown := startTestBroker(t)
	fake := b.native()
	a := boundFixture(t, b, "sender")
	r := boundFixture(t, b, "recipient")
	release := stallOpen(t, fake, "stalled")
	defer shutdown()
	defer release()
	enrollFixture(t, b, "stalled")
	select {
	case <-fake.openEntered:
	case <-time.After(config.Defaults.StartupTimeout):
		t.Fatal("no worker attempted the stalled attachment")
	}
	c := connectClient(t, socket)
	defer c.Close()
	resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdSend, Credential: a.Credential, Recipient: r.Enrollment.ID, Message: "progress", IdempotencyKey: "progress", Hops: b.config.Messaging.DefaultHops})
	if !resp.OK {
		t.Fatal(resp.Error)
	}
	var stored messages.Message
	unmarshalResponse(t, resp, &stored)
	select {
	case e := <-fake.submitted:
		if e.Message.ID != stored.ID {
			t.Fatalf("unexpected submission %s", e.Message.ID)
		}
	case <-time.After(config.Defaults.StartupTimeout):
		t.Fatal("bound recipient made no native submission while an unrelated attach stalled")
	}
}

// Draining closes ingress and every accepted connection with it. A stop
// request that drained inside its own RPC would therefore destroy the
// connection carrying its acknowledgement, and every operator stop would
// report a transport failure for a broker that actually stopped.
// Draining inside the RPC loses the reply as a race rather than always, so one
// round trip can pass by luck. Repeating makes the regression certain to show.
func TestStopRequestIsAcknowledgedBeforeIngressCloses(t *testing.T) {
	for i := range 10 {
		func() {
			socket, b, shutdown := startTestBroker(t)
			defer shutdown()
			c := connectClient(t, socket)
			defer c.Close()
			resp, err := c.Send(protocol.Request{Cmd: protocol.CmdStop})
			if err != nil {
				t.Fatalf("stop %d lost its acknowledgement: %v", i, err)
			}
			if !resp.OK {
				t.Fatal(resp.Error)
			}
			select {
			case <-b.owner.Draining():
			case <-time.After(config.Defaults.StartupTimeout):
				t.Fatalf("acknowledged stop %d never began draining", i)
			}
		}()
	}
}

// An unresolved transport close is fatal by design: it begins draining and
// retains ownership. This test owns its broker directly because the shared
// fixture cleanup expects a clean release.
func TestUnresolvedCloseFailureRetainsOwnership(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	socket := shortBrokerSocketPath(t, "waggle-closefail-*")
	database := filepath.Join(t.TempDir(), "state.db")
	owner, err := brokerstate.Acquire(t.Context(), config.NewOwnershipConfig(database, config.CreateStore), statetest.Process{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Initialize(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	fake := newNativeFixture()
	b, err := newWithConnector(t.Context(), owner, config.NewBrokerConfig(config.BrokerEndpoints{Socket: socket, PID: socket + ".pid"}), config.NewNativeConfig(), fake)
	if err != nil {
		t.Fatal(err)
	}
	serving := make(chan error, 1)
	go func() { serving <- b.Serve(t.Context()) }()
	bad := boundFixture(t, b, "bad")
	boundFixture(t, b, "good")
	closeFailure := errors.New("native close failed")
	fake.mu.Lock()
	fake.closeErr[bad.Enrollment.ID] = closeFailure
	fake.mu.Unlock()
	c := connectClient(t, socket)
	defer c.Close()
	if resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdRetire, Recipient: bad.Enrollment.ID, Reason: "test"}); !resp.OK {
		t.Fatal(resp.Error)
	}
	deadline := time.After(config.Defaults.StartupTimeout)
	for {
		fake.mu.Lock()
		closes := fake.closes[bad.Enrollment.ID]
		fake.mu.Unlock()
		if closes == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("retired worker never closed its transport")
		case <-time.After(config.Defaults.StartupPollInterval):
		}
	}
	// Ingress closes because the fatal close report begins draining. Bound the
	// join so a regression that swallows that report fails here, not by
	// exhausting the suite deadline.
	select {
	case err := <-serving:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(config.Defaults.StartupTimeout):
		t.Fatal("unresolved close did not begin draining; ingress stayed open")
	}
	err = b.Shutdown(context.Background())
	if !errors.Is(err, brokerstate.ErrFinalizationFailed) || !errors.Is(err, closeFailure) {
		t.Fatalf("unresolved close must retain ownership: %v", err)
	}
	if _, err := brokerstate.Acquire(t.Context(), config.NewOwnershipConfig(database, config.OpenStore), statetest.Process{}); !errors.Is(err, brokerstate.ErrOwnerAlive) {
		t.Fatalf("retained ownership allowed takeover: %v", err)
	}
}
