package broker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/broker/enrollment"
	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/brokerstate/statetest"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/messages"
	"github.com/seungpyoson/waggle/internal/protocol"
)

// Adapted adversarial Probe 4: worker A's admitted accepted result must commit
// before worker B's fatal permits the supervising command to exit.
func TestFatalWaitPersistsAnotherWorkersAcceptedOutcome(t *testing.T) {
	database := filepath.Join(t.TempDir(), "state.db")
	socket := shortBrokerSocketPath(t, "waggle-fatal-*")
	owner, err := brokerstate.Acquire(t.Context(), config.NewOwnershipConfig(database, config.CreateStore), statetest.Process{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Initialize(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	waitBudget := config.Defaults.StartupTimeout
	cfg := config.NewBrokerConfig(config.BrokerEndpoints{Socket: socket, PID: socket + ".pid"})
	// Retirement wakes discovery, not an existing worker. Keep B's idle check
	// and the deliberate held-operation wait inside A's native-call budget,
	// leaving time for outcome persistence before the test's Wait expires.
	cfg.Messaging.IdleCheckInterval = cfg.Messaging.QueueCheckInterval
	transport := config.NewNativeConfig()
	transport.RequestTimeout = waitBudget / 2
	if cfg.Messaging.IdleCheckInterval+config.Defaults.StartupPollInterval >= transport.RequestTimeout || transport.RequestTimeout >= waitBudget {
		t.Fatal("fixture requires idle check plus held wait < native request timeout < owner wait budget")
	}
	b, err := newWithConnector(t.Context(), owner, cfg, transport, newNativeFixture())
	if err != nil {
		t.Fatal(err)
	}
	serving := make(chan error, 1)
	go func() { serving <- b.Serve(t.Context()) }()
	defer func() { owner.BeginShutdown(nil); <-serving }()
	sender := boundFixture(t, b, "sender")
	a := boundFixture(t, b, "a")
	other := boundFixture(t, b, "b")
	fake := b.native()
	gate := fake.holdSubmit(a.Enrollment.ID)
	released := false
	release := func() {
		if !released {
			released = true
			close(gate)
		}
	}
	defer release()
	fake.mu.Lock()
	fake.ignoreCancel = map[string]bool{a.Enrollment.ID: true}
	fake.closeErr[other.Enrollment.ID] = errors.New("fixture unresolved close")
	fake.mu.Unlock()
	c := connectClient(t, socket)
	defer c.Close()
	resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdSend, Credential: sender.Credential, Recipient: a.Enrollment.ID, Message: "in-flight", IdempotencyKey: "in-flight", Hops: b.config.Messaging.DefaultHops})
	if !resp.OK {
		t.Fatal(resp.Error)
	}
	envelope := fake.receive(t)
	// B observes retirement on its next configured idle check and closes.
	if resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdRetire, Recipient: other.Enrollment.ID, Reason: "test"}); !resp.OK {
		t.Fatal(resp.Error)
	}
	ctx, cancel := context.WithTimeout(t.Context(), waitBudget)
	defer cancel()
	select {
	case <-owner.Draining():
	case <-ctx.Done():
		t.Fatal("close failure never drained broker")
	}
	short, stop := context.WithTimeout(ctx, config.Defaults.StartupPollInterval)
	defer stop()
	if err := owner.Wait(short); !errors.Is(err, brokerstate.ErrShutdownIncomplete) {
		t.Errorf("Wait returned before A's outcome: %v", err)
	}
	release()
	if err := owner.Wait(ctx); !errors.Is(err, brokerstate.ErrFinalizationFailed) {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+database+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var possession, attempt string
	if err := raw.QueryRowContext(ctx, "SELECT possession, attempt_id FROM message_status WHERE id=?", envelope.Message.ID).Scan(&possession, &attempt); err != nil {
		t.Fatal(err)
	}
	if possession != "accepted" || attempt != envelope.Message.Attempt {
		t.Errorf("accepted result not persisted before Wait: %s %s", possession, attempt)
	}
	var barrier string
	if err := raw.QueryRowContext(ctx, "SELECT attempt_id FROM recipient_barrier WHERE recipient=?", a.Enrollment.ID).Scan(&barrier); err != nil {
		t.Fatal(err)
	}
	if barrier != attempt {
		t.Fatalf("acceptance changed receipt barrier: %s != %s", barrier, attempt)
	}
}

