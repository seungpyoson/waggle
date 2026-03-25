package messages

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
)

// Sentinel errors
var (
	ErrMessageNotFound = errors.New("message not found")
	ErrNotRecipient    = errors.New("not recipient")
)

// Message represents a message in the store
type Message struct {
	ID        int64  `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Body      string `json:"body"`
	Priority  string `json:"priority"`
	State     string `json:"state"`
	CreatedAt string `json:"created_at"`
	PushedAt  string `json:"pushed_at,omitempty"`
	SeenAt    string `json:"seen_at,omitempty"`
	AckedAt   string `json:"acked_at,omitempty"`
	TTL       *int   `json:"ttl,omitempty"`
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

	// Run schema migration to add new columns
	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migrating schema: %w", err)
	}

	return &Store{db: db}, nil
}

// migrate adds new columns to the messages table if they don't exist
func migrate(db *sql.DB) error {
	type col struct {
		name string
		ddl  string
	}
	columns := []col{
		{"seen_at", "ALTER TABLE messages ADD COLUMN seen_at TEXT"},
		{"acked_at", "ALTER TABLE messages ADD COLUMN acked_at TEXT"},
		{"priority", "ALTER TABLE messages ADD COLUMN priority TEXT NOT NULL DEFAULT 'normal'"},
		{"ttl", "ALTER TABLE messages ADD COLUMN ttl INTEGER"},
	}
	for _, c := range columns {
		var count int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('messages') WHERE name = ?`, c.name,
		).Scan(&count); err != nil {
			return fmt.Errorf("checking column %s: %w", c.name, err)
		}
		if count == 0 {
			if _, err := db.Exec(c.ddl); err != nil {
				return fmt.Errorf("adding column %s: %w", c.name, err)
			}
		}
	}
	return nil
}

// Send inserts a new message into the store
func (s *Store) Send(from, to, body, priority string, ttl *int) (*Message, error) {
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

	// Validate and default priority
	if priority == "" {
		priority = config.Defaults.DefaultMsgPriority
	}
	validPriorities := map[string]bool{"critical": true, "normal": true, "bulk": true}
	if !validPriorities[priority] {
		return nil, fmt.Errorf("invalid priority: must be critical, normal, or bulk")
	}

	// Validate TTL
	if ttl != nil {
		if *ttl <= 0 {
			return nil, fmt.Errorf("ttl must be positive")
		}
		if *ttl > config.Defaults.MaxTTL {
			return nil, fmt.Errorf("ttl exceeds maximum allowed (%d seconds)", config.Defaults.MaxTTL)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)

	result, err := s.db.Exec(
		`INSERT INTO messages (from_name, to_name, body, priority, ttl, state, created_at)
		 VALUES (?, ?, ?, ?, ?, 'queued', ?)`,
		from, to, body, priority, ttl, now,
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
		Priority:  priority,
		TTL:       ttl,
		State:     "queued",
		CreatedAt: now,
	}, nil
}

// Ack marks a message as acknowledged
func (s *Store) Ack(id int64, caller string) error {
	// Use a transaction for atomic check-then-update
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Check message exists and caller is the recipient
	var toName string
	err = tx.QueryRow("SELECT to_name FROM messages WHERE id = ?", id).Scan(&toName)
	if err != nil {
		if err == sql.ErrNoRows {
			return ErrMessageNotFound
		}
		return fmt.Errorf("query message: %w", err)
	}

	if toName != caller {
		return ErrNotRecipient
	}

	// Update state to acked (idempotent - only update if not already terminal)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = tx.Exec(
		`UPDATE messages SET state = 'acked', acked_at = ?
		 WHERE id = ? AND state NOT IN ('acked', 'expired')`,
		now, id,
	)
	if err != nil {
		return fmt.Errorf("update message: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// MarkExpired marks messages past their TTL as expired
func (s *Store) MarkExpired() (int, error) {
	result, err := s.db.Exec(`
		UPDATE messages SET state = 'expired'
		WHERE state NOT IN ('acked', 'expired')
		  AND ttl IS NOT NULL
		  AND CAST(strftime('%s','now') AS INTEGER) >= CAST(strftime('%s', created_at) AS INTEGER) + ttl
	`)
	if err != nil {
		return 0, fmt.Errorf("marking expired: %w", err)
	}

	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("getting rows affected: %w", err)
	}

	return int(count), nil
}

// Inbox returns all queued, pushed, and seen messages for a recipient
// Excludes expired messages and marks queued/pushed messages as seen
func (s *Store) Inbox(name string) ([]*Message, error) {
	rows, err := s.db.Query(
		`SELECT id, from_name, to_name, body, priority, ttl, state, created_at, pushed_at, seen_at, acked_at
		 FROM messages
		 WHERE to_name = ?
		   AND state IN ('queued', 'pushed', 'seen')
		   AND (ttl IS NULL OR CAST(strftime('%s','now') AS INTEGER) < CAST(strftime('%s', created_at) AS INTEGER) + ttl)
		 ORDER BY id ASC`,
		name,
	)
	if err != nil {
		return nil, fmt.Errorf("querying inbox: %w", err)
	}
	defer rows.Close()

	var messages []*Message
	var idsToMarkSeen []int64
	for rows.Next() {
		var m Message
		var pushedAt, seenAt, ackedAt sql.NullString
		var ttl sql.NullInt64
		if err := rows.Scan(&m.ID, &m.From, &m.To, &m.Body, &m.Priority, &ttl, &m.State, &m.CreatedAt, &pushedAt, &seenAt, &ackedAt); err != nil {
			return nil, fmt.Errorf("scanning message: %w", err)
		}
		if pushedAt.Valid {
			m.PushedAt = pushedAt.String
		}
		if seenAt.Valid {
			m.SeenAt = seenAt.String
		}
		if ackedAt.Valid {
			m.AckedAt = ackedAt.String
		}
		if ttl.Valid {
			ttlInt := int(ttl.Int64)
			m.TTL = &ttlInt
		}

		// Track messages that need to be marked as seen
		if m.State == "queued" || m.State == "pushed" {
			idsToMarkSeen = append(idsToMarkSeen, m.ID)
		}

		messages = append(messages, &m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating messages: %w", err)
	}

	// Mark queued/pushed messages as seen
	if len(idsToMarkSeen) > 0 {
		now := time.Now().UTC().Format(time.RFC3339)
		for _, id := range idsToMarkSeen {
			_, err := s.db.Exec(
				`UPDATE messages SET state = 'seen', seen_at = ? WHERE id = ?`,
				now, id,
			)
			if err != nil {
				return nil, fmt.Errorf("marking message %d as seen: %w", id, err)
			}
			// Update the state in the returned messages too
			for _, msg := range messages {
				if msg.ID == id {
					msg.State = "seen"
					msg.SeenAt = now
					break
				}
			}
		}
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

