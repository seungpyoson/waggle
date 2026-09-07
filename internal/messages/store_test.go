package messages

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/brokerstate/statetest"
	"github.com/seungpyoson/waggle/internal/config"
)

func TestObservationPagesExcludeTerminalHistoryAndReachEveryLiveEnrollment(t *testing.T) {
	f := newFixture(t)
	f.limits.ScanLimit = 2
	live := make(map[string]bool)
	for i := 0; i < 5; i++ {
		e := f.must(Enroll{Provider: "codex", Conversation: fmt.Sprint(i), Endpoint: "/registered/daemon.sock", Label: fmt.Sprint(i)})
		if i == 2 {
			f.must(Retire{ID: e.Enrollment.ID, Reason: "retired fixture"})
		} else {
			live[e.Enrollment.ID] = true
		}
	}
	var after string
	seen := make(map[string]bool)
	for {
		var page []Enrollment
		err := statetest.Write(f.owner, func(tx *brokerstate.WriteTx) error {
			var err error
			page, err = NewStore(tx, f.limits, f.now).Enrollments(after, ObservableEnrollments)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		if len(page) > f.limits.ScanLimit {
			t.Fatal("unbounded observation page")
		}
		for _, e := range page {
			if !live[e.ID] || seen[e.ID] || e.ID <= after {
				t.Fatal("terminal, duplicate or out-of-order observation candidate")
			}
			seen[e.ID] = true
			after = e.ID
		}
	}
	if len(seen) != len(live) {
		t.Fatal("bounded scans stranded a live enrollment")
	}
}

func TestEnrollmentCapacityIsTransactionalAndRetirementFreesCapacity(t *testing.T) {
	f := newFixture(t)
	f.limits.EnrollmentCapacity = 1
	first := f.must(Enroll{Provider: "codex", Conversation: "first", Endpoint: "/registered/daemon.sock", Label: "first"})
	if _, err := f.apply(Enroll{Provider: "codex", Conversation: "second", Endpoint: "/registered/daemon.sock", Label: "second"}); err == nil {
		t.Fatal("enrollment exceeded capacity")
	}
	f.must(Retire{ID: first.Enrollment.ID, Reason: "capacity test"})
	second := f.must(Enroll{Provider: "codex", Conversation: "second", Endpoint: "/registered/daemon.sock", Label: "second"})
	if second.Enrollment.State != "pending" {
		t.Fatal("retirement failed to free enrollment capacity")
	}
}

func TestUnavailablePendingObservationPreservesDeadlineAndCannotReviveRetirement(t *testing.T) {
	f := newFixture(t)
	e := f.must(Enroll{Provider: "codex", Conversation: "pending", Endpoint: "/registered/daemon.sock", Label: "pending"})
	f.must(Unavailable{ID: e.Enrollment.ID, Evidence: "thread is not loaded"})
	f.now = f.now.Add(f.limits.PendingTTL)
	f.must(Expire{})
	if _, err := f.apply(Bind{ID: e.Enrollment.ID, Conversation: "pending", Endpoint: "/registered/daemon.sock", Evidence: "late idle observation"}); !errors.Is(err, ErrConflict) {
		t.Fatal("late probe revived failed enrollment")
	}
	f.must(Retire{ID: e.Enrollment.ID, Reason: "operator retirement"})
	if _, err := f.apply(Unavailable{ID: e.Enrollment.ID, Evidence: "late unavailable observation"}); !errors.Is(err, ErrConflict) {
		t.Fatal("late observation changed retirement")
	}
}

type fixture struct {
	t      *testing.T
	owner  *brokerstate.Owner
	limits config.MessagingConfig
	now    time.Time
}

func newFixture(t *testing.T) *fixture {
	return &fixture{t: t, owner: statetest.New(t, Schema+"UPDATE cutover SET state='active' WHERE singleton=1"), limits: config.NewMessagingConfig(), now: time.Now().UTC()}
}

func (f *fixture) apply(c Command) (out Result, err error) {
	err = statetest.Write(f.owner, func(tx *brokerstate.WriteTx) error { out, err = NewStore(tx, f.limits, f.now).Apply(c); return err })
	return
}

func (f *fixture) must(c Command) Result {
	f.t.Helper()
	out, err := f.apply(c)
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fixture) enrolled(label string) Result {
	f.t.Helper()
	out := f.must(Enroll{Provider: "codex", Conversation: label, Endpoint: "/registered/daemon.sock", Label: label})
	f.must(Bind{ID: out.Enrollment.ID, Conversation: label, Endpoint: "/registered/daemon.sock", Evidence: "exact thread membership and native idle observation"})
	return out
}

func (f *fixture) send(sender, recipient Result, key string) Message {
	f.t.Helper()
	return f.must(Enqueue{Credential: sender.Credential, Recipient: recipient.Enrollment.ID, RequestID: key, Body: key, Within: f.limits.DefaultDeadline, Hops: f.limits.DefaultHops}).Message
}

func (f *fixture) message(id string) (m Message) {
	f.t.Helper()
	if err := statetest.Write(f.owner, func(tx *brokerstate.WriteTx) error {
		var err error
		m, err = NewStore(tx, f.limits, f.now).Message(id)
		return err
	}); err != nil {
		f.t.Fatal(err)
	}
	return
}

func TestFreshEnrollmentBindsWithoutToolCall(t *testing.T) {
	f := newFixture(t)
	sender := f.enrolled("sender")
	receiver := f.must(Enroll{Provider: "codex", Conversation: "fresh", Endpoint: "/registered/daemon.sock", Label: "fresh"})
	if receiver.Enrollment.State != "pending" {
		t.Fatal("unverified enrollment reported ready")
	}
	_, err := f.apply(Enqueue{Credential: sender.Credential, Recipient: receiver.Enrollment.ID, RequestID: "first", Body: "first", Within: time.Minute, Hops: 2})
	if !errors.Is(err, ErrNotBound) {
		t.Fatalf("pending target accepted direct send: %v", err)
	}
	f.must(Bind{ID: receiver.Enrollment.ID, Conversation: "fresh", Endpoint: "/registered/daemon.sock", Evidence: "thread loaded, native idle"})
	m := f.send(sender, receiver, "first")
	d := f.must(Select{}).Dispatches
	if len(d) != 1 || d[0].Envelope.Message.ID != m.ID || d[0].Envelope.Acknowledgement == "" {
		t.Fatalf("fresh target not selectable with ack instruction: %+v", d)
	}
	f.must(Consume{Credential: receiver.Credential, MessageID: m.ID, Attempt: d[0].Envelope.Message.Attempt})
	if got := f.message(m.ID).Possession; got != "consumed" {
		t.Fatalf("first authenticated tool call did not consume: %s", got)
	}
}

func TestBindingAndReceiptRequireExactIncarnation(t *testing.T) {
	f := newFixture(t)
	a := f.enrolled("a")
	b := f.enrolled("b")
	if a.Credential == b.Credential || a.Enrollment.ID == b.Enrollment.ID {
		t.Fatal("same endpoint shared an identity")
	}
	pending := f.must(Enroll{Provider: "codex", Conversation: "c", Endpoint: "/registered/daemon.sock", Label: "c"})
	if _, err := f.apply(Bind{ID: pending.Enrollment.ID, Conversation: "b", Endpoint: "/registered/daemon.sock", Evidence: "another thread"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("substituted binding accepted: %v", err)
	}
	m := f.send(a, b, "hello")
	d := f.must(Select{}).Dispatches[0]
	for _, receipt := range []Consume{
		{Credential: a.Credential, MessageID: m.ID, Attempt: d.Envelope.Message.Attempt},
		{Credential: b.Credential, MessageID: m.ID, Attempt: "another-attempt"},
		{Credential: "b", MessageID: m.ID, Attempt: d.Envelope.Message.Attempt},
	} {
		if _, err := f.apply(receipt); err == nil {
			t.Fatal("substituted receipt accepted")
		}
	}
	if f.message(m.ID).Possession != "uncertain" {
		t.Fatal("invalid receipt changed possession")
	}
}

func TestRetainedInputSurvivesExpiryRestartAndLateEvidence(t *testing.T) {
	f := newFixture(t)
	a := f.enrolled("a")
	b := f.enrolled("b")
	m := f.send(a, b, "held")
	d := f.must(Select{}).Dispatches[0]
	f.must(Observe{Attempt: d.Envelope.Message.Attempt, Recipient: b.Enrollment.ID, Kind: "held", Evidence: "native held input"})
	f.now = f.now.Add(f.limits.DefaultDeadline)
	f.must(Expire{})
	f.must(Restart{})
	f.must(Bind{ID: a.Enrollment.ID, Conversation: "a", Endpoint: "/registered/daemon.sock", Evidence: "reconnected same thread"})
	f.must(Bind{ID: b.Enrollment.ID, Conversation: "b", Endpoint: "/registered/daemon.sock", Evidence: "reconnected same thread"})
	next := f.send(a, b, "later")
	if got := f.message(next.ID).BlockedBy; got != m.ID {
		t.Fatalf("retained input must identify the blocking message: %q", got)
	}
	if len(f.must(Select{}).Dispatches) != 0 {
		t.Fatal("expiry or restart cleared retention barrier")
	}
	if got := f.message(m.ID); got.Possession != "held" || got.Disposition != "deadline_elapsed" {
		t.Fatalf("lost independent facts: %+v", got)
	}
	f.must(Consume{Credential: b.Credential, MessageID: m.ID, Attempt: d.Envelope.Message.Attempt})
	f.must(Observe{Attempt: d.Envelope.Message.Attempt, Recipient: b.Enrollment.ID, Kind: "uncertain", Evidence: "delayed transport response loss"})
	if got := f.message(m.ID); got.Possession != "consumed" || got.Disposition != "deadline_elapsed" {
		t.Fatalf("late evidence reversed terminal facts: %+v", got)
	}
	work := f.must(Select{}).Dispatches
	if len(work) != 1 || work[0].Envelope.Message.ID != next.ID {
		t.Fatal("receipt did not release exact recipient barrier")
	}
	if len(f.must(Select{}).Dispatches) != 0 {
		t.Fatal("second attempt became eligible")
	}
}

func TestCancellationBeforeIntentAndReplyBudget(t *testing.T) {
	f := newFixture(t)
	a := f.enrolled("a")
	b := f.enrolled("b")
	stopped := f.send(a, b, "stop-before-intent")
	f.must(Stop{Credential: a.Credential, Conversation: stopped.Conversation, Reason: "operator stopped"})
	m := f.must(Enqueue{Credential: a.Credential, Recipient: b.Enrollment.ID, RequestID: "one-hop", Body: "bounded conversation", Within: time.Minute, Hops: 1}).Message
	d := f.must(Select{}).Dispatches
	if len(d) != 1 || d[0].Envelope.Message.ID != m.ID {
		t.Fatal("canceled unsent work retained ordering")
	}
	reply := f.must(Reply{Credential: b.Credential, MessageID: m.ID, RequestID: "reply", Body: "correlated reply"}).Message
	if reply.Conversation != m.Conversation || !reply.Deadline.Equal(m.Deadline) || reply.RemainingHops != 0 || reply.Disposition != "budget_exhausted" {
		t.Fatalf("reply reset conversation bounds: %+v", reply)
	}
	if f.message(m.ID).Possession != "consumed" || len(f.must(Select{}).Dispatches) != 0 {
		t.Fatal("reply consumption or budget contract failed")
	}
}

func TestIdempotencyCapacityAndFailedCommandRollback(t *testing.T) {
	f := newFixture(t)
	f.limits.QueueCapacity = 1
	a := f.enrolled("a")
	b := f.enrolled("b")
	c := Enqueue{Credential: a.Credential, Recipient: b.Enrollment.ID, RequestID: "request", Body: "body", Within: time.Minute, Hops: 2}
	first := f.must(c).Message
	if f.must(c).Message.ID != first.ID {
		t.Fatal("duplicate request inserted another message")
	}
	c.Body = "changed"
	if _, err := f.apply(c); !errors.Is(err, ErrConflict) {
		t.Fatalf("request ID conflict: %v", err)
	}
	c.RequestID = "second"
	if _, err := f.apply(c); !errors.Is(err, ErrBackpressure) {
		t.Fatalf("queue capacity: %v", err)
	}
	if len(f.must(Select{}).Dispatches) != 1 {
		t.Fatal("failed command damaged stored work")
	}
}

func TestRequestDeadlineIsAssignedOnceByBroker(t *testing.T) {
	f := newFixture(t)
	a, b := f.enrolled("a"), f.enrolled("b")
	c := Enqueue{Credential: a.Credential, Recipient: b.Enrollment.ID, RequestID: "stable", Body: "body", Hops: 2}
	first := f.must(c).Message
	if !first.Deadline.Equal(f.now.Add(f.limits.DefaultDeadline)) {
		t.Fatal("broker did not assign its configured default deadline")
	}
	f.now = f.now.Add(f.limits.DefaultDeadline * 2)
	f.limits.DefaultDeadline = f.limits.DefaultDeadline / 2
	duplicate := f.must(c).Message
	if duplicate.ID != first.ID || !duplicate.Deadline.Equal(first.Deadline) || duplicate.Disposition != "deadline_elapsed" {
		t.Fatal("duplicate request changed its deadline or restarted expired work")
	}
	c.Within = time.Minute
	if _, err := f.apply(c); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed duration reused the same request ID: %v", err)
	}
}

func TestRetirementCannotRebindHeldConversation(t *testing.T) {
	f := newFixture(t)
	a := f.enrolled("a")
	b := f.enrolled("b")
	m := f.send(a, b, "uncertain")
	d := f.must(Select{}).Dispatches[0]
	f.must(Retire{ID: b.Enrollment.ID, Reason: "unresolved provider retention"})
	if _, err := f.apply(Enroll{Provider: "codex", Conversation: "b", Endpoint: "/registered/daemon.sock", Label: "b"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("retirement bypassed retained input: %v", err)
	}
	f.must(Consume{Credential: b.Credential, MessageID: m.ID, Attempt: d.Envelope.Message.Attempt})
	f.must(Enroll{Provider: "codex", Conversation: "b", Endpoint: "/registered/daemon.sock", Label: "b"})
	if len(f.must(Select{}).Dispatches) != 0 {
		t.Fatal("retirement replayed old input")
	}
}

func TestPendingBoundAndNonRetentionHaveExplicitEvidence(t *testing.T) {
	f := newFixture(t)
	a := f.enrolled("a")
	b := f.enrolled("b")
	p := f.must(Enroll{Provider: "codex", Conversation: "pending", Endpoint: "/registered/daemon.sock", Label: "pending"})
	f.now = f.now.Add(f.limits.PendingTTL)
	f.must(Expire{})
	if _, err := f.apply(Bind{ID: p.Enrollment.ID, Conversation: "pending", Endpoint: "/registered/daemon.sock", Evidence: "late readiness"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired enrollment activated: %v", err)
	}
	m := f.send(a, b, "refused")
	d := f.must(Select{}).Dispatches[0]
	if _, err := f.apply(Observe{Attempt: d.Envelope.Message.Attempt, Recipient: b.Enrollment.ID, Kind: "refused"}); err == nil {
		t.Fatal("unsubstantiated refusal accepted")
	}
	f.must(Observe{Attempt: d.Envelope.Message.Attempt, Recipient: b.Enrollment.ID, Kind: "refused", Evidence: "attempt-specific definitive native refusal"})
	if got := f.message(m.ID).Possession; got != "not_retained" {
		t.Fatalf("definitive refusal did not establish non-retention: %s", got)
	}
	if _, err := f.apply(Consume{Credential: b.Credential, MessageID: m.ID, Attempt: d.Envelope.Message.Attempt}); !errors.Is(err, ErrConflict) {
		t.Fatalf("contradictory receipt accepted: %v", err)
	}
	f.send(a, b, "next")
	if len(f.must(Select{}).Dispatches) != 1 {
		t.Fatal("definitive non-retention retained barrier")
	}
}

func TestInboxDoesNotAcknowledgeOrReleaseBarrier(t *testing.T) {
	f := newFixture(t)
	a := f.enrolled("a")
	b := f.enrolled("b")
	m := f.send(a, b, "inbox")
	f.must(Select{})
	if err := statetest.Write(f.owner, func(tx *brokerstate.WriteTx) error {
		items, err := NewStore(tx, f.limits, f.now).Inbox(b.Credential)
		if err == nil && len(items) != 1 {
			t.Fatal("inbox lost message")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if f.message(m.ID).Possession != "uncertain" || len(f.must(Select{}).Dispatches) != 0 {
		t.Fatal("inbox mutated delivery state")
	}
}

func TestCanonicalSchemaRejectsDanglingAndDuplicateAttempts(t *testing.T) {
	f := newFixture(t)
	err := statetest.Write(f.owner, func(tx *brokerstate.WriteTx) error {
		_, err := tx.Exec("INSERT INTO attempts(id,message_id,owner_id,generation,intent_at) VALUES('attempt','missing-message','owner',1,1)")
		return err
	})
	if err == nil {
		t.Fatal("foreign-key enforcement is disabled on canonical storage")
	}
	a := f.enrolled("a")
	b := f.enrolled("b")
	m := f.send(a, b, "one-attempt")
	f.must(Select{})
	err = statetest.Write(f.owner, func(tx *brokerstate.WriteTx) error {
		_, err := tx.Exec("INSERT INTO attempts(id,message_id,owner_id,generation,intent_at) VALUES('second-attempt',?,'owner',1,1)", m.ID)
		return err
	})
	if err == nil {
		t.Fatal("schema admitted a second attempt for one message")
	}
}

func TestSelectScopedToRecipientLeavesOtherRecipientsUntouched(t *testing.T) {
	f := newFixture(t)
	a := f.enrolled("a")
	b := f.enrolled("b")
	c := f.enrolled("c")
	// The unscoped candidate is enqueued first: a selection that only limited
	// its page size, without scoping the recipient, would claim this one.
	f.must(Enqueue{Credential: a.Credential, Recipient: c.Enrollment.ID, RequestID: "to-c", Body: "c", Hops: f.limits.DefaultHops})
	f.must(Enqueue{Credential: a.Credential, Recipient: b.Enrollment.ID, RequestID: "to-b", Body: "b", Hops: f.limits.DefaultHops})
	d := f.must(Select{Recipient: b.Enrollment.ID}).Dispatches
	if len(d) != 1 || d[0].Recipient.ID != b.Enrollment.ID {
		t.Fatalf("recipient-scoped selection: %+v", d)
	}
	if f.message(d[0].Envelope.Message.ID).Attempt == "" {
		t.Fatal("intent not committed with selection")
	}
	rest := f.must(Select{}).Dispatches
	if len(rest) != 1 || rest[0].Recipient.ID != c.Enrollment.ID {
		t.Fatalf("scoped selection touched another recipient: %+v", rest)
	}
}

// pending runs the read-only hint through a View built over the fixture's write
// transaction: WriteTx is a Reader, so the hint sees exactly what selection sees.
func (f *fixture) pending(recipient string) (out bool) {
	f.t.Helper()
	if err := statetest.Write(f.owner, func(tx *brokerstate.WriteTx) error {
		var err error
		out, err = NewView(tx, f.limits, f.now).Pending(recipient)
		return err
	}); err != nil {
		f.t.Fatal(err)
	}
	return
}

func TestPendingHintTracksSelectEligibility(t *testing.T) {
	f := newFixture(t)
	a := f.enrolled("a")
	b := f.enrolled("b")
	if f.pending(b.Enrollment.ID) {
		t.Fatal("empty queue hinted queued work")
	}
	m := f.send(a, b, "hint")
	if !f.pending(b.Enrollment.ID) {
		t.Fatal("queued eligible message did not hint work")
	}
	f.must(Unavailable{ID: b.Enrollment.ID, Evidence: "native readiness withdrawn"})
	if f.pending(b.Enrollment.ID) {
		t.Fatal("recipient that is not bound hinted work")
	}
	f.must(Bind{ID: b.Enrollment.ID, Conversation: "b", Endpoint: "/registered/daemon.sock", Evidence: "reconnected same thread"})
	if !f.pending(b.Enrollment.ID) {
		t.Fatal("rebound recipient lost its hint")
	}
	d := f.must(Select{Recipient: b.Enrollment.ID}).Dispatches
	if len(d) != 1 || d[0].Envelope.Message.ID != m.ID {
		t.Fatalf("selection did not claim the hinted message: %+v", d)
	}
	if f.pending(b.Enrollment.ID) {
		t.Fatal("claimed attempt and retained input still hinted work")
	}
	f.must(Consume{Credential: b.Credential, MessageID: m.ID, Attempt: d[0].Envelope.Message.Attempt})
	next := f.send(a, b, "stopped")
	if !f.pending(b.Enrollment.ID) {
		t.Fatal("released barrier did not restore the hint")
	}
	f.must(Stop{Credential: a.Credential, Conversation: next.Conversation, Reason: "operator stop"})
	if f.pending(b.Enrollment.ID) {
		t.Fatal("stopped conversation hinted work")
	}
	if len(f.must(Select{Recipient: b.Enrollment.ID}).Dispatches) != 0 {
		t.Fatal("hint and selection disagreed about eligibility")
	}
}

// expirable runs the read-only hint the discovery pass uses to decide whether
// entering the Expire transaction can change anything at all.
func (f *fixture) expirable() (out bool) {
	f.t.Helper()
	if err := statetest.Write(f.owner, func(tx *brokerstate.WriteTx) error {
		var err error
		out, err = NewView(tx, f.limits, f.now).Expirable()
		return err
	}); err != nil {
		f.t.Fatal(err)
	}
	return
}

func TestExpirableHintTracksExpire(t *testing.T) {
	f := newFixture(t)
	e := f.must(Enroll{Provider: "codex", Conversation: "pending", Endpoint: "/registered/daemon.sock", Label: "pending"})
	if f.expirable() {
		t.Fatal("live readiness deadline hinted expiry")
	}
	f.now = f.now.Add(f.limits.PendingTTL)
	if !f.expirable() {
		t.Fatal("elapsed readiness deadline did not hint expiry")
	}
	f.must(Expire{})
	if f.expirable() {
		t.Fatal("expiry left an expirable enrollment behind")
	}
	if got := f.must(Enroll{Provider: "codex", Conversation: "reenrolled", Endpoint: "/registered/daemon.sock", Label: "reenrolled"}); got.Enrollment.State != "pending" {
		t.Fatalf("expiry did not free the failed enrollment: %s", e.Enrollment.State)
	}
	a := f.enrolled("a")
	b := f.enrolled("b")
	f.send(a, b, "deadline")
	if f.expirable() {
		t.Fatal("live conversation deadline hinted expiry")
	}
	f.now = f.now.Add(f.limits.DefaultDeadline)
	if !f.expirable() {
		t.Fatal("elapsed conversation deadline did not hint expiry")
	}
	f.must(Expire{})
	if f.expirable() {
		t.Fatal("expiry left an expirable conversation behind")
	}
}
