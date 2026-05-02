package messages

import (
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/seungpyoson/waggle/internal/config"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)

	// Set pragmas (same as production)
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA busy_timeout=%d", config.Defaults.BusyTimeout.Milliseconds())); err != nil {
		db.Close()
		t.Fatal(err)
	}

	s, err := NewStore(db)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return s
}

// TestStore_SendAndInbox — send message, inbox returns it
func TestStore_SendAndInbox(t *testing.T) {
	s := newTestStore(t)

	msg, err := s.Send("alice", "bob", "hello", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.ID == 0 {
		t.Fatal("expected non-zero ID")
	}
	if msg.From != "alice" {
		t.Errorf("from = %q, want alice", msg.From)
	}
	if msg.To != "bob" {
		t.Errorf("to = %q, want bob", msg.To)
	}
	if msg.Body != "hello" {
		t.Errorf("body = %q, want hello", msg.Body)
	}
	if msg.State != "queued" {
		t.Errorf("state = %q, want queued", msg.State)
	}

	// Check inbox
	messages, err := s.Inbox("bob")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("inbox len = %d, want 1", len(messages))
	}
	if messages[0].Body != "hello" {
		t.Errorf("inbox message body = %q, want hello", messages[0].Body)
	}
}

// TestStore_InboxFiltersByRecipient — send to A and B, A only sees A's
func TestStore_InboxFiltersByRecipient(t *testing.T) {
	s := newTestStore(t)

	s.Send("alice", "bob", "msg for bob", "", nil)
	s.Send("alice", "charlie", "msg for charlie", "", nil)
	s.Send("bob", "charlie", "another for charlie", "", nil)

	bobInbox, _ := s.Inbox("bob")
	if len(bobInbox) != 1 {
		t.Errorf("bob inbox len = %d, want 1", len(bobInbox))
	}
	if len(bobInbox) > 0 && bobInbox[0].Body != "msg for bob" {
		t.Errorf("bob got wrong message: %q", bobInbox[0].Body)
	}

	charlieInbox, _ := s.Inbox("charlie")
	if len(charlieInbox) != 2 {
		t.Errorf("charlie inbox len = %d, want 2", len(charlieInbox))
	}
}

// TestStore_InboxOrdering — 3 messages arrive in order
func TestStore_InboxOrdering(t *testing.T) {
	s := newTestStore(t)

	s.Send("alice", "bob", "first", "", nil)
	time.Sleep(10 * time.Millisecond)
	s.Send("alice", "bob", "second", "", nil)
	time.Sleep(10 * time.Millisecond)
	s.Send("alice", "bob", "third", "", nil)

	messages, _ := s.Inbox("bob")
	if len(messages) != 3 {
		t.Fatalf("inbox len = %d, want 3", len(messages))
	}
	if messages[0].Body != "first" {
		t.Errorf("messages[0] = %q, want first", messages[0].Body)
	}
	if messages[1].Body != "second" {
		t.Errorf("messages[1] = %q, want second", messages[1].Body)
	}
	if messages[2].Body != "third" {
		t.Errorf("messages[2] = %q, want third", messages[2].Body)
	}
}

