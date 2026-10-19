package messages

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/config"
)

var (
	ErrNotFound     = errors.New("canonical message or enrollment not found")
	ErrCredential   = errors.New("missing or invalid enrollment credential; start the broker and restart the provider session")
	ErrNotBound     = errors.New("session is not receive-bound; inspect enrollment diagnostics")
	ErrBackpressure = errors.New("recipient queue capacity reached")
	ErrStopped      = errors.New("conversation cannot dispatch: stopped, expired, or budget exhausted")
	ErrConflict     = errors.New("request conflicts with canonical state")
)

// View is read-only domain access over any fenced capability. A read snapshot
// and a write transaction answer the same questions through it.
type View struct {
	r      brokerstate.Reader
	limits config.MessagingConfig
	now    time.Time
}

func NewView(r brokerstate.Reader, limits config.MessagingConfig, now time.Time) *View {
	return &View{r: r, limits: limits, now: now.UTC()}
}

// Store adds mutation to a View over a write transaction. It is a domain view
// of one fenced transaction, not another database owner.
type Store struct {
	*View
	tx *brokerstate.WriteTx
}

func NewStore(tx *brokerstate.WriteTx, limits config.MessagingConfig, now time.Time) *Store {
	return &Store{View: NewView(tx, limits, now), tx: tx}
}

// Apply is the only mutation entrypoint. Errors must propagate to the owner so
// the entire command rolls back, including authentication, capacity and intent.
func (s *Store) Apply(command Command) (Result, error) {
	if err := s.limits.Validate(); err != nil {
		return Result{}, err
	}
	if err := s.tx.RequireActive(); err != nil {
		return Result{}, err
	}
	switch c := command.(type) {
	case Enroll:
		return s.enroll(c)
	case Bind:
		return s.bind(c)
	case Unavailable:
		if err := s.evidence(c.Evidence); err != nil {
			return Result{}, err
		}
		e, err := s.Enrollment(c.ID)
		if err != nil {
			return Result{}, err
		}
		if e.State != "bound" && e.State != "disconnected" && e.State != "pending" {
			return Result{}, fmt.Errorf("%w: native availability cannot change terminal enrollment", ErrConflict)
		}
		_, err = s.tx.Exec("UPDATE enrollments SET state=CASE WHEN state='bound' THEN 'disconnected' ELSE state END, verified_generation=NULL, evidence=? WHERE id=?", c.Evidence, c.ID)
		return Result{}, err
	case Restart:
		var live int
		if err := s.tx.Scan("SELECT count(*) FROM enrollments WHERE state IN ('pending','bound','disconnected')", nil, &live); err != nil {
			return Result{}, err
		}
		if live > s.limits.EnrollmentCapacity {
			return Result{}, fmt.Errorf("configured enrollment capacity is below existing live enrollment count")
		}
		_, err := s.tx.Exec("UPDATE enrollments SET state='disconnected', verified_generation=NULL WHERE state='bound'")
		return Result{}, err
	case Enqueue:
		return s.enqueue(c)
	case Reply:
		return s.reply(c)
	case Select:
		return s.selectWork(c)
	case Observe:
		switch c.Kind {
		case "uncertain", "accepted", "held", "not_submitted", "refused", "dropped":
		default:
			return Result{}, fmt.Errorf("invalid native observation kind")
		}
		return Result{}, s.record(c)
	case Consume:
		return s.consume(c)
	case Stop:
		return s.stop(c)
	case Retire:
		return s.retire(c)
	case Expire:
		if _, err := s.tx.Exec("UPDATE enrollments SET state='failed', evidence='native readiness deadline elapsed' WHERE "+expiredEnrollment, s.now.UnixNano()); err != nil {
			return Result{}, err
		}
		_, err := s.tx.Exec("UPDATE conversations SET stopped_reason='deadline_elapsed' WHERE "+expiredConversation, s.now.UnixNano())
		return Result{}, err
	default:
		return Result{}, fmt.Errorf("unsupported message transition")
	}
}

func (s *Store) field(value, name string) error {
	if len(value) == 0 || len(value) > s.limits.MaxFieldBytes {
		return fmt.Errorf("%s must contain 1..%d bytes", name, s.limits.MaxFieldBytes)
	}
	return nil
}

func (s *Store) evidence(value string) error {
	if len(value) == 0 || len(value) > s.limits.MaxEvidenceBytes {
		return fmt.Errorf("native evidence must contain 1..%d bytes", s.limits.MaxEvidenceBytes)
	}
	return nil
}