func TestWorkerOpenFailureClassification(t *testing.T) {
	for _, unresolved := range []bool{false, true} {
		t.Run(fmt.Sprint(unresolved), func(t *testing.T) {
			socket := shortBrokerSocketPath(t, "waggle-openfail-*")
			owner, err := brokerstate.Acquire(t.Context(), config.NewOwnershipConfig(filepath.Join(t.TempDir(), "state.db"), config.CreateStore), statetest.Process{})
			if err != nil {
				t.Fatal(err)
			}
			if err := Initialize(t.Context(), owner); err != nil {
				t.Fatal(err)
			}
			b, err := newWithConnector(t.Context(), owner, config.NewBrokerConfig(config.BrokerEndpoints{Socket: socket, PID: socket + ".pid"}), config.NewNativeConfig(), newNativeFixture())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), config.Defaults.StartupTimeout)
			defer cancel()
			serving := make(chan error, 1)
			go func() { serving <- b.Serve(ctx) }()
			defer func() { b.owner.BeginShutdown(nil); _ = b.owner.Wait(ctx); <-serving }()
			openErr := errors.New("subscription failed")
			if unresolved {
				openErr = fmt.Errorf("failed attach: %w", errors.Join(openErr, enrollment.ErrCloseFailed))
			}
			fake := b.native()
			fake.mu.Lock()
			fake.openErr = map[string]error{"broken": openErr}
			fake.mu.Unlock()
			e := enrollFixture(t, b, "broken")
			if !unresolved {
				// An initial Open failure preserves pending (only previously bound
				// enrollments disconnect). Wait for its actual observation.
				for {
					current := waitEnrollment(t, b, e.Enrollment.ID, "pending")
					if strings.Contains(current.Evidence, "subscription failed") {
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal("Open error was not observed")
					case <-time.After(config.Defaults.StartupPollInterval):
					}
				}
				select {
				case <-b.owner.Draining():
					t.Fatal("ordinary Open failure drained broker")
				default:
				}
				return
			}
			select {
			case <-b.owner.Draining():
			case <-ctx.Done():
				t.Fatal("unresolved Open close did not drain broker")
			}
			if err := b.owner.Wait(ctx); !errors.Is(err, brokerstate.ErrFinalizationFailed) || !errors.Is(err, enrollment.ErrCloseFailed) {
				t.Fatalf("unresolved Open close released ownership: %v", err)
			}
		})
	}
}

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

