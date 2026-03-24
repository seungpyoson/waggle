package tasks

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/seungpyoson/waggle/internal/config"
)

// State constants
const (
	StatePending   = "pending"
	StateClaimed   = "claimed"
	StateCompleted = "completed"
	StateFailed    = "failed"
	StateCanceled  = "canceled"
)

// Task represents a task in the store
type Task struct {
	ID              int64
	IdempotencyKey  string
	Type            string
	Tags            []string
	Payload         string
	Priority        int
	State           string
	Blocked         bool
	DependsOn       []int64
	ClaimToken      string
	ClaimedBy       string
	ClaimedAt       string
	LeaseExpiresAt  string
	LeaseDuration   int
	MaxRetries      int
	RetryCount      int
	Result          string
	FailureReason   string
	CreatedAt       string
	UpdatedAt       string
}

// CreateParams holds parameters for creating a task
type CreateParams struct {
	IdempotencyKey string
	Type           string
	Tags           []string
	Payload        string
	Priority       int
	DependsOn      []int64
	LeaseDuration  int
	MaxRetries     int
}

// ClaimFilter holds filters for claiming tasks
type ClaimFilter struct {
	Type string
	Tags []string
}

// ListFilter holds filters for listing tasks
type ListFilter struct {
	State string
	Type  string
	Owner string
}

// Store manages task persistence
type Store struct {
	db      *sql.DB
	claimMu sync.Mutex // Serializes claim operations
}

// NewStore creates a new task store
func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite serializes writers

	// Set pragmas
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("setting WAL mode: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("setting busy_timeout: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}

	return s, nil
}

// Close closes the database
func (s *Store) Close() error {
	return s.db.Close()
}

// migrate creates the schema
func (s *Store) migrate() error {
	// Check schema version
	var version int
	err := s.db.QueryRow("SELECT version FROM schema_version LIMIT 1").Scan(&version)
	if err == nil {
		// Schema exists, check version
		if version != 1 {
			return fmt.Errorf("unsupported schema version %d", version)
		}
		return nil
	}
	if err != sql.ErrNoRows {
		// Error other than "no rows" means table might not exist
		// Try to create it
	}

	// Create schema
	schema := `
	CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL);
	
	CREATE TABLE IF NOT EXISTS tasks (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		idempotency_key TEXT UNIQUE,
		type            TEXT,
		tags            TEXT,
		payload         TEXT NOT NULL,
		priority        INTEGER DEFAULT 0,
		state           TEXT NOT NULL DEFAULT 'pending',
		blocked         BOOLEAN DEFAULT FALSE,
		depends_on      TEXT,
		claim_token     TEXT,
		claimed_by      TEXT,
		claimed_at      TEXT,
		lease_expires_at TEXT,
		lease_duration  INTEGER DEFAULT 300,
		max_retries     INTEGER DEFAULT 3,
		retry_count     INTEGER DEFAULT 0,
		result          TEXT,
		failure_reason  TEXT,
		created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
		updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
	);

	CREATE INDEX IF NOT EXISTS idx_tasks_claimable ON tasks (state, blocked, priority DESC, created_at ASC);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_idempotency ON tasks (idempotency_key) WHERE idempotency_key IS NOT NULL;
	`

	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("creating schema: %w", err)
	}

	// Insert schema version if not exists
	var count int
	err = s.db.QueryRow("SELECT COUNT(*) FROM schema_version").Scan(&count)
	if err != nil {
		return fmt.Errorf("checking schema_version: %w", err)
	}
	if count == 0 {
		if _, err := s.db.Exec("INSERT INTO schema_version (version) VALUES (1)"); err != nil {
			return fmt.Errorf("inserting schema version: %w", err)
		}
	}

	return nil
}