func (s *Store) body(value string) error {
	if len(value) == 0 || len(value) > s.limits.MaxBodyBytes {
		return fmt.Errorf("message body must contain 1..%d bytes", s.limits.MaxBodyBytes)
	}
	return nil
}

func credentialHash(value string) []byte { hash := sha256.Sum256([]byte(value)); return hash[:] }

func (s *Store) enroll(c Enroll) (Result, error) {
	if c.Provider != "codex" && c.Provider != "claude-code" {
		return Result{}, fmt.Errorf("provider does not support native enrollment")
	}
	for _, f := range []struct{ value, name string }{{c.Conversation, "provider conversation"}, {c.Label, "label"}} {
		if err := s.field(f.value, f.name); err != nil {
			return Result{}, err
		}
	}
	if !filepath.IsAbs(c.Endpoint) || len(c.Endpoint) > s.limits.MaxFieldBytes {
		return Result{}, fmt.Errorf("enrollment requires a bounded absolute native socket endpoint")
	}
	var blocked int
	if err := s.tx.Scan(`SELECT count(*) FROM recipient_barrier b JOIN enrollments e ON e.id=b.recipient WHERE e.provider=? AND e.conversation=?`, []any{c.Provider, c.Conversation}, &blocked); err != nil {
		return Result{}, err
	}
	if blocked != 0 {
		return Result{}, fmt.Errorf("%w: provider conversation still has unresolved retained input", ErrConflict)
	}
	var live int
	if err := s.tx.Scan("SELECT count(*) FROM enrollments WHERE state IN ('pending','bound','disconnected')", nil, &live); err != nil {
		return Result{}, err
	}
	if live >= s.limits.EnrollmentCapacity {
		return Result{}, fmt.Errorf("enrollment capacity reached")
	}
	id, credential := rand.Text(), rand.Text()
	_, err := s.tx.Exec(`INSERT INTO enrollments(id,provider,conversation,endpoint,label,credential_hash,state,pending_until) VALUES(?,?,?,?,?,?,'pending',?)`, id, c.Provider, c.Conversation, c.Endpoint, c.Label, credentialHash(credential), s.now.Add(s.limits.PendingTTL).UnixNano())
	if err != nil {
		return Result{}, fmt.Errorf("enroll exact conversation: %w", err)
	}
	enrollment, err := s.Enrollment(id)
	return Result{Enrollment: enrollment, Credential: credential}, err
}

func (s *Store) bind(c Bind) (Result, error) {
	if err := s.evidence(c.Evidence); err != nil {
		return Result{}, err
	}
	changed, err := s.tx.Exec(`UPDATE enrollments SET state='bound', evidence=?, verified_generation=(SELECT generation FROM broker_owner WHERE singleton=1)
	 WHERE id=? AND conversation=? AND endpoint=? AND (state IN ('bound','disconnected') OR (state='pending' AND pending_until>?))`, c.Evidence, c.ID, c.Conversation, c.Endpoint, s.now.UnixNano())
	if err != nil {
		return Result{}, err
	}
	n, err := changed.RowsAffected()
	if err != nil {
		return Result{}, err
	}
	if n != 1 {
		return Result{}, fmt.Errorf("%w: native binding mismatched, expired, or invalid state", ErrConflict)
	}
	e, err := s.Enrollment(c.ID)
	return Result{Enrollment: e}, err
}