// A readiness answer that names another native thread is a provider-side
// condition, not a canonical persistence, fence or close failure. It withdraws
// exactly one enrollment's binding, with diagnostics, and the broker keeps
// serving every other enrollment.
func TestUncorrelatedReadinessDisconnectsOnlyThatEnrollment(t *testing.T) {
	_, b, shutdown := startTestBroker(t)
	defer shutdown()
	sender := boundFixture(t, b, "sender")
	broken := boundFixture(t, b, "broken")
	healthy := boundFixture(t, b, "healthy")
	fake := b.native()
	fake.uncorrelate("broken")
	e := waitEnrollment(t, b, broken.Enrollment.ID, "disconnected")
	if e.Evidence != "native readiness uncorrelated with the enrolled conversation" {
		t.Fatalf("provider anomaly recorded without its diagnostics: %q", e.Evidence)
	}
	// The transport is joined before the observation is written, on the same
	// path a failed probe uses, so the next open waits out the reconnect backoff.
	fake.mu.Lock()
	joined := fake.closes[broken.Enrollment.ID]
	fake.mu.Unlock()
	if joined < 1 {
		t.Fatal("anomaly recorded readiness without joining its transport")
	}
	select {
	case <-b.owner.Draining():
		t.Fatal("a provider anomaly closed broker admission")
	default:
	}
	before := b.owner.Stats()
	var stored messages.Result
	if err := statetest.Write(b.owner, func(tx *brokerstate.WriteTx) error {
		var err error
		stored, err = messages.NewStore(tx, b.config.Messaging, time.Now()).Apply(messages.Enqueue{Credential: sender.Credential, Recipient: healthy.Enrollment.ID, RequestID: "unaffected", Body: "still serving", Hops: b.config.Messaging.DefaultHops})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := fake.receive(t); got.Message.ID != stored.Message.ID {
		t.Fatal("a second bound enrollment stopped receiving after the anomaly")
	}
	if after := b.owner.Stats(); after.Writes <= before.Writes || after.Reads <= before.Reads {
		t.Fatalf("owner admitted no work after the anomaly: %+v then %+v", before, after)
	}
}

// The recipient can acknowledge from its inbox while the submission is still in
// flight. When the provider then reports refusal, the canonical store rejects
// the contradiction and keeps its own decision. That rejection is a domain
// outcome: the intent is never resubmitted and the broker keeps serving.
func TestEarlyReceiptRacingRefusedOutcomeKeepsBrokerServing(t *testing.T) {
	socket, b, shutdown := startTestBroker(t)
	defer shutdown()
	sender := boundFixture(t, b, "sender")
	recipient := boundFixture(t, b, "recipient")
	fake := b.native()
	gate := fake.holdSubmit(recipient.Enrollment.ID)
	var released bool
	release := func() {
		if !released {
			released = true
			close(gate)
		}
	}
	defer release()
	c := connectClient(t, socket)
	defer c.Close()
	send := func(key string) messages.Message {
		resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdSend, Credential: sender.Credential, Recipient: recipient.Enrollment.ID, Message: key, IdempotencyKey: key, Hops: b.config.Messaging.DefaultHops})
		if !resp.OK {
			release()
			t.Fatal(resp.Error)
		}
		var m messages.Message
		unmarshalResponse(t, resp, &m)
		return m
	}
	first := send("early receipt")
	envelope := fake.receive(t) // the attempt is committed; Submit is held
	ack := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdAck, Credential: recipient.Credential, MessageID: first.ID, AttemptID: envelope.Message.Attempt})
	if !ack.OK {
		release()
		t.Fatal(ack.Error)
	}
	fake.answer(enrollment.Refused)
	release()
	// The worker keeps selecting for this recipient, which it can only do after
	// the rejected observation returned it to its loop.
	second := send("after the conflict")
	if got := fake.receive(t); got.Message.ID != second.ID {
		t.Fatal("worker stopped serving after the canonical store rejected the outcome")
	}
	select {
	case <-b.owner.Draining():
		t.Fatal("a rejected observation closed broker admission")
	default:
	}
	if err := statetest.Write(b.owner, func(tx *brokerstate.WriteTx) error {
		m, err := messages.NewStore(tx, b.config.Messaging, time.Now()).Message(first.ID)
		if err != nil {
			return err
		}
		if m.Possession != "consumed" || m.Attempt != envelope.Message.Attempt {
			t.Errorf("store did not keep its own decision: %+v", m)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
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
	// Keep the measurement fast while exercising distinct scheduling periods.
	cfg.Messaging.QueueCheckInterval = config.Defaults.StartupPollInterval / 2
	cfg.Messaging.IdleCheckInterval = 4 * cfg.Messaging.QueueCheckInterval
	cfg.Messaging.DiscoveryInterval = 2 * cfg.Messaging.IdleCheckInterval
	cfg.Messaging.ProbeInterval = cfg.Messaging.IdleCheckInterval
	idle := 3 * max(cfg.Messaging.IdleCheckInterval, cfg.Messaging.DiscoveryInterval)
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
	// One worker snapshot per idle check, two snapshots per discovery pass.
	// Include one boundary check from each loop; a queue-floor timer exceeds
	// this bound even though it still produces no writes.
	readBound := uint64(idle/cfg.Messaging.IdleCheckInterval+1) + 2*uint64(idle/cfg.Messaging.DiscoveryInterval+1)
	if reads := after.Reads - before.Reads; reads > readBound {
		t.Fatalf("idle timer used wake floor: %d reads, bound %d", reads, readBound)
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

func TestBoundRecipientWakeDeliversBeforeIdlePoll(t *testing.T) {
	socket := shortBrokerSocketPath(t, "waggle-wake-*")
	cfg := config.NewBrokerConfig(config.BrokerEndpoints{Socket: socket, PID: socket + ".pid"})
	b, err := newOwnedTestBroker(t, filepath.Join(t.TempDir(), "state.db"), config.CreateStore, cfg)
	if err != nil {
		t.Fatal(err)
	}
	shutdown := serveTestBroker(t, b)
	defer shutdown()
	sender := boundFixture(t, b, "sender")
	recipient := boundFixture(t, b, "recipient")
	c := connectClient(t, socket)
	defer c.Close()
	allowance := config.Defaults.StartupPollInterval
	bound := cfg.Messaging.QueueCheckInterval + allowance
	if bound >= cfg.Messaging.IdleCheckInterval/2 {
		t.Fatal("delivery bound does not distinguish wakes from idle polling")
	}
	for _, key := range []string{"first wake", "wake after receipt", "another wake"} {
		start := time.Now()
		resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdSend, Credential: sender.Credential, Recipient: recipient.Enrollment.ID, Message: key, IdempotencyKey: key, Hops: cfg.Messaging.DefaultHops})
		if !resp.OK {
			t.Fatal(resp.Error)
		}
		select {
		case envelope := <-b.native().submitted:
			t.Logf("wake delivered in %v (bound %v, idle %v)", time.Since(start), bound, cfg.Messaging.IdleCheckInterval)
			if time.Since(start) > bound {
				t.Fatal("wake delivery exceeded floor plus transport allowance")
			}
			if resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdAck, Credential: recipient.Credential, MessageID: envelope.Message.ID, AttemptID: envelope.Message.Attempt}); !resp.OK {
				t.Fatal(resp.Error)
			}
		case <-time.After(bound):
			t.Fatal("bound recipient waited for idle poll instead of wake")
		}
	}
}

