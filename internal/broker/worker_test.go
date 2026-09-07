package broker

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/broker/enrollment"
	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/brokerstate/statetest"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/messages"
	"github.com/seungpyoson/waggle/internal/protocol"
)

func TestWorkerFindsCommittedWorkWithoutWakeup(t *testing.T) {
	_, b, shutdown := startTestBroker(t)
	defer shutdown()
	a := boundFixture(t, b, "sender")
	recipient := boundFixture(t, b, "recipient")
	var stored messages.Result
	// Inject interruption exactly after canonical commit: omit the RPC
	// wakeup. The recipient worker's own periodic check must discover this row.
	if err := statetest.Write(b.owner, func(tx *brokerstate.WriteTx) error {
		var err error
		stored, err = messages.NewStore(tx, b.config.Messaging, time.Now()).Apply(messages.Enqueue{Credential: a.Credential, Recipient: recipient.Enrollment.ID, RequestID: "lost-wakeup", Body: "committed work", Hops: b.config.Messaging.DefaultHops})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := b.native().receive(t); got.Message.ID != stored.Message.ID {
		t.Fatal("periodic worker check did not discover committed work")
	}
}

func TestWorkerRetainsUncertainAttemptAcrossRestart(t *testing.T) {
	database := filepath.Join(t.TempDir(), "state.db")
	socket := shortBrokerSocketPath(t, "waggle-restart-*")
	b, err := newOwnedTestBroker(t, database, config.CreateStore, config.NewBrokerConfig(config.BrokerEndpoints{Socket: socket, PID: socket + ".pid"}))
	if err != nil {
		t.Fatal(err)
	}
	shutdown := serveTestBroker(t, b)
	defer shutdown()
	fake := b.native()
	fake.mu.Lock()
	fake.outcome = enrollment.Uncertain
	fake.mu.Unlock()
	a := boundFixture(t, b, "sender")
	recipient := boundFixture(t, b, "recipient")
	other := boundFixture(t, b, "other")
	c := connectClient(t, socket)
	defer c.Close()
	send := func(recipient, key string) messages.Message {
		resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdSend, Credential: a.Credential, Recipient: recipient, Message: key, IdempotencyKey: key, Hops: b.config.Messaging.DefaultHops})
		if !resp.OK {
			t.Fatal(resp.Error)
		}
		var m messages.Message
		unmarshalResponse(t, resp, &m)
		return m
	}
	first := send(recipient.Enrollment.ID, "first")
	if fake.receive(t).Message.ID != first.ID {
		t.Fatal("wrong first submission")
	}
	second := send(recipient.Enrollment.ID, "second")
	if err := b.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Reacquire the same explicit store without resubmitting its intent.
	next, err := newOwnedTestBroker(t, database, config.OpenStore, b.config)
	if err != nil {
		t.Fatal(err)
	}
	stopNext := serveTestBroker(t, next)
	defer stopNext()
	waitEnrollment(t, next, a.Enrollment.ID, "bound")
	waitEnrollment(t, next, recipient.Enrollment.ID, "bound")
	waitEnrollment(t, next, other.Enrollment.ID, "bound")
	c = connectClient(t, socket)
	defer c.Close()
	marker := send(other.Enrollment.ID, "independent")
	if got := next.native().receive(t); got.Message.ID != marker.ID {
		t.Fatal("restart resubmitted retained input or crossed its barrier")
	}
	if err := statetest.Write(next.owner, func(tx *brokerstate.WriteTx) error {
		s := messages.NewStore(tx, next.config.Messaging, time.Now())
		m, err := s.Message(second.ID)
		if err != nil {
			return err
		}
		if m.Attempt != "" || m.BlockedBy != first.ID {
			t.Error("restart released uncertain recipient ordering")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownRetainsOwnershipUntilDriverCloseJoins(t *testing.T) {
	database := filepath.Join(t.TempDir(), "state.db")
	socket := shortBrokerSocketPath(t, "waggle-close-*")
	b, err := newOwnedTestBroker(t, database, config.CreateStore, config.NewBrokerConfig(config.BrokerEndpoints{Socket: socket, PID: socket + ".pid"}))
	if err != nil {
		t.Fatal(err)
	}
	shutdown := serveTestBroker(t, b)
	defer shutdown()
	a := boundFixture(t, b, "sender")
	recipient := boundFixture(t, b, "recipient")
	fake := b.native()
	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	fake.mu.Lock()
	fake.closeGate, fake.closeEntered = gate, entered
	fake.mu.Unlock()
	c := connectClient(t, socket)
	defer c.Close()
	resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdSend, Credential: a.Credential, Recipient: recipient.Enrollment.ID, Message: "held close", IdempotencyKey: "held close", Hops: b.config.Messaging.DefaultHops})
	if !resp.OK {
		close(gate)
		t.Fatal(resp.Error)
	}
	// Always release the fixture even when an assertion fails.
	defer close(gate)
	fake.receive(t)
	deadline, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	b.owner.BeginShutdown(nil)
	// A reporting deadline bounds only this caller; it never changes owner
	// progress and is not the permanent finalization failure.
	if err := b.owner.Wait(deadline); !errors.Is(err, brokerstate.ErrShutdownIncomplete) {
		t.Fatalf("unjoined driver did not bound shutdown wait: %v", err)
	}
	waitCtx, cancelWait := context.WithTimeout(t.Context(), config.Defaults.StartupTimeout)
	defer cancelWait()
	select {
	case <-entered:
	case <-waitCtx.Done():
		t.Fatal("driver never entered close")
	}
	// New admission remains closed even though the socket listener is closed.
	if err := b.owner.Do(t.Context(), func(*brokerstate.Operation) error { t.Error("admission reopened"); return nil }); !errors.Is(err, brokerstate.ErrAdmissionClosed) {
		t.Fatal(err)
	}
	contender, err := brokerstate.Acquire(t.Context(), config.NewOwnershipConfig(database, config.OpenStore), statetest.Process{})
	if contender != nil || !errors.Is(err, brokerstate.ErrOwnerAlive) {
		t.Fatalf("ownership released before native close joined: %v", err)
	}
}

// TestIdleBoundWorkerIssuesNoWriteTransactions holds the design's idle contract:
// an idle broker with one bound enrollment observes through read snapshots and
// enters no write transaction at all, from its worker or from discovery, yet
// still finds work committed without a wakeup. The window covers whole periods
// of both loops.
func TestIdleBoundWorkerIssuesNoWriteTransactions(t *testing.T) {
	socket := shortBrokerSocketPath(t, "waggle-idle-*")
	cfg := config.NewBrokerConfig(config.BrokerEndpoints{Socket: socket, PID: socket + ".pid"})
	idle := 3 * max(cfg.Messaging.QueueCheckInterval, cfg.Messaging.DiscoveryInterval)
	b, err := newOwnedTestBroker(t, filepath.Join(t.TempDir(), "state.db"), config.CreateStore, cfg)
	if err != nil {
		t.Fatal(err)
	}
	shutdown := serveTestBroker(t, b)
	defer shutdown()
	recipient := boundFixture(t, b, "recipient")
	fake := b.native()
	// A second probe proves the binding check completed: its write transactions,
	// and any selection it entered, are behind the measurement.
	awaitTransport(t, fake, recipient.Enrollment.ID, "bound worker replaced or dropped its transport",
		func(opens, closes, _ int) bool { return opens <= 1 && closes == 0 },
		func(_, _, probes int) bool { return probes >= 2 })
	before := b.owner.Stats()
	time.Sleep(idle)
	after := b.owner.Stats()
	if after.Writes != before.Writes {
		t.Fatalf("idle broker opened %d write transactions in %v", after.Writes-before.Writes, idle)
	}
	if after.Reads < before.Reads+2 {
		t.Fatalf("idle broker took %d read snapshots in %v, want at least 2", after.Reads-before.Reads, idle)
	}
	// The hint must still carry committed work into the intent transaction. This
	// row is committed without an RPC wakeup, so only the snapshot can find it.
	sender := boundFixture(t, b, "sender")
	var stored messages.Result
	if err := statetest.Write(b.owner, func(tx *brokerstate.WriteTx) error {
		var err error
		stored, err = messages.NewStore(tx, b.config.Messaging, time.Now()).Apply(messages.Enqueue{Credential: sender.Credential, Recipient: recipient.Enrollment.ID, RequestID: "after-idle", Body: "queued while idle", Hops: b.config.Messaging.DefaultHops})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := fake.receive(t); got.Message.ID != stored.Message.ID {
		t.Fatal("pending hint did not enter the intent transaction")
	}
}