// TestStore_MarkPushed — state changes from queued to pushed, then inbox marks as seen
func TestStore_MarkPushed(t *testing.T) {
	s := newTestStore(t)

	msg, _ := s.Send("alice", "bob", "hello", "", nil)
	if msg.State != "queued" {
		t.Errorf("initial state = %q, want queued", msg.State)
	}

	err := s.MarkPushed(msg.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Verify state changed (Task 48: inbox call marks pushed→seen)
	messages, _ := s.Inbox("bob")
	if len(messages) != 1 {
		t.Fatalf("inbox len = %d, want 1", len(messages))
	}
	if messages[0].State != "seen" {
		t.Errorf("state after inbox = %q, want seen (Task 48: inbox marks as seen)", messages[0].State)
	}
	if messages[0].PushedAt == "" {
		t.Error("pushed_at should be set")
	}
	if messages[0].SeenAt == "" {
		t.Error("seen_at should be set after inbox call")
	}
}

// TestStore_SendConcurrent — 10 goroutines, all messages stored
func TestStore_SendConcurrent(t *testing.T) {
	s := newTestStore(t)

	var wg sync.WaitGroup
	count := 10
	wg.Add(count)

	for i := 0; i < count; i++ {
		go func(n int) {
			defer wg.Done()
			_, err := s.Send("alice", "bob", fmt.Sprintf("msg-%d", n), "", nil)
			if err != nil {
				t.Errorf("send failed: %v", err)
			}
		}(i)
	}

	wg.Wait()

	messages, _ := s.Inbox("bob")
	if len(messages) != count {
		t.Errorf("inbox len = %d, want %d", len(messages), count)
	}
}

// TestStore_EmptyInbox — inbox with no messages returns empty slice (not nil, not error)
func TestStore_EmptyInbox(t *testing.T) {
	s := newTestStore(t)

	messages, err := s.Inbox("nobody")
	if err != nil {
		t.Errorf("empty inbox should not error: %v", err)
	}
	if messages == nil {
		t.Error("inbox should return empty slice, not nil")
	}
	if len(messages) != 0 {
		t.Errorf("inbox len = %d, want 0", len(messages))
	}
}

// TestStore_SendValidation — empty from/to/body returns error
func TestStore_SendValidation(t *testing.T) {
	s := newTestStore(t)

	tests := []struct {
		name string
		from string
		to   string
		body string
	}{
		{"empty from", "", "bob", "hello"},
		{"empty to", "alice", "", "hello"},
		{"empty body", "alice", "bob", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.Send(tt.from, tt.to, tt.body, "", nil)
			if err == nil {
				t.Errorf("expected error for %s", tt.name)
			}
		})
	}
}

// ========== Task 48 Phase A: Failing Tests ==========

// TestStore_Ack — ack sets state=acked + acked_at timestamp
func TestStore_Ack(t *testing.T) {
	s := newTestStore(t)

	msg, _ := s.Send("alice", "bob", "hello", "", nil)

	// Ack the message
	err := s.Ack(msg.ID, "bob")
	if err != nil {
		t.Fatalf("ack failed: %v", err)
	}

	// Verify state changed to acked
	messages, _ := s.Inbox("bob")
	// Message should not be in inbox anymore (acked messages are terminal)
	for _, m := range messages {
		if m.ID == msg.ID {
			t.Errorf("acked message should not be in inbox")
		}
	}

	// Query the message directly to verify state
	var state, ackedAt string
	err = s.db.QueryRow("SELECT state, acked_at FROM messages WHERE id = ?", msg.ID).Scan(&state, &ackedAt)
	if err != nil {
		t.Fatalf("query message: %v", err)
	}
	if state != "acked" {
		t.Errorf("state = %q, want acked", state)
	}
	if ackedAt == "" {
		t.Error("acked_at should be set")
	}
}

// TestStore_AckNonexistent — ack unknown id returns ErrMessageNotFound
func TestStore_AckNonexistent(t *testing.T) {
	s := newTestStore(t)

	err := s.Ack(99999, "bob")
	if err == nil {
		t.Fatal("expected error for nonexistent message")
	}
	if err != ErrMessageNotFound {
		t.Errorf("expected ErrMessageNotFound, got %v", err)
	}
}

// TestStore_AckForbidden — caller != to_name returns ErrNotRecipient
func TestStore_AckForbidden(t *testing.T) {
	s := newTestStore(t)

	msg, _ := s.Send("alice", "bob", "hello", "", nil)

	// Alice tries to ack a message sent to bob
	err := s.Ack(msg.ID, "alice")
	if err == nil {
		t.Fatal("expected error when non-recipient acks")
	}
	if err != ErrNotRecipient {
		t.Errorf("expected ErrNotRecipient, got %v", err)
	}
}

// TestStore_AckIdempotent — ack already-acked message: success (idempotent)
func TestStore_AckIdempotent(t *testing.T) {
	s := newTestStore(t)

	msg, _ := s.Send("alice", "bob", "hello", "", nil)

	// Ack once
	err := s.Ack(msg.ID, "bob")
	if err != nil {
		t.Fatalf("first ack failed: %v", err)
	}

	// Ack again — should succeed (idempotent)
	err = s.Ack(msg.ID, "bob")
	if err != nil {
		t.Errorf("second ack should be idempotent, got error: %v", err)
	}
}