// Create creates a new task
func (s *Store) Create(params CreateParams) (*Task, error) {
	// Check for existing task with same idempotency key
	if params.IdempotencyKey != "" {
		var existingID int64
		err := s.db.QueryRow("SELECT id FROM tasks WHERE idempotency_key = ?", params.IdempotencyKey).Scan(&existingID)
		if err == nil {
			return s.Get(existingID)
		}
		if err != sql.ErrNoRows {
			return nil, err
		}
	}

	// Set defaults
	leaseDuration := params.LeaseDuration
	if leaseDuration == 0 {
		leaseDuration = int(config.Defaults.LeaseDuration.Seconds())
	}
	maxRetries := params.MaxRetries
	if maxRetries == 0 {
		maxRetries = config.Defaults.MaxRetries
	}

	// Marshal tags and depends_on to JSON
	tagsJSON := "[]"
	if len(params.Tags) > 0 {
		b, err := json.Marshal(params.Tags)
		if err != nil {
			return nil, err
		}
		tagsJSON = string(b)
	}

	dependsOnJSON := "[]"
	blocked := false
	if len(params.DependsOn) > 0 {
		b, err := json.Marshal(params.DependsOn)
		if err != nil {
			return nil, err
		}
		dependsOnJSON = string(b)
		blocked = true // Tasks with dependencies start blocked
	}

	// Insert task
	var idempotencyKey interface{}
	if params.IdempotencyKey != "" {
		idempotencyKey = params.IdempotencyKey
	}

	result, err := s.db.Exec(`
		INSERT INTO tasks (idempotency_key, type, tags, payload, priority, blocked, depends_on, lease_duration, max_retries)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, idempotencyKey, params.Type, tagsJSON, params.Payload, params.Priority, blocked, dependsOnJSON, leaseDuration, maxRetries)
	if err != nil {
		return nil, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}

	return s.Get(id)
}

// Get retrieves a task by ID
func (s *Store) Get(id int64) (*Task, error) {
	row := s.db.QueryRow(`
		SELECT id, idempotency_key, type, tags, payload, priority, state, blocked, depends_on,
		       claim_token, claimed_by, claimed_at, lease_expires_at, lease_duration, max_retries,
		       retry_count, result, failure_reason, created_at, updated_at
		FROM tasks WHERE id = ?
	`, id)

	return scanTask(row)
}

// scanTask scans a task from a row
func scanTask(row *sql.Row) (*Task, error) {
	var t Task
	var idempotencyKey, taskType, tagsJSON, dependsOnJSON sql.NullString
	var claimToken, claimedBy, claimedAt, leaseExpiresAt sql.NullString
	var result, failureReason sql.NullString

	err := row.Scan(
		&t.ID, &idempotencyKey, &taskType, &tagsJSON, &t.Payload, &t.Priority, &t.State, &t.Blocked, &dependsOnJSON,
		&claimToken, &claimedBy, &claimedAt, &leaseExpiresAt, &t.LeaseDuration, &t.MaxRetries,
		&t.RetryCount, &result, &failureReason, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	// Handle nullable fields
	if idempotencyKey.Valid {
		t.IdempotencyKey = idempotencyKey.String
	}
	if taskType.Valid {
		t.Type = taskType.String
	}
	if claimToken.Valid {
		t.ClaimToken = claimToken.String
	}
	if claimedBy.Valid {
		t.ClaimedBy = claimedBy.String
	}
	if claimedAt.Valid {
		t.ClaimedAt = claimedAt.String
	}
	if leaseExpiresAt.Valid {
		t.LeaseExpiresAt = leaseExpiresAt.String
	}
	if result.Valid {
		t.Result = result.String
	}
	if failureReason.Valid {
		t.FailureReason = failureReason.String
	}

	// Unmarshal JSON arrays
	if tagsJSON.Valid && tagsJSON.String != "" {
		if err := json.Unmarshal([]byte(tagsJSON.String), &t.Tags); err != nil {
			return nil, fmt.Errorf("unmarshaling tags: %w", err)
		}
	}
	if dependsOnJSON.Valid && dependsOnJSON.String != "" {
		if err := json.Unmarshal([]byte(dependsOnJSON.String), &t.DependsOn); err != nil {
			return nil, fmt.Errorf("unmarshaling depends_on: %w", err)
		}
	}

	return &t, nil
}

// Claim atomically claims the next eligible task
func (s *Store) Claim(worker string, filter ClaimFilter) (*Task, error) {
	// Serialize claim operations to avoid transaction conflicts
	s.claimMu.Lock()
	defer s.claimMu.Unlock()

	// Generate claim token
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, err
	}
	claimToken := hex.EncodeToString(tokenBytes)

	// Calculate lease expiry
	leaseExpiresAt := time.Now().Add(config.Defaults.LeaseDuration).UTC().Format(time.RFC3339)

	// Use immediate transaction mode via pragma
	// SQLite's BEGIN IMMEDIATE must be the first statement in the transaction
	_, err := s.db.Exec("BEGIN IMMEDIATE")
	if err != nil {
		return nil, err
	}

	// Build query with optional type filter
	query := `
		SELECT id FROM tasks
		WHERE state = 'pending' AND blocked = 0
	`
	args := []interface{}{}
	if filter.Type != "" {
		query += " AND type = ?"
		args = append(args, filter.Type)
	}
	query += " ORDER BY priority DESC, created_at ASC LIMIT 1"

	var taskID int64
	err = s.db.QueryRow(query, args...).Scan(&taskID)
	if err == sql.ErrNoRows {
		s.db.Exec("ROLLBACK")
		return nil, fmt.Errorf("no eligible tasks")
	}
	if err != nil {
		s.db.Exec("ROLLBACK")
		return nil, err
	}

	// Update task to claimed
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.Exec(`
		UPDATE tasks
		SET state = 'claimed',
		    claim_token = ?,
		    claimed_by = ?,
		    claimed_at = ?,
		    lease_expires_at = ?,
		    updated_at = ?
		WHERE id = ?
	`, claimToken, worker, now, leaseExpiresAt, now, taskID)
	if err != nil {
		s.db.Exec("ROLLBACK")
		return nil, err
	}

	if _, err := s.db.Exec("COMMIT"); err != nil {
		s.db.Exec("ROLLBACK")
		return nil, err
	}

	return s.Get(taskID)
}

// Complete marks a task as completed
func (s *Store) Complete(id int64, claimToken, result string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`
		UPDATE tasks
		SET state = 'completed',
		    result = ?,
		    updated_at = ?
		WHERE id = ? AND claim_token = ? AND state = 'claimed'
	`, result, now, id, claimToken)
	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("invalid claim token or task not claimed")
	}

	return nil
}

// Fail marks a task as failed
func (s *Store) Fail(id int64, claimToken, reason string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`
		UPDATE tasks
		SET state = 'failed',
		    failure_reason = ?,
		    updated_at = ?
		WHERE id = ? AND claim_token = ? AND state = 'claimed'
	`, reason, now, id, claimToken)
	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("invalid claim token or task not claimed")
	}

	return nil
}

// Cancel marks a task as canceled
func (s *Store) Cancel(id int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`
		UPDATE tasks
		SET state = 'canceled',
		    updated_at = ?
		WHERE id = ? AND state IN ('pending', 'claimed')
	`, now, id)
	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("task not found or already completed/failed/canceled")
	}

	return nil
}

// Heartbeat extends the lease on a claimed task
func (s *Store) Heartbeat(id int64, claimToken string) error {
	leaseExpiresAt := time.Now().Add(config.Defaults.LeaseDuration).UTC().Format(time.RFC3339)
	now := time.Now().UTC().Format(time.RFC3339)

	res, err := s.db.Exec(`
		UPDATE tasks
		SET lease_expires_at = ?,
		    updated_at = ?
		WHERE id = ? AND claim_token = ? AND state = 'claimed'
	`, leaseExpiresAt, now, id, claimToken)
	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("invalid claim token or task not claimed")
	}

	return nil
}

// List returns tasks matching the filter
func (s *Store) List(filter ListFilter) ([]*Task, error) {
	query := `
		SELECT id, idempotency_key, type, tags, payload, priority, state, blocked, depends_on,
		       claim_token, claimed_by, claimed_at, lease_expires_at, lease_duration, max_retries,
		       retry_count, result, failure_reason, created_at, updated_at
		FROM tasks
		WHERE 1=1
	`
	args := []interface{}{}

	if filter.State != "" {
		query += " AND state = ?"
		args = append(args, filter.State)
	}
	if filter.Type != "" {
		query += " AND type = ?"
		args = append(args, filter.Type)
	}
	if filter.Owner != "" {
		query += " AND claimed_by = ?"
		args = append(args, filter.Owner)
	}

	query += " ORDER BY created_at ASC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		var t Task
		var idempotencyKey, taskType, tagsJSON, dependsOnJSON sql.NullString
		var claimToken, claimedBy, claimedAt, leaseExpiresAt sql.NullString
		var result, failureReason sql.NullString

		err := rows.Scan(
			&t.ID, &idempotencyKey, &taskType, &tagsJSON, &t.Payload, &t.Priority, &t.State, &t.Blocked, &dependsOnJSON,
			&claimToken, &claimedBy, &claimedAt, &leaseExpiresAt, &t.LeaseDuration, &t.MaxRetries,
			&t.RetryCount, &result, &failureReason, &t.CreatedAt, &t.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		// Handle nullable fields
		if idempotencyKey.Valid {
			t.IdempotencyKey = idempotencyKey.String
		}
		if taskType.Valid {
			t.Type = taskType.String
		}
		if claimToken.Valid {
			t.ClaimToken = claimToken.String
		}
		if claimedBy.Valid {
			t.ClaimedBy = claimedBy.String
		}
		if claimedAt.Valid {
			t.ClaimedAt = claimedAt.String
		}
		if leaseExpiresAt.Valid {
			t.LeaseExpiresAt = leaseExpiresAt.String
		}
		if result.Valid {
			t.Result = result.String
		}
		if failureReason.Valid {
			t.FailureReason = failureReason.String
		}

		// Unmarshal JSON arrays
		if tagsJSON.Valid && tagsJSON.String != "" {
			if err := json.Unmarshal([]byte(tagsJSON.String), &t.Tags); err != nil {
				return nil, fmt.Errorf("unmarshaling tags: %w", err)
			}
		}
		if dependsOnJSON.Valid && dependsOnJSON.String != "" {
			if err := json.Unmarshal([]byte(dependsOnJSON.String), &t.DependsOn); err != nil {
				return nil, fmt.Errorf("unmarshaling depends_on: %w", err)
			}
		}

		tasks = append(tasks, &t)
	}

	return tasks, rows.Err()
}

// RequeueAllClaimed requeues all claimed tasks (for crash recovery)
func (s *Store) RequeueAllClaimed() (int, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`
		UPDATE tasks
		SET state = 'pending',
		    claimed_by = NULL,
		    claim_token = NULL,
		    claimed_at = NULL,
		    lease_expires_at = NULL,
		    retry_count = retry_count + 1,
		    updated_at = ?
		WHERE state = 'claimed'
	`, now)
	if err != nil {
		return 0, err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}

	return int(rows), nil
}

// RequeueByOwner requeues all tasks claimed by a specific owner (for session cleanup)
func (s *Store) RequeueByOwner(owner string) (int, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`
		UPDATE tasks
		SET state = 'pending',
		    claimed_by = NULL,
		    claim_token = NULL,
		    claimed_at = NULL,
		    lease_expires_at = NULL,
		    retry_count = retry_count + 1,
		    updated_at = ?
		WHERE state = 'claimed' AND claimed_by = ?
	`, now, owner)
	if err != nil {
		return 0, err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}

	return int(rows), nil
}

// RequeueExpiredLeases finds expired leases and re-queues them or marks them as failed
func (s *Store) RequeueExpiredLeases() (int, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	totalCount := 0

	// First, mark tasks that have exceeded max retries as failed
	res1, err := s.db.Exec(`
		UPDATE tasks
		SET state = 'failed',
		    failure_reason = 'max_retries_exceeded',
		    retry_count = retry_count + 1,
		    claimed_by = NULL,
		    claim_token = NULL,
		    claimed_at = NULL,
		    lease_expires_at = NULL,
		    updated_at = ?
		WHERE state = 'claimed'
		  AND lease_expires_at < ?
		  AND retry_count + 1 >= max_retries
	`, now, now)
	if err != nil {
		return 0, err
	}

	rows1, err := res1.RowsAffected()
	if err != nil {
		return 0, err
	}
	totalCount += int(rows1)

	// Then, re-queue tasks that haven't exceeded max retries
	res2, err := s.db.Exec(`
		UPDATE tasks
		SET state = 'pending',
		    retry_count = retry_count + 1,
		    claimed_by = NULL,
		    claim_token = NULL,
		    claimed_at = NULL,
		    lease_expires_at = NULL,
		    updated_at = ?
		WHERE state = 'claimed'
		  AND lease_expires_at < ?
		  AND retry_count + 1 < max_retries
	`, now, now)
	if err != nil {
		return 0, err
	}

	rows2, err := res2.RowsAffected()
	if err != nil {
		return 0, err
	}
	totalCount += int(rows2)

	return totalCount, nil
}

