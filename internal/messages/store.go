package messages

import (
	"database/sql"
	"fmt"
	"time"
)

// Message represents a message in the store
type Message struct {
	ID        int64  `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Body      string `json:"body"`
	State     string `json:"state"`
	CreatedAt string `json:"created_at"`
	PushedAt  string `json:"pushed_at,omitempty"`
}

// Store manages message persistence
type Store struct {
	db *sql.DB
}

// NewStore creates a new message store
func NewStore(db *sql.DB) (*Store, error) {
	// Create messages table
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
		return nil, fmt.Errorf("creating messages table: %w", err)
	}

	// CLASS 5 FIX (G5): Add index on to_name and state for inbox queries
	indexSchema := `
	CREATE INDEX IF NOT EXISTS idx_messages_to_name ON messages(to_name, state);
	`
	if _, err := db.Exec(indexSchema); err != nil {
		return nil, fmt.Errorf("creating messages index: %w", err)
	}

	return &Store{db: db}, nil
}

// Send inserts a new message into the store
func (s *Store) Send(from, to, body string) (*Message, error) {
	// Validate inputs
	if from == "" {
		return nil, fmt.Errorf("from name required")
	}
	if to == "" {
		return nil, fmt.Errorf("to name required")
	}
	if body == "" {
		return nil, fmt.Errorf("message body required")
	}

	now := time.Now().UTC().Format(time.RFC3339)

	result, err := s.db.Exec(
		`INSERT INTO messages (from_name, to_name, body, state, created_at)
		 VALUES (?, ?, ?, 'queued', ?)`,
		from, to, body, now,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting message: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("getting message ID: %w", err)
	}

	return &Message{
		ID:        id,
		From:      from,
		To:        to,
		Body:      body,
		State:     "queued",
		CreatedAt: now,
	}, nil
}

// Inbox returns all queued and pushed messages for a recipient
func (s *Store) Inbox(name string) ([]*Message, error) {
	rows, err := s.db.Query(
		`SELECT id, from_name, to_name, body, state, created_at, pushed_at
		 FROM messages
		 WHERE to_name = ? AND state IN ('queued', 'pushed')
		 ORDER BY id ASC`,
		name,
	)
	if err != nil {
		return nil, fmt.Errorf("querying inbox: %w", err)
	}
	defer rows.Close()

	var messages []*Message
	for rows.Next() {
		var m Message
		var pushedAt sql.NullString
		if err := rows.Scan(&m.ID, &m.From, &m.To, &m.Body, &m.State, &m.CreatedAt, &pushedAt); err != nil {
			return nil, fmt.Errorf("scanning message: %w", err)
		}
		if pushedAt.Valid {
			m.PushedAt = pushedAt.String
		}
		messages = append(messages, &m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating messages: %w", err)
	}

	// Return empty slice (not nil) when no messages
	if messages == nil {
		messages = []*Message{}
	}

	return messages, nil
}

// MarkPushed updates a message state to 'pushed'
func (s *Store) MarkPushed(id int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(
		`UPDATE messages SET state = 'pushed', pushed_at = ? WHERE id = ?`,
		now, id,
	)
	if err != nil {
		return fmt.Errorf("marking message as pushed: %w", err)
	}
	return nil
}

