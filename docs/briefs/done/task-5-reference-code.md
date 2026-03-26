# Task 5: Reference Code — SQLite Migration + Claim Transaction

This is reference code for the two hardest parts of Task 5. The implementer should use this as the starting point, not write from scratch.

## SQLite Open + Migration

```go
package tasks

import (
    "crypto/rand"
    "database/sql"
    "encoding/json"
    "fmt"
    "time"

    _ "modernc.org/sqlite"
)

const (
    StatePending   = "pending"
    StateClaimed   = "claimed"
    StateCompleted = "completed"
    StateFailed    = "failed"
    StateCanceled  = "canceled"

    schemaVersion = 1
)

type Store struct {
    db *sql.DB
}

func NewStore(dbPath string) (*Store, error) {
    db, err := sql.Open("sqlite", dbPath)
    if err != nil {
        return nil, fmt.Errorf("open db: %w", err)
    }

    // CRITICAL: SQLite serializes writers. Multiple connections = "database is locked".
    db.SetMaxOpenConns(1)

    // WAL mode for concurrent reads
    if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
        db.Close()
        return nil, fmt.Errorf("set WAL: %w", err)
    }

    // Busy timeout: without this, concurrent reads block writes instantly
    if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
        db.Close()
        return nil, fmt.Errorf("set busy_timeout: %w", err)
    }

    if err := migrate(db); err != nil {
        db.Close()
        return nil, fmt.Errorf("migrate: %w", err)
    }

    return &Store{db: db}, nil
}

func (s *Store) Close() error {
    return s.db.Close()
}

func migrate(db *sql.DB) error {
    // Check if schema_version table exists
    var count int
    err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_version'`).Scan(&count)
    if err != nil {
        return fmt.Errorf("check schema_version: %w", err)
    }

    if count == 0 {
        // First time: create everything
        if _, err := db.Exec(`
            CREATE TABLE schema_version (
                version INTEGER NOT NULL
            );
            INSERT INTO schema_version VALUES (?);
        `, schemaVersion); err != nil {
            return fmt.Errorf("create schema_version: %w", err)
        }

        if _, err := db.Exec(`
            CREATE TABLE tasks (
                id              INTEGER PRIMARY KEY AUTOINCREMENT,
                idempotency_key TEXT,
                type            TEXT,
                tags            TEXT,
                payload         TEXT NOT NULL,
                priority        INTEGER NOT NULL DEFAULT 0,
                state           TEXT NOT NULL DEFAULT 'pending',
                blocked         INTEGER NOT NULL DEFAULT 0,
                depends_on      TEXT,
                claim_token     TEXT,
                claimed_by      TEXT,
                claimed_at      TEXT,
                lease_expires_at TEXT,
                lease_duration  INTEGER NOT NULL DEFAULT 300,
                max_retries     INTEGER NOT NULL DEFAULT 3,
                retry_count     INTEGER NOT NULL DEFAULT 0,
                result          TEXT,
                failure_reason  TEXT,
                created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
                updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
            );

            CREATE INDEX idx_tasks_claimable
                ON tasks (state, blocked, priority DESC, created_at ASC);

            CREATE UNIQUE INDEX idx_tasks_idempotency
                ON tasks (idempotency_key) WHERE idempotency_key IS NOT NULL;
        `); err != nil {
            return fmt.Errorf("create tasks table: %w", err)
        }

        return nil
    }

    // Existing DB: check version
    var version int
    if err := db.QueryRow("SELECT version FROM schema_version").Scan(&version); err != nil {
        return fmt.Errorf("read schema version: %w", err)
    }
    if version != schemaVersion {
        return fmt.Errorf("schema version mismatch: got %d, want %d", version, schemaVersion)
    }

    return nil
}
```

## Claim Transaction (the hardest part)

This must be atomic — exactly one goroutine wins even under contention.

```go
func (s *Store) Claim(worker string, filter ClaimFilter) (*Task, error) {
    // BEGIN IMMEDIATE acquires a write lock immediately, preventing two
    // concurrent claims from both reading the same eligible task.
    tx, err := s.db.Begin()
    if err != nil {
        return nil, fmt.Errorf("begin tx: %w", err)
    }
    defer tx.Rollback()

    // SQLite with single connection means BEGIN is effectively IMMEDIATE,
    // but being explicit is safer if MaxOpenConns ever changes.
    if _, err := tx.Exec("BEGIN IMMEDIATE"); err != nil {
        // If we're already in a transaction (from db.Begin), this may fail.
        // That's OK — db.Begin() already acquired the lock.
    }

    // Build query with optional filters
    query := `SELECT id FROM tasks WHERE state = ? AND blocked = 0`
    args := []any{StatePending}

    if filter.Type != "" {
        query += " AND type = ?"
        args = append(args, filter.Type)
    }
    if len(filter.Tags) > 0 {
        // Match tasks that have ALL specified tags
        // Tags stored as JSON array, use json_each to check
        for _, tag := range filter.Tags {
            query += " AND EXISTS (SELECT 1 FROM json_each(tags) WHERE value = ?)"
            args = append(args, tag)
        }
    }

    query += " ORDER BY priority DESC, created_at ASC LIMIT 1"

    var taskID int64
    if err := tx.QueryRow(query, args...).Scan(&taskID); err != nil {
        if err == sql.ErrNoRows {
            return nil, fmt.Errorf("no eligible task")
        }
        return nil, fmt.Errorf("select eligible: %w", err)
    }

    // Generate claim token
    token, err := generateToken()
    if err != nil {
        return nil, fmt.Errorf("generate token: %w", err)
    }

    // Read lease duration from the task (may be custom per-task)
    var leaseDuration int
    if err := tx.QueryRow("SELECT lease_duration FROM tasks WHERE id = ?", taskID).Scan(&leaseDuration); err != nil {
        return nil, fmt.Errorf("read lease duration: %w", err)
    }

    ts := now()
    leaseExpires := time.Now().UTC().Add(time.Duration(leaseDuration) * time.Second).Format(time.RFC3339)

    _, err = tx.Exec(`
        UPDATE tasks SET
            state = ?, claim_token = ?, claimed_by = ?, claimed_at = ?,
            lease_expires_at = ?, updated_at = ?
        WHERE id = ?`,
        StateClaimed, token, worker, ts, leaseExpires, ts, taskID,
    )
    if err != nil {
        return nil, fmt.Errorf("update claim: %w", err)
    }

    if err := tx.Commit(); err != nil {
        return nil, fmt.Errorf("commit claim: %w", err)
    }

    return s.Get(taskID)
}

