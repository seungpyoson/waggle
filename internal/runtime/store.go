package runtime

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
	_ "modernc.org/sqlite"
)

var ErrRecordNotFound = errors.New("record not found")

// Store persists runtime watches and delivery records in a local SQLite file.
type Store struct {
	db *sql.DB
}

// NewStore opens or creates the SQLite database at path and migrates the schema.
func NewStore(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("path required")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create store directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open runtime store: %w", err)
	}
	db.SetMaxOpenConns(1)

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set journal mode: %w", err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA busy_timeout=%d", config.Defaults.BusyTimeout.Milliseconds())); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set busy timeout: %w", err)
	}

	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func migrate(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS watches (
		project_id TEXT NOT NULL,
		agent_name TEXT NOT NULL,
		source TEXT NOT NULL,
		expires_at TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		PRIMARY KEY (project_id, agent_name)
	);

	CREATE TABLE IF NOT EXISTS delivery_records (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		project_id TEXT NOT NULL,
		agent_name TEXT NOT NULL,
		message_id INTEGER NOT NULL,
		from_name TEXT NOT NULL,
		body TEXT NOT NULL,
		sent_at TEXT NOT NULL,
		received_at TEXT NOT NULL,
		notified_at TEXT NOT NULL,
		retry_attempts INTEGER NOT NULL DEFAULT 0,
		retry_next_at TEXT NOT NULL DEFAULT '',
		retry_exhausted_at TEXT NOT NULL DEFAULT '',
		surfaced_at TEXT,
		dismissed_at TEXT,
		UNIQUE (project_id, agent_name, message_id)
	);

	CREATE INDEX IF NOT EXISTS idx_delivery_records_unread
		ON delivery_records(project_id, agent_name, surfaced_at, message_id);
	`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("create runtime schema: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE watches ADD COLUMN expires_at TEXT`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("add watches.expires_at column: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE delivery_records ADD COLUMN retry_attempts INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("add delivery_records.retry_attempts column: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE delivery_records ADD COLUMN retry_next_at TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("add delivery_records.retry_next_at column: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE delivery_records ADD COLUMN retry_exhausted_at TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("add delivery_records.retry_exhausted_at column: %w", err)
	}
	if _, err := db.Exec(`UPDATE delivery_records SET notified_at = '' WHERE notified_at IS NULL`); err != nil {
		return fmt.Errorf("normalize delivery_records.notified_at: %w", err)
	}
	if _, err := db.Exec(`UPDATE delivery_records SET retry_next_at = '' WHERE retry_next_at IS NULL`); err != nil {
		return fmt.Errorf("normalize delivery_records.retry_next_at: %w", err)
	}
	if _, err := db.Exec(`UPDATE delivery_records SET retry_exhausted_at = '' WHERE retry_exhausted_at IS NULL`); err != nil {
		return fmt.Errorf("normalize delivery_records.retry_exhausted_at: %w", err)
	}
	return nil
}

// UpsertWatch inserts or updates a watch keyed by (project_id, agent_name).
func (s *Store) UpsertWatch(w Watch) error {
	if w.ProjectID == "" || w.AgentName == "" {
		return fmt.Errorf("project_id and agent_name required")
	}
	if w.Source == "" {
		return fmt.Errorf("source required")
	}

	now := time.Now().UTC()
	w = normalizedWatch(w, now)
	_, err := s.db.Exec(`
		INSERT INTO watches (project_id, agent_name, source, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, agent_name) DO UPDATE SET
			source = excluded.source,
			expires_at = excluded.expires_at,
			updated_at = excluded.updated_at
	`, w.ProjectID, w.AgentName, w.Source, timeValue(w.ExpiresAt), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("upserting watch: %w", err)
	}
	return nil
}

// RemoveWatch deletes a watch for the given project and agent.
func (s *Store) RemoveWatch(projectID, agentName string) error {
	if projectID == "" || agentName == "" {
		return fmt.Errorf("project_id and agent_name required")
	}
	if _, err := s.db.Exec(
		`DELETE FROM watches WHERE project_id = ? AND agent_name = ?`,
		projectID, agentName,
	); err != nil {
		return fmt.Errorf("removing watch: %w", err)
	}
	return nil
}

// ListWatches returns all persisted watches in a stable order.
func (s *Store) ListWatches() ([]Watch, error) {
	rows, err := s.db.Query(`
		SELECT project_id, agent_name, source, expires_at
		FROM watches
		ORDER BY project_id ASC, agent_name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("listing watches: %w", err)
	}
	defer rows.Close()

	var watches []Watch
	for rows.Next() {
		var w Watch
		var expiresAt sql.NullString
		if err := rows.Scan(&w.ProjectID, &w.AgentName, &w.Source, &expiresAt); err != nil {
			return nil, fmt.Errorf("scanning watch: %w", err)
		}
		w.ExpiresAt = parseTime(expiresAt)
		watches = append(watches, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating watches: %w", err)
	}
	return watches, nil
}

func (s *Store) PruneExpiredWatches(now time.Time) (int64, error) {
	result, err := s.db.Exec(`
		DELETE FROM watches
		WHERE expires_at IS NOT NULL AND expires_at != '' AND expires_at <= ?
	`, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("pruning expired watches: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting pruned watches: %w", err)
	}
	return rowsAffected, nil
}

// AddRecord persists a delivery record for later surfacing.
func (s *Store) AddRecord(rec DeliveryRecord) error {
	if rec.ProjectID == "" || rec.AgentName == "" {
		return fmt.Errorf("project_id and agent_name required")
	}
	if rec.MessageID == 0 {
		return fmt.Errorf("message_id required")
	}
	if rec.FromName == "" {
		return fmt.Errorf("from_name required")
	}
	if rec.Body == "" {
		return fmt.Errorf("body required")
	}
	if rec.SentAt.IsZero() || rec.ReceivedAt.IsZero() {
		return fmt.Errorf("sent_at and received_at required")
	}

	_, err := s.db.Exec(`
		INSERT INTO delivery_records (
			project_id, agent_name, message_id, from_name, body,
			sent_at, received_at, notified_at, retry_attempts, retry_next_at, retry_exhausted_at, surfaced_at, dismissed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, agent_name, message_id) DO UPDATE SET
			from_name = excluded.from_name,
			body = excluded.body,
			sent_at = excluded.sent_at,
			received_at = excluded.received_at,
			notified_at = CASE
				WHEN excluded.notified_at = '' THEN delivery_records.notified_at
				ELSE excluded.notified_at
			END,
			retry_attempts = delivery_records.retry_attempts,
			retry_next_at = delivery_records.retry_next_at,
			retry_exhausted_at = delivery_records.retry_exhausted_at,
			surfaced_at = COALESCE(excluded.surfaced_at, delivery_records.surfaced_at),
			dismissed_at = COALESCE(excluded.dismissed_at, delivery_records.dismissed_at)
	`, rec.ProjectID, rec.AgentName, rec.MessageID, rec.FromName, rec.Body,
		timeValue(rec.SentAt), timeValue(rec.ReceivedAt), textTimeValue(rec.NotifiedAt),
		rec.RetryAttempts, textTimeValue(rec.RetryNextAt), textTimeValue(rec.RetryExhaustedAt),
		timeValue(rec.SurfacedAt), timeValue(rec.DismissedAt))
	if err != nil {
		return fmt.Errorf("adding delivery record: %w", err)
	}
	return nil
}

// AddRecordIfAbsent inserts a delivery record once and reports whether it was created.
func (s *Store) AddRecordIfAbsent(rec DeliveryRecord) (bool, error) {
	if rec.ProjectID == "" || rec.AgentName == "" {
		return false, fmt.Errorf("project_id and agent_name required")
	}
	if rec.MessageID == 0 {
		return false, fmt.Errorf("message_id required")
	}
	if rec.FromName == "" {
		return false, fmt.Errorf("from_name required")
	}
	if rec.Body == "" {
		return false, fmt.Errorf("body required")
	}
	if rec.SentAt.IsZero() || rec.ReceivedAt.IsZero() {
		return false, fmt.Errorf("sent_at and received_at required")
	}

	result, err := s.db.Exec(`
		INSERT INTO delivery_records (
			project_id, agent_name, message_id, from_name, body,
			sent_at, received_at, notified_at, retry_attempts, retry_next_at, retry_exhausted_at, surfaced_at, dismissed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, agent_name, message_id) DO NOTHING
	`, rec.ProjectID, rec.AgentName, rec.MessageID, rec.FromName, rec.Body,
		timeValue(rec.SentAt), timeValue(rec.ReceivedAt), textTimeValue(rec.NotifiedAt),
		rec.RetryAttempts, textTimeValue(rec.RetryNextAt), textTimeValue(rec.RetryExhaustedAt),
		timeValue(rec.SurfacedAt), timeValue(rec.DismissedAt))
	if err != nil {
		return false, fmt.Errorf("adding delivery record if absent: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("counting inserted delivery records: %w", err)
	}
	return rowsAffected > 0, nil
}

// GetRecord returns one delivery record by its deduplication key.
func (s *Store) GetRecord(projectID, agentName string, messageID int64) (DeliveryRecord, error) {
	if projectID == "" || agentName == "" {
		return DeliveryRecord{}, fmt.Errorf("project_id and agent_name required")
	}
	if messageID == 0 {
		return DeliveryRecord{}, fmt.Errorf("message_id required")
	}

	row := s.db.QueryRow(`
		SELECT project_id, agent_name, message_id, from_name, body,
		       sent_at, received_at, notified_at, retry_attempts, retry_next_at, retry_exhausted_at, surfaced_at, dismissed_at
		FROM delivery_records
		WHERE project_id = ? AND agent_name = ? AND message_id = ?
	`, projectID, agentName, messageID)

	rec, err := scanDeliveryRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return DeliveryRecord{}, ErrRecordNotFound
	}
	if err != nil {
		return DeliveryRecord{}, err
	}
	return rec, nil
}

// MarkNotified records successful notification delivery.
func (s *Store) MarkNotified(projectID, agentName string, messageID int64, notifiedAt time.Time) error {
	if projectID == "" || agentName == "" {
		return fmt.Errorf("project_id and agent_name required")
	}
	if messageID == 0 {
		return fmt.Errorf("message_id required")
	}
	if notifiedAt.IsZero() {
		return fmt.Errorf("notified_at required")
	}

	result, err := s.db.Exec(`
		UPDATE delivery_records
		SET notified_at = ?, retry_attempts = 0, retry_next_at = '', retry_exhausted_at = ''
		WHERE project_id = ? AND agent_name = ? AND message_id = ?
	`, textTimeValue(notifiedAt), projectID, agentName, messageID)
	if err != nil {
		return fmt.Errorf("marking notified: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("counting notified rows: %w", err)
	}
	if rowsAffected == 0 {
		return ErrRecordNotFound
	}
	return nil
}

// PendingNotifications returns delivery records that still need local notification.
func (s *Store) PendingNotifications(projectID, agentName string) ([]DeliveryRecord, error) {
	if projectID == "" || agentName == "" {
		return nil, fmt.Errorf("project_id and agent_name required")
	}

	rows, err := s.db.Query(`
		SELECT project_id, agent_name, message_id, from_name, body,
		       sent_at, received_at, notified_at, retry_attempts, retry_next_at, retry_exhausted_at, surfaced_at, dismissed_at
		FROM delivery_records
		WHERE project_id = ? AND agent_name = ? AND notified_at = '' AND dismissed_at IS NULL
		  AND retry_exhausted_at = ''
		  AND (retry_next_at = '' OR retry_next_at <= ?)
		ORDER BY received_at ASC, message_id ASC
	`, projectID, agentName, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("query pending notifications: %w", err)
	}
	defer rows.Close()

	var records []DeliveryRecord
	for rows.Next() {
		rec, err := scanDeliveryRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending notifications: %w", err)
	}
	return records, nil
}

// PendingNotificationsAll returns pending notification records across all watches in stable order.
func (s *Store) PendingNotificationsAll() ([]DeliveryRecord, error) {
	return s.PendingNotificationsBatch(0)
}

// PendingNotificationsBatch returns up to limit pending notification records across all watches in stable order.
// A non-positive limit means no explicit limit.
func (s *Store) PendingNotificationsBatch(limit int) ([]DeliveryRecord, error) {
	query := `
		SELECT project_id, agent_name, message_id, from_name, body,
		       sent_at, received_at, notified_at, retry_attempts, retry_next_at, retry_exhausted_at, surfaced_at, dismissed_at
		FROM delivery_records
		WHERE notified_at = '' AND dismissed_at IS NULL
		  AND retry_exhausted_at = ''
		  AND (retry_next_at = '' OR retry_next_at <= ?)
		ORDER BY project_id ASC, agent_name ASC, message_id ASC
	`

	var (
		rows *sql.Rows
		err  error
	)
	if limit > 0 {
		rows, err = s.db.Query(query+` LIMIT ?`, time.Now().UTC().Format(time.RFC3339Nano), limit)
	} else {
		rows, err = s.db.Query(query, time.Now().UTC().Format(time.RFC3339Nano))
	}
	if err != nil {
		return nil, fmt.Errorf("query pending notifications: %w", err)
	}
	defer rows.Close()

	var records []DeliveryRecord
	for rows.Next() {
		rec, err := scanDeliveryRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending notifications: %w", err)
	}
	return records, nil
}

// Unread returns records that have not yet been surfaced to the agent.
func (s *Store) Unread(projectID, agentName string) ([]DeliveryRecord, error) {
	if projectID == "" || agentName == "" {
		return nil, fmt.Errorf("project_id and agent_name required")
	}

	rows, err := s.db.Query(`
		SELECT project_id, agent_name, message_id, from_name, body,
		       sent_at, received_at, notified_at, retry_attempts, retry_next_at, retry_exhausted_at, surfaced_at, dismissed_at
		FROM delivery_records
		WHERE project_id = ? AND agent_name = ? AND surfaced_at IS NULL AND dismissed_at IS NULL
		ORDER BY received_at ASC, message_id ASC
	`, projectID, agentName)
	if err != nil {
		return nil, fmt.Errorf("query unread records: %w", err)
	}
	defer rows.Close()

	var records []DeliveryRecord
	for rows.Next() {
		rec, err := scanDeliveryRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate unread records: %w", err)
	}
	return records, nil
}

func (s *Store) RecordNotificationFailure(projectID, agentName string, messageID int64, attempts int, nextRetryAt, exhaustedAt time.Time) error {
	if projectID == "" || agentName == "" {
		return fmt.Errorf("project_id and agent_name required")
	}
	if messageID == 0 {
		return fmt.Errorf("message_id required")
	}
	if attempts <= 0 {
		return fmt.Errorf("attempts must be positive")
	}
	if nextRetryAt.IsZero() == exhaustedAt.IsZero() {
		return fmt.Errorf("exactly one of nextRetryAt or exhaustedAt must be set")
	}

	result, err := s.db.Exec(`
		UPDATE delivery_records
		SET retry_attempts = ?, retry_next_at = ?, retry_exhausted_at = ?
		WHERE project_id = ? AND agent_name = ? AND message_id = ?
	`, attempts, textTimeValue(nextRetryAt), textTimeValue(exhaustedAt), projectID, agentName, messageID)
	if err != nil {
		return fmt.Errorf("recording notification failure: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("counting failed notification rows: %w", err)
	}
	if rowsAffected == 0 {
		return ErrRecordNotFound
	}
	return nil
}

// MarkSurfaced marks a delivery record as surfaced to the agent.
func (s *Store) MarkSurfaced(projectID, agentName string, messageID int64) error {
	if projectID == "" || agentName == "" {
		return fmt.Errorf("project_id and agent_name required")
	}
	if messageID == 0 {
		return fmt.Errorf("message_id required")
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.Exec(`
		UPDATE delivery_records
		SET surfaced_at = ?
		WHERE project_id = ? AND agent_name = ? AND message_id = ? AND surfaced_at IS NULL
	`, now, projectID, agentName, messageID)
	if err != nil {
		return fmt.Errorf("marking surfaced: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("counting surfaced rows: %w", err)
	}
	if rowsAffected > 0 {
		return nil
	}

	var exists int
	err = s.db.QueryRow(`
		SELECT 1 FROM delivery_records
		WHERE project_id = ? AND agent_name = ? AND message_id = ?
	`, projectID, agentName, messageID).Scan(&exists)
	if err == sql.ErrNoRows {
		return ErrRecordNotFound
	}
	if err != nil {
		return fmt.Errorf("checking surfaced record: %w", err)
	}
	return nil
}

func (s *Store) PruneDeliveryRecords(before time.Time) (int64, error) {
	result, err := s.db.Exec(`
		DELETE FROM delivery_records
		WHERE received_at < ?
		  AND (
			dismissed_at IS NOT NULL
			OR surfaced_at IS NOT NULL
			OR COALESCE(notified_at, '') != ''
		  )
	`, before.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("pruning delivery records: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting pruned delivery records: %w", err)
	}
	return rowsAffected, nil
}

func timeValue(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func normalizedWatch(w Watch, now time.Time) Watch {
	if w.Source == "explicit" {
		w.ExpiresAt = time.Time{}
		return w
	}
	if w.ExpiresAt.IsZero() {
		w.ExpiresAt = now.UTC().Add(config.Defaults.RuntimeEphemeralWatchTTL)
		return w
	}
	w.ExpiresAt = w.ExpiresAt.UTC()
	return w
}

func textTimeValue(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func scanDeliveryRecord(scanner interface {
	Scan(dest ...any) error
}) (DeliveryRecord, error) {
	var (
		rec                            DeliveryRecord
		sentAt, receivedAt, notifiedAt sql.NullString
		retryNextAt, retryExhaustedAt  sql.NullString
		surfacedAt, dismissedAt        sql.NullString
	)

	if err := scanner.Scan(
		&rec.ProjectID,
		&rec.AgentName,
		&rec.MessageID,
		&rec.FromName,
		&rec.Body,
		&sentAt,
		&receivedAt,
		&notifiedAt,
		&rec.RetryAttempts,
		&retryNextAt,
		&retryExhaustedAt,
		&surfacedAt,
		&dismissedAt,
	); err != nil {
		return DeliveryRecord{}, fmt.Errorf("scanning delivery record: %w", err)
	}

	rec.SentAt = parseTime(sentAt)
	rec.ReceivedAt = parseTime(receivedAt)
	rec.NotifiedAt = parseTime(notifiedAt)
	rec.RetryNextAt = parseTime(retryNextAt)
	rec.RetryExhaustedAt = parseTime(retryExhaustedAt)
	rec.SurfacedAt = parseTime(surfacedAt)
	rec.DismissedAt = parseTime(dismissedAt)
	return rec, nil
}

func parseTime(v sql.NullString) time.Time {
	if !v.Valid || v.String == "" {
		return time.Time{}
	}

	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if ts, err := time.Parse(layout, v.String); err == nil {
			return ts
		}
	}
	return time.Time{}
}
