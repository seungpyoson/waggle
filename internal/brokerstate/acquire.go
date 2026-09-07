package brokerstate

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
	_ "modernc.org/sqlite"
)

const ownershipSchema = `
CREATE TABLE schema_version (version INTEGER NOT NULL);
CREATE TABLE broker_owner (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    instance_id TEXT NOT NULL CHECK (length(instance_id) > 0),
    generation INTEGER NOT NULL CHECK (generation > 0),
    boot_id TEXT NOT NULL CHECK (length(boot_id) > 0),
    pid INTEGER NOT NULL CHECK (pid > 0),
    process_start TEXT NOT NULL CHECK (length(process_start) > 0),
    acquired_at TEXT NOT NULL,
    released_at TEXT
);
CREATE TABLE cutover (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    state TEXT NOT NULL CHECK (state IN ('prepared', 'active'))
);
INSERT INTO cutover(singleton, state) VALUES (1, 'prepared');
`

// Acquire is the only owner constructor. CreateStore is explicit fresh-store
// initialization; OpenStore requires an existing native store and never migrates.
// Prepared stores permit owned maintenance, but cannot admit native service
// until RequireActive succeeds. Domain schema initialization and activation
// remain explicit owned transactions, never constructor side effects in stores.
func Acquire(ctx context.Context, cfg config.OwnershipConfig, inspector ProcessInspector) (_ *Owner, err error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if inspector == nil {
		return nil, ErrIdentityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.AcquireTimeout)
	defer cancel()
	self, err := inspector.Current(ctx)
	if err != nil {
		return nil, fmt.Errorf("identify current broker: %w", err)
	}
	if err := self.validate(); err != nil {
		return nil, err
	}
	if cfg.Action == config.CreateStore {
		f, err := os.OpenFile(cfg.Database, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return nil, fmt.Errorf("create new canonical store: %w", err)
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
	}
	info, err := os.Lstat(cfg.Database)
	if err != nil {
		return nil, fmt.Errorf("inspect canonical store: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("canonical store must be a regular file, not a symlink")
	}
	u := url.URL{Scheme: "file", Path: cfg.Database}
	q := u.Query()
	q.Set("mode", "rw")
	// Each opened connection uses a bounded SQLite busy wait. Acquisition and
	// transactions also carry their caller's context deadline.
	q.Set("_pragma", fmt.Sprintf("busy_timeout(%d)", cfg.TransactionTimeout.Milliseconds()))
	// Referential integrity is mandatory on every connection, including those
	// opened by database/sql after an earlier connection has been discarded.
	q.Add("_pragma", "foreign_keys(1)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open canonical store: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			err = errors.Join(err, db.Close())
		}
	}()
	db.SetMaxOpenConns(config.CanonicalConnections)
	// Journal initialization belongs to explicit creation. Opening existing
	// storage must never change its journal mode before ownership is acquired.
	if cfg.Action == config.CreateStore {
		if _, err := db.ExecContext(ctx, "PRAGMA journal_mode="+config.CanonicalJournalMode); err != nil {
			return nil, fmt.Errorf("initialize canonical journal: %w", err)
		}
	}
	instance := rand.Text()
	var generation int64
	var predecessor *ProcessIdentity
	err = reserved(ctx, db, writeAccess, func(conn *sql.Conn) error {
		var journal string
		if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
			return err
		}
		if journal != config.CanonicalJournalMode {
			return fmt.Errorf("%w: expected canonical journal mode %s, got %s", ErrSchemaVersion, config.CanonicalJournalMode, journal)
		}
		if cfg.Action == config.CreateStore {
			if _, err := conn.ExecContext(ctx, ownershipSchema); err != nil {
				return err
			}
			if _, err := conn.ExecContext(ctx, "INSERT INTO schema_version(version) VALUES (?)", config.NativeSchemaVersion); err != nil {
				return err
			}
			generation = 1
			_, err := conn.ExecContext(ctx, `INSERT INTO broker_owner
				(singleton, instance_id, generation, boot_id, pid, process_start, acquired_at)
				VALUES (1, ?, ?, ?, ?, ?, ?)`, instance, generation, self.BootID, self.PID, self.Start,
				time.Now().UTC().Format(time.RFC3339Nano))
			return err
		}
		var count, minVersion, maxVersion int
		if err := conn.QueryRowContext(ctx, `SELECT count(*), coalesce(min(version), 0), coalesce(max(version), 0) FROM schema_version`).Scan(&count, &minVersion, &maxVersion); err != nil {
			return fmt.Errorf("%w: %v", ErrSchemaVersion, err)
		}
		if count != 1 || minVersion != config.NativeSchemaVersion || maxVersion != config.NativeSchemaVersion {
			return ErrSchemaVersion
		}
		var oldID string
		var old ProcessIdentity
		var releasedAt sql.NullString
		if err := conn.QueryRowContext(ctx, `SELECT instance_id, generation, boot_id, pid, process_start, released_at
			FROM broker_owner WHERE singleton = 1`).Scan(&oldID, &generation, &old.BootID, &old.PID, &old.Start, &releasedAt); err != nil {
			return fmt.Errorf("invalid canonical ownership row: %w", err)
		}
		if err := old.validate(); err != nil {
			return err
		}
		predecessor = &old
		if oldID == "" || generation <= 0 || generation == math.MaxInt64 {
			return fmt.Errorf("invalid or exhausted canonical ownership generation")
		}
		if !releasedAt.Valid {
			status, err := inspector.Inspect(ctx, old)
			if err != nil {
				return fmt.Errorf("inspect predecessor: %w", err)
			}
			switch status {
			case ProcessExited:
			case ProcessAlive:
				return fmt.Errorf("%w: pid=%d generation=%d", ErrOwnerAlive, old.PID, generation)
			default:
				return ErrIdentityUnavailable
			}
		}
		previous := generation
		generation++
		res, err := conn.ExecContext(ctx, `UPDATE broker_owner SET instance_id = ?, generation = ?, boot_id = ?, pid = ?, process_start = ?,
			acquired_at = ?, released_at = NULL WHERE singleton = 1 AND instance_id = ? AND generation = ?`,
			instance, generation, self.BootID, self.PID, self.Start, time.Now().UTC().Format(time.RFC3339Nano), oldID, previous)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrFenced
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("acquire canonical store: %w", err)
	}
	keep = true
	interrupt, interruptCancel := context.WithCancelCause(context.Background())
	return &Owner{state: &ownerState{
		db: db, identity: instance, generation: generation, config: cfg, phase: serving,
		workers: make(map[string]struct{}),
		drained: make(chan struct{}), stop: make(chan struct{}), finalDone: make(chan struct{}), failed: make(chan struct{}),
		interrupt: interrupt, cancel: interruptCancel,
		predecessor: predecessor, inspector: inspector,
	}}, nil
}

// RequireActive belongs in the same transaction as enrollment or selection.
// Acquiring maintenance ownership of prepared storage never implies readiness.
func (tx *WriteTx) RequireActive() error {
	var state string
	if err := tx.Scan("SELECT state FROM cutover WHERE singleton = 1", nil, &state); err != nil {
		return err
	}
	if state != "active" {
		return ErrPrepared
	}
	return nil
}
