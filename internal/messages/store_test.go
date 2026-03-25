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

	msg, err := s.Send("alice", "bob", "hello")
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

	s.Send("alice", "bob", "msg for bob")
	s.Send("alice", "charlie", "msg for charlie")
	s.Send("bob", "charlie", "another for charlie")

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

	s.Send("alice", "bob", "first")
	time.Sleep(10 * time.Millisecond)
	s.Send("alice", "bob", "second")
	time.Sleep(10 * time.Millisecond)
	s.Send("alice", "bob", "third")

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

// TestStore_MarkPushed — state changes from queued to pushed
func TestStore_MarkPushed(t *testing.T) {
	s := newTestStore(t)

	msg, _ := s.Send("alice", "bob", "hello")
	if msg.State != "queued" {
		t.Errorf("initial state = %q, want queued", msg.State)
	}

	err := s.MarkPushed(msg.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Verify state changed
	messages, _ := s.Inbox("bob")
	if len(messages) != 1 {
		t.Fatalf("inbox len = %d, want 1", len(messages))
	}
	if messages[0].State != "pushed" {
		t.Errorf("state after mark = %q, want pushed", messages[0].State)
	}
	if messages[0].PushedAt == "" {
		t.Error("pushed_at should be set")
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
			_, err := s.Send("alice", "bob", fmt.Sprintf("msg-%d", n))
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
			_, err := s.Send(tt.from, tt.to, tt.body)
			if err == nil {
				t.Errorf("expected error for %s", tt.name)
			}
		})
	}
}