func TestWorkerWakeStormRespectsQueueCheckFloor(t *testing.T) {
	socket := shortBrokerSocketPath(t, "waggle-floor-*")
	cfg := config.NewBrokerConfig(config.BrokerEndpoints{Socket: socket, PID: socket + ".pid"})
	b, err := newOwnedTestBroker(t, filepath.Join(t.TempDir(), "state.db"), config.CreateStore, cfg)
	if err != nil {
		t.Fatal(err)
	}
	shutdown := serveTestBroker(t, b)
	defer shutdown()
	r := boundFixture(t, b, "recipient")
	window := 4 * cfg.Messaging.QueueCheckInterval
	tick := time.NewTicker(cfg.Messaging.QueueCheckInterval / 10)
	defer tick.Stop()
	timer := time.NewTimer(window)
	defer timer.Stop()
	before, started := b.owner.Stats(), time.Now()
storm:
	for {
		b.enrollment.Wake(r.Enrollment.ID)
		select {
		case <-tick.C:
		case <-timer.C:
			break storm
		}
	}
	elapsed := time.Since(started)
	reads := b.owner.Stats().Reads - before.Reads
	// Worker checks plus discovery's two snapshots, including boundary passes.
	bound := uint64(elapsed/cfg.Messaging.QueueCheckInterval+2) + 2*uint64(elapsed/cfg.Messaging.DiscoveryInterval+1)
	if reads > bound {
		t.Fatalf("coalesced wakes bypassed floor: %d reads, bound %d in %v", reads, bound, elapsed)
	}
	if reads < 2 {
		t.Fatalf("wake storm starved checks: %d reads in %v", reads, elapsed)
	}
}