// TestStore_ConcurrentAck — 10 goroutines ack same message; exactly one wins
func TestStore_ConcurrentAck(t *testing.T) {
	s := newTestStore(t)

	msg, _ := s.Send("alice", "bob", "hello", "", nil)

	var wg sync.WaitGroup
	count := 10
	wg.Add(count)

	for i := 0; i < count; i++ {
		go func() {
			defer wg.Done()
			s.Ack(msg.ID, "bob")
		}()
	}

	wg.Wait()

	// Verify message is acked exactly once
	var state string
	err := s.db.QueryRow("SELECT state FROM messages WHERE id = ?", msg.ID).Scan(&state)
	if err != nil {
		t.Fatalf("query message: %v", err)
	}
	if state != "acked" {
		t.Errorf("state = %q, want acked", state)
	}
}

// TestStore_MarkExpired — send with ttl=1; sleep 1s; MarkExpired; state=expired
func TestStore_MarkExpired(t *testing.T) {
	s := newTestStore(t)

	ttl := 1
	s.Send("alice", "bob", "hello", "", &ttl)

	// Wait for TTL to expire
	time.Sleep(2 * time.Second)

	// Mark expired
	count, err := s.MarkExpired()
	if err != nil {
		t.Fatalf("MarkExpired failed: %v", err)
	}
	if count != 1 {
		t.Errorf("MarkExpired count = %d, want 1", count)
	}

	// Verify state changed to expired
	var state string
	err = s.db.QueryRow("SELECT state FROM messages WHERE to_name = 'bob'").Scan(&state)
	if err != nil {
		t.Fatalf("query message: %v", err)
	}
	if state != "expired" {
		t.Errorf("state = %q, want expired", state)
	}
}

// TestStore_MarkExpired_NoTTL — messages without ttl never marked expired
func TestStore_MarkExpired_NoTTL(t *testing.T) {
	s := newTestStore(t)

	s.Send("alice", "bob", "hello", "", nil)

	// Wait and mark expired
	time.Sleep(2 * time.Second)
	count, err := s.MarkExpired()
	if err != nil {
		t.Fatalf("MarkExpired failed: %v", err)
	}
	if count != 0 {
		t.Errorf("MarkExpired count = %d, want 0 (no TTL)", count)
	}

	// Verify message still queued
	messages, _ := s.Inbox("bob")
	if len(messages) != 1 {
		t.Errorf("inbox len = %d, want 1", len(messages))
	}
}

// TestStore_InboxExcludesExpired — expired message absent from Inbox
func TestStore_InboxExcludesExpired(t *testing.T) {
	s := newTestStore(t)

	ttl := 1
	s.Send("alice", "bob", "hello", "", &ttl)

	// Wait for TTL to expire
	time.Sleep(2 * time.Second)

	// Inbox should exclude expired message (belt-and-suspenders SQL filter)
	messages, err := s.Inbox("bob")
	if err != nil {
		t.Fatalf("Inbox failed: %v", err)
	}
	if len(messages) != 0 {
		t.Errorf("inbox len = %d, want 0 (expired message excluded)", len(messages))
	}
}