func generateToken() (string, error) {
    b := make([]byte, 16)
    if _, err := rand.Read(b); err != nil {
        return "", err
    }
    return fmt.Sprintf("%x", b), nil
}

func now() string {
    return time.Now().UTC().Format(time.RFC3339)
}
```

## RequeueByWorker (for unclean disconnect)

```go
// RequeueByWorker re-queues all tasks claimed by a specific worker.
// Called on unclean disconnect (socket dropped without disconnect command).
func (s *Store) RequeueByWorker(worker string) (int, error) {
    ts := now()
    result, err := s.db.Exec(`
        UPDATE tasks SET
            state = ?, blocked = 0,
            claim_token = NULL, claimed_by = NULL, claimed_at = NULL,
            lease_expires_at = NULL, retry_count = retry_count + 1, updated_at = ?
        WHERE claimed_by = ? AND state = ?`,
        StatePending, ts, worker, StateClaimed,
    )
    if err != nil {
        return 0, fmt.Errorf("requeue by worker: %w", err)
    }
    n, _ := result.RowsAffected()
    return int(n), nil
}

// RequeueAllClaimed re-queues ALL claimed tasks. Called on broker startup (crash recovery).
func (s *Store) RequeueAllClaimed() (int, error) {
    ts := now()
    result, err := s.db.Exec(`
        UPDATE tasks SET
            state = ?, blocked = 0,
            claim_token = NULL, claimed_by = NULL, claimed_at = NULL,
            lease_expires_at = NULL, retry_count = retry_count + 1, updated_at = ?
        WHERE state = ?`,
        StatePending, ts, StateClaimed,
    )
    if err != nil {
        return 0, fmt.Errorf("requeue all claimed: %w", err)
    }
    n, _ := result.RowsAffected()
    return int(n), nil
}
```

## Concurrent Claim Test (must pass with -race)

```go
func TestStore_ClaimConcurrent(t *testing.T) {
    s := newTestStore(t)

    // Create one task
    _, err := s.Create(CreateParams{Payload: `{"concurrent": true}`})
    if err != nil {
        t.Fatal(err)
    }

    // 10 goroutines race to claim it
    var wins int32
    var wg sync.WaitGroup
    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            _, err := s.Claim(fmt.Sprintf("worker-%d", id), ClaimFilter{})
            if err == nil {
                atomic.AddInt32(&wins, 1)
            }
        }(i)
    }
    wg.Wait()

    if wins != 1 {
        t.Fatalf("expected exactly 1 winner, got %d", wins)
    }

    // Verify the task is claimed
    tasks, _ := s.List(ListFilter{State: StateClaimed})
    if len(tasks) != 1 {
        t.Fatalf("expected 1 claimed task, got %d", len(tasks))
    }
}
```

## Complete, Fail, Cancel (straightforward but token-checked)

```go
func (s *Store) Complete(id int64, token, result string) error {
    res, err := s.db.Exec(`
        UPDATE tasks SET state = ?, result = ?, updated_at = ?
        WHERE id = ? AND state = ? AND claim_token = ?`,
        StateCompleted, result, now(), id, StateClaimed, token,
    )
    if err != nil {
        return fmt.Errorf("complete task %d: %w", id, err)
    }
    n, _ := res.RowsAffected()
    if n == 0 {
        return fmt.Errorf("task %d: not claimed or invalid token", id)
    }
    return nil
}