func (v *View) Enrollment(id string) (Enrollment, error) {
	var e Enrollment
	err := v.r.Scan("SELECT id,provider,conversation,endpoint,label,state,evidence FROM enrollments WHERE id=?", []any{id}, &e.ID, &e.Provider, &e.Conversation, &e.Endpoint, &e.Label, &e.State, &e.Evidence)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

// Authenticate establishes scope only. Sending additionally requires a bound
// incarnation; a retired recipient may still provide a late consumption receipt.
func (v *View) Authenticate(credential string) (Enrollment, error) {
	if credential == "" {
		return Enrollment{}, ErrCredential
	}
	var id string
	err := v.r.Scan("SELECT id FROM enrollments WHERE credential_hash=?", []any{credentialHash(credential)}, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return Enrollment{}, ErrCredential
	}
	if err != nil {
		return Enrollment{}, err
	}
	return v.Enrollment(id)
}

func (s *Store) requireBound(id string) error {
	var bound int
	err := s.tx.Scan(`SELECT count(*) FROM enrollments e JOIN broker_owner o ON o.singleton=1
	 WHERE e.id=? AND e.state='bound' AND e.verified_generation=o.generation`, []any{id}, &bound)
	if err != nil {
		return err
	}
	if bound != 1 {
		return ErrNotBound
	}
	return nil
}

func requestHash(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	return hash[:], nil
}

// duplicate is idempotency validation, not another enqueue/dispatch path.
func (s *Store) duplicate(sender, request string, hash []byte) (Message, bool, error) {
	var id string
	var stored []byte
	err := s.tx.Scan("SELECT id,request_hash FROM messages WHERE sender=? AND request_id=?", []any{sender, request}, &id, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, false, nil
	}
	if err != nil {
		return Message{}, false, err
	}
	if string(stored) != string(hash) {
		return Message{}, false, fmt.Errorf("%w: request ID reused with different inputs", ErrConflict)
	}
	m, err := s.Message(id)
	return m, true, err
}

func (s *Store) capacity(recipient string) error {
	var count int
	err := s.tx.Scan(`SELECT count(*) FROM message_status WHERE recipient=? AND possession NOT IN ('consumed','not_retained')
	 AND (attempt_id IS NOT NULL OR (stopped_reason='' AND deadline>? AND remaining_hops>0))`, []any{recipient, s.now.UnixNano()}, &count)
	if err != nil {
		return err
	}
	if count >= s.limits.QueueCapacity {
		return ErrBackpressure
	}
	return nil
}

func (s *Store) enqueue(c Enqueue) (Result, error) {
	sender, err := s.Authenticate(c.Credential)
	if err != nil {
		return Result{}, err
	}
	if err = s.requireBound(sender.ID); err != nil {
		return Result{}, err
	}
	if err = s.field(c.RequestID, "request ID"); err != nil {
		return Result{}, err
	}
	if err = s.body(c.Body); err != nil {
		return Result{}, err
	}
	c.Credential = ""
	hash, err := requestHash(c)
	if err != nil {
		return Result{}, err
	}
	prior, exists, err := s.duplicate(sender.ID, c.RequestID, hash)
	if err != nil || exists {
		return Result{Message: prior}, err
	}
	recipient, err := s.Enrollment(c.Recipient)
	if err != nil {
		return Result{}, err
	}
	if c.Offline {
		if recipient.State != "bound" && recipient.State != "disconnected" {
			return Result{}, ErrNotBound
		}
	} else if err = s.requireBound(recipient.ID); err != nil {
		return Result{}, err
	}
	within := c.Within
	if within == 0 {
		within = s.limits.DefaultDeadline
	}
	if within <= 0 || within > s.limits.MaxDeadline {
		return Result{}, fmt.Errorf("conversation duration must be positive and within the configured maximum")
	}
	if c.Hops <= 0 || c.Hops > s.limits.MaxHops {
		return Result{}, fmt.Errorf("conversation hop budget outside configured bounds")
	}
	if err = s.capacity(recipient.ID); err != nil {
		return Result{}, err
	}
	conversation := rand.Text()
	if _, err = s.tx.Exec("INSERT INTO conversations(id,deadline,remaining_hops) VALUES(?,?,?)", conversation, s.now.Add(within).UnixNano(), c.Hops); err != nil {
		return Result{}, err
	}
	return s.insert(sender.ID, recipient.ID, conversation, "", c.RequestID, c.Body, hash)
}

func (s *Store) insert(sender, recipient, conversation, replyTo, request, body string, hash []byte) (Result, error) {
	id := rand.Text()
	_, err := s.tx.Exec(`INSERT INTO messages(id,sender,recipient,conversation_id,reply_to,request_id,request_hash,body,created_at)
	 VALUES(?,?,?,?,NULLIF(?,''),?,?,?,?)`, id, sender, recipient, conversation, replyTo, request, hash, body, s.now.UnixNano())
	if err != nil {
		return Result{}, err
	}
	m, err := s.Message(id)
	return Result{Message: m}, err
}

func (s *Store) reply(c Reply) (Result, error) {
	sender, err := s.Authenticate(c.Credential)
	if err != nil {
		return Result{}, err
	}
	if err = s.field(c.RequestID, "request ID"); err != nil {
		return Result{}, err
	}
	if err = s.body(c.Body); err != nil {
		return Result{}, err
	}
	c.Credential = ""
	hash, err := requestHash(c)
	if err != nil {
		return Result{}, err
	}
	prior, exists, err := s.duplicate(sender.ID, c.RequestID, hash)
	if err != nil || exists {
		return Result{Message: prior}, err
	}
	original, err := s.Message(c.MessageID)
	if err != nil {
		return Result{}, err
	}
	if original.Recipient != sender.ID || original.Attempt == "" {
		return Result{}, fmt.Errorf("%w: reply requires the original recipient and submission intent", ErrConflict)
	}
	if err = s.capacity(original.Sender); err != nil {
		return Result{}, err
	}
	if err = s.record(Observe{Attempt: original.Attempt, Recipient: sender.ID, Kind: "consumed", Evidence: "authenticated correlated reply"}); err != nil {
		return Result{}, err
	}
	// Deadline, stop and hop budget are inherited. Late replies remain inspectable
	// but selection cannot turn them into fresh conversations or reset budgets.
	return s.insert(sender.ID, original.Sender, original.Conversation, original.ID, c.RequestID, c.Body, hash)
}

// expiredEnrollment and expiredConversation are the two expiry predicates, each
// taking the caller's current time. They exist once so a read snapshot and the
// Expire transition can never disagree about what has elapsed.
const (
	expiredEnrollment   = `state='pending' AND pending_until<=?`
	expiredConversation = `stopped_reason='' AND deadline<=?`
)

// Expirable reports whether Expire would change at least one row at this
// snapshot. It is a hint only: Expire remains the sole transition and reapplies
// both predicates under its own fence.
func (v *View) Expirable() (bool, error) {
	var expirable bool
	err := v.r.Scan(`SELECT EXISTS(SELECT 1 FROM enrollments WHERE `+expiredEnrollment+`)
	 OR EXISTS(SELECT 1 FROM conversations WHERE `+expiredConversation+`)`,
		[]any{v.now.UnixNano(), v.now.UnixNano()}, &expirable)
	return expirable, err
}

// eligible is the selection predicate. It exists once so a read snapshot and
// the intent transaction can never disagree about what makes a message
// dispatchable. Its single parameter is the caller's current time.
const eligible = `FROM messages m JOIN conversations c ON c.id=m.conversation_id
	 JOIN enrollments e ON e.id=m.recipient JOIN broker_owner o ON o.singleton=1
	 WHERE e.state='bound' AND e.verified_generation=o.generation AND c.stopped_reason='' AND c.deadline>? AND c.remaining_hops>0
	 AND NOT EXISTS(SELECT 1 FROM attempts a WHERE a.message_id=m.id)
	 AND NOT EXISTS(SELECT 1 FROM recipient_barrier b WHERE b.recipient=m.recipient)`

// Pending reports whether at least one message for recipient satisfies the
// selection predicate at this snapshot. It is a hint only: Select remains the
// sole eligibility decision and rechecks every constraint under its own fence.
func (v *View) Pending(recipient string) (bool, error) {
	var pending bool
	err := v.r.Scan("SELECT EXISTS(SELECT 1 "+eligible+" AND m.recipient=?)", []any{v.now.UnixNano(), recipient}, &pending)
	return pending, err
}

func (s *Store) selectWork(c Select) (Result, error) {
	var ids []string
	limit := s.limits.ScanLimit
	if c.Recipient != "" {
		limit = 1
	}
	err := s.tx.Query(`SELECT id FROM (
	 SELECT m.id,m.sequence,row_number() OVER (PARTITION BY m.recipient ORDER BY m.sequence) AS position
	 `+eligible+`
	 AND (?='' OR m.recipient=?)
	) WHERE position=1 ORDER BY sequence LIMIT ?`, []any{s.now.UnixNano(), c.Recipient, c.Recipient, limit}, func(rows *sql.Rows) error {
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	result := Result{Dispatches: make([]Dispatch, 0, len(ids))}
	for _, id := range ids {
		m, err := s.Message(id)
		if err != nil {
			return Result{}, err
		}
		changed, err := s.tx.Exec("UPDATE conversations SET remaining_hops=remaining_hops-1 WHERE id=? AND remaining_hops>0", m.Conversation)
		if err != nil {
			return Result{}, err
		}
		n, err := changed.RowsAffected()
		if err != nil {
			return Result{}, err
		}
		// Several recipients can share the final hop; the transaction spends it once.
		if n == 0 {
			continue
		}
		attempt := rand.Text()
		if _, err = s.tx.Exec(`INSERT INTO attempts(id,message_id,owner_id,generation,intent_at)
		 SELECT ?,?,instance_id,generation,? FROM broker_owner WHERE singleton=1`, attempt, id, s.now.UnixNano()); err != nil {
			return Result{}, err
		}
		if _, err = s.tx.Exec("INSERT INTO recipient_barrier(recipient,attempt_id) VALUES(?,?)", m.Recipient, attempt); err != nil {
			return Result{}, err
		}
		m, err = s.Message(id)
		if err != nil {
			return Result{}, err
		}
		recipient, err := s.Enrollment(m.Recipient)
		if err != nil {
			return Result{}, err
		}
		sender, err := s.Enrollment(m.Sender)
		if err != nil {
			return Result{}, err
		}
		result.Dispatches = append(result.Dispatches, Dispatch{Recipient: recipient, Envelope: Envelope{Message: m, SenderLabel: sender.Label, Acknowledgement: "waggle ack " + m.ID + " --attempt=" + attempt}})
	}
	return result, nil
}

func (s *Store) record(c Observe) error {
	if err := s.evidence(c.Evidence); err != nil {
		return err
	}
	if len(c.ProviderRef) > s.limits.MaxFieldBytes {
		return fmt.Errorf("native reference exceeds configured bound")
	}
	var recipient string
	err := s.tx.Scan("SELECT m.recipient FROM attempts a JOIN messages m ON m.id=a.message_id WHERE a.id=?", []any{c.Attempt}, &recipient)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if recipient != c.Recipient {
		return fmt.Errorf("%w: observation names another recipient", ErrConflict)
	}
	var consumed, notRetained int
	err = s.tx.Scan(`SELECT count(*) FILTER(WHERE kind='consumed'),count(*) FILTER(WHERE kind IN ('not_submitted','refused','dropped')) FROM receipts WHERE attempt_id=?`, []any{c.Attempt}, &consumed, &notRetained)
	if err != nil {
		return err
	}
	nonRetention := c.Kind == "not_submitted" || c.Kind == "refused" || c.Kind == "dropped"
	if (consumed > 0 && nonRetention) || (notRetained > 0 && c.Kind == "consumed") {
		return fmt.Errorf("%w: consumption contradicts non-retention evidence", ErrConflict)
	}
	_, err = s.tx.Exec(`INSERT INTO receipts(attempt_id,kind,detail,provider_ref,observed_at) VALUES(?,?,?,?,?)
	 ON CONFLICT(attempt_id,kind,detail,provider_ref) DO NOTHING`, c.Attempt, c.Kind, c.Evidence, c.ProviderRef, s.now.UnixNano())
	if err != nil {
		return err
	}
	if c.Kind == "consumed" || nonRetention {
		_, err = s.tx.Exec("DELETE FROM recipient_barrier WHERE recipient=? AND attempt_id=?", c.Recipient, c.Attempt)
	}
	return err
}

func (s *Store) consume(c Consume) (Result, error) {
	recipient, err := s.Authenticate(c.Credential)
	if err != nil {
		return Result{}, err
	}
	m, err := s.Message(c.MessageID)
	if err != nil {
		return Result{}, err
	}
	if m.Recipient != recipient.ID || m.Attempt == "" || c.Attempt != m.Attempt {
		return Result{}, fmt.Errorf("%w: receipt requires the exact recipient, message and attempt", ErrConflict)
	}
	if err = s.record(Observe{Attempt: c.Attempt, Recipient: recipient.ID, Kind: "consumed", Evidence: "authenticated recipient acknowledgement"}); err != nil {
		return Result{}, err
	}
	m, err = s.Message(c.MessageID)
	return Result{Message: m}, err
}

func (s *Store) stop(c Stop) (Result, error) {
	actor, err := s.Authenticate(c.Credential)
	if err != nil {
		return Result{}, err
	}
	if err = s.field(c.Reason, "stop reason"); err != nil {
		return Result{}, err
	}
	var member int
	err = s.tx.Scan("SELECT count(*) FROM messages WHERE conversation_id=? AND (sender=? OR recipient=?)", []any{c.Conversation, actor.ID, actor.ID}, &member)
	if err != nil {
		return Result{}, err
	}
	if member == 0 {
		return Result{}, ErrNotFound
	}
	_, err = s.tx.Exec("UPDATE conversations SET stopped_reason=? WHERE id=? AND stopped_reason=''", c.Reason, c.Conversation)
	return Result{}, err
}

func (s *Store) retire(c Retire) (Result, error) {
	if err := s.field(c.Reason, "retirement reason"); err != nil {
		return Result{}, err
	}
	if _, err := s.Enrollment(c.ID); err != nil {
		return Result{}, err
	}
	if _, err := s.tx.Exec("UPDATE enrollments SET state='retired', verified_generation=NULL, evidence=? WHERE id=?", c.Reason, c.ID); err != nil {
		return Result{}, err
	}
	_, err := s.tx.Exec(`UPDATE conversations SET stopped_reason='recipient_retired' WHERE stopped_reason='' AND id IN (
	 SELECT conversation_id FROM messages WHERE recipient=? OR sender=?)`, c.ID, c.ID)
	if err != nil {
		return Result{}, err
	}
	e, err := s.Enrollment(c.ID)
	return Result{Enrollment: e}, err
}

const messageColumns = `m.id,m.sender,m.recipient,m.conversation_id,COALESCE(m.reply_to,''),m.request_id,m.body,COALESCE(m.attempt_id,''),m.possession,m.deadline,m.remaining_hops,
 CASE WHEN m.stopped_reason!='' THEN m.stopped_reason WHEN m.deadline<=? THEN 'deadline_elapsed'
 WHEN m.attempt_id IS NULL AND m.remaining_hops=0 THEN 'budget_exhausted'
 WHEN e.state='retired' THEN 'recipient_retired' ELSE 'active' END,
 COALESCE((SELECT a.message_id FROM recipient_barrier b JOIN attempts a ON a.id=b.attempt_id WHERE b.recipient=m.recipient AND a.message_id!=m.id), '')`

func scanMessage(scan func(...any) error) (Message, error) {
	var m Message
	var deadline int64
	err := scan(&m.ID, &m.Sender, &m.Recipient, &m.Conversation, &m.ReplyTo, &m.RequestID, &m.Body, &m.Attempt, &m.Possession, &deadline, &m.RemainingHops, &m.Disposition, &m.BlockedBy)
	m.Deadline = time.Unix(0, deadline).UTC()
	return m, err
}

func (v *View) Message(id string) (Message, error) {
	m, err := scanMessage(func(dest ...any) error {
		return v.r.Scan("SELECT "+messageColumns+" FROM message_status m JOIN enrollments e ON e.id=m.recipient WHERE m.id=?", []any{v.now.UnixNano(), id}, dest...)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

// Inbox is an authenticated, read-only projection. It supplies no receipt.
func (v *View) Inbox(credential string) ([]Message, error) {
	e, err := v.Authenticate(credential)
	if err != nil {
		return nil, err
	}
	items := make([]Message, 0)
	err = v.r.Query("SELECT "+messageColumns+" FROM message_status m JOIN enrollments e ON e.id=m.recipient WHERE m.recipient=? ORDER BY m.sequence DESC LIMIT ?", []any{v.now.UnixNano(), e.ID, v.limits.ScanLimit}, func(rows *sql.Rows) error {
		for rows.Next() {
			m, err := scanMessage(rows.Scan)
			if err != nil {
				return err
			}
			items = append(items, m)
		}
		return nil
	})
	return items, err
}

// Enrollments uses a bounded keyset page for both inspection and observation.
// Callers continue after the final ID; an empty page ends a scan.
func (v *View) Enrollments(after string, scope EnrollmentScope) ([]Enrollment, error) {
	if len(after) > v.limits.MaxFieldBytes {
		return nil, fmt.Errorf("enrollment cursor exceeds the configured bound")
	}
	if scope != AllEnrollments && scope != ObservableEnrollments {
		return nil, fmt.Errorf("enrollment scan requires an explicit scope")
	}
	items := make([]Enrollment, 0)
	err := v.r.Query("SELECT id,provider,conversation,endpoint,label,state,evidence FROM enrollments WHERE id>? AND (? OR state IN ('pending','bound','disconnected')) ORDER BY id LIMIT ?", []any{after, scope == AllEnrollments, v.limits.ScanLimit}, func(rows *sql.Rows) error {
		for rows.Next() {
			var e Enrollment
			if err := rows.Scan(&e.ID, &e.Provider, &e.Conversation, &e.Endpoint, &e.Label, &e.State, &e.Evidence); err != nil {
				return err
			}
			items = append(items, e)
		}
		return nil
	})
	return items, err
}