// TestStore_InboxMarksSeen — inbox call transitions pushed→seen; sets seen_at
func TestStore_InboxMarksSeen(t *testing.T) {
	s := newTestStore(t)

	msg, _ := s.Send("alice", "bob", "hello", "", nil)

	// Mark as pushed
	s.MarkPushed(msg.ID)

	// Call inbox — should transition to seen
	messages, err := s.Inbox("bob")
	if err != nil {
		t.Fatalf("Inbox failed: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("inbox len = %d, want 1", len(messages))
	}

	// Verify state changed to seen
	var state, seenAt string
	err = s.db.QueryRow("SELECT state, seen_at FROM messages WHERE id = ?", msg.ID).Scan(&state, &seenAt)
	if err != nil {
		t.Fatalf("query message: %v", err)
	}
	if state != "seen" {
		t.Errorf("state = %q, want seen", state)
	}
	if seenAt == "" {
		t.Error("seen_at should be set")
	}
}

func TestStore_ReplayDoesNotMarkSeen(t *testing.T) {
	s := newTestStore(t)

	msg, _ := s.Send("alice", "bob", "hello", "", nil)
	if err := s.MarkPushed(msg.ID); err != nil {
		t.Fatalf("MarkPushed failed: %v", err)
	}

	messages, err := s.Replay("bob")
	if err != nil {
		t.Fatalf("Replay failed: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("replay len = %d, want 1", len(messages))
	}

	var state string
	var seenAt sql.NullString
	err = s.db.QueryRow("SELECT state, seen_at FROM messages WHERE id = ?", msg.ID).Scan(&state, &seenAt)
	if err != nil {
		t.Fatalf("query message: %v", err)
	}
	if state != "pushed" {
		t.Errorf("state = %q, want pushed", state)
	}
	if seenAt.Valid {
		t.Errorf("seen_at = %q, want unset", seenAt.String)
	}
}

// TestStore_PriorityStored — Send with priority=critical; inbox shows priority=critical
func TestStore_PriorityStored(t *testing.T) {
	s := newTestStore(t)

	msg, err := s.Send("alice", "bob", "urgent", "critical", nil)
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if msg.Priority != "critical" {
		t.Errorf("msg.Priority = %q, want critical", msg.Priority)
	}

	// Verify via inbox
	messages, _ := s.Inbox("bob")
	if len(messages) != 1 {
		t.Fatalf("inbox len = %d, want 1", len(messages))
	}
	if messages[0].Priority != "critical" {
		t.Errorf("inbox priority = %q, want critical", messages[0].Priority)
	}
}

// TestStore_PriorityDefault — Send with empty priority; inbox shows priority=normal
func TestStore_PriorityDefault(t *testing.T) {
	s := newTestStore(t)

	msg, err := s.Send("alice", "bob", "hello", "", nil)
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if msg.Priority != "normal" {
		t.Errorf("msg.Priority = %q, want normal", msg.Priority)
	}

	// Verify via inbox
	messages, _ := s.Inbox("bob")
	if len(messages) != 1 {
		t.Fatalf("inbox len = %d, want 1", len(messages))
	}
	if messages[0].Priority != "normal" {
		t.Errorf("inbox priority = %q, want normal", messages[0].Priority)
	}
}

// TestStore_PriorityInvalid — Send with priority=unknown returns error
func TestStore_PriorityInvalid(t *testing.T) {
	s := newTestStore(t)

	_, err := s.Send("alice", "bob", "hello", "unknown", nil)
	if err == nil {
		t.Fatal("expected error for invalid priority")
	}
}

// TestStore_TTLValidation — Send with ttl>MaxTTL returns error
func TestStore_TTLValidation(t *testing.T) {
	s := newTestStore(t)

	ttl := config.Defaults.MaxTTL + 1
	_, err := s.Send("alice", "bob", "hello", "", &ttl)
	if err == nil {
		t.Fatal("expected error for ttl > MaxTTL")
	}
}

// TestStore_SchemaMigration — create v1 schema, insert row, run migration, read row
func TestStore_SchemaMigration(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	// Set pragmas
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec(fmt.Sprintf("PRAGMA busy_timeout=%d", config.Defaults.BusyTimeout.Milliseconds()))

	// Create v1 schema (without new columns)
	schema := `
	CREATE TABLE IF NOT EXISTS messages (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		from_name   TEXT NOT NULL,
		to_name     TEXT NOT NULL,
		body        TEXT NOT NULL,
		state       TEXT DEFAULT 'queued',
		created_at  TEXT NOT NULL,
		pushed_at   TEXT
	);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create v1 schema: %v", err)
	}

	// Insert a v1 row
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = db.Exec(
		`INSERT INTO messages (from_name, to_name, body, state, created_at)
		 VALUES (?, ?, ?, 'queued', ?)`,
		"alice", "bob", "v1 message", now,
	)
	if err != nil {
		t.Fatalf("insert v1 row: %v", err)
	}

	// Run migration (NewStore should run it)
	s, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore (migration): %v", err)
	}

	// Verify v1 row survives and has correct defaults
	messages, err := s.Inbox("bob")
	if err != nil {
		t.Fatalf("Inbox failed: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("inbox len = %d, want 1", len(messages))
	}
	if messages[0].Body != "v1 message" {
		t.Errorf("body = %q, want 'v1 message'", messages[0].Body)
	}
	if messages[0].Priority != "normal" {
		t.Errorf("priority = %q, want normal (default)", messages[0].Priority)
	}
}