func (s *Store) Fail(id int64, token, reason string) error {
    res, err := s.db.Exec(`
        UPDATE tasks SET state = ?, failure_reason = ?, updated_at = ?
        WHERE id = ? AND state = ? AND claim_token = ?`,
        StateFailed, reason, now(), id, StateClaimed, token,
    )
    if err != nil {
        return fmt.Errorf("fail task %d: %w", id, err)
    }
    n, _ := res.RowsAffected()
    if n == 0 {
        return fmt.Errorf("task %d: not claimed or invalid token", id)
    }
    return nil
}

func (s *Store) Cancel(id int64) error {
    res, err := s.db.Exec(`
        UPDATE tasks SET state = ?, updated_at = ?
        WHERE id = ? AND state IN (?, ?)`,
        StateCanceled, now(), id, StatePending, StateClaimed,
    )
    if err != nil {
        return fmt.Errorf("cancel task %d: %w", id, err)
    }
    n, _ := res.RowsAffected()
    if n == 0 {
        return fmt.Errorf("task %d: not in cancelable state", id)
    }
    return nil
}

func (s *Store) Heartbeat(id int64, token string) error {
    var leaseDur int
    err := s.db.QueryRow(
        "SELECT lease_duration FROM tasks WHERE id = ? AND state = ? AND claim_token = ?",
        id, StateClaimed, token,
    ).Scan(&leaseDur)
    if err != nil {
        return fmt.Errorf("task %d: not claimed or invalid token", id)
    }

    newExpiry := time.Now().UTC().Add(time.Duration(leaseDur) * time.Second).Format(time.RFC3339)
    _, err = s.db.Exec(
        "UPDATE tasks SET lease_expires_at = ?, updated_at = ? WHERE id = ?",
        newExpiry, now(), id,
    )
    return err
}
```

## Notes for implementer

- The `db.Begin()` in Go's database/sql already starts a transaction. The explicit `BEGIN IMMEDIATE` inside it is belt-and-suspenders. With `MaxOpenConns(1)`, there's only one connection, so all transactions are serialized. But keep the pattern — it documents intent.
- Tags filter uses `json_each` with `EXISTS` subquery — this correctly matches tasks that have ALL specified tags.
- `generateToken` uses `crypto/rand` not `math/rand` — tokens are security-relevant (they prove claim ownership).
- All timestamp fields use RFC3339 UTC. The `now()` helper ensures consistency.
- `RowsAffected() == 0` is the correct way to detect "wrong token" or "wrong state" — it means the WHERE clause didn't match.
