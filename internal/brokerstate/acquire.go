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

// schemaVersionDDL creates the single shared version record. A legacy store
// already has this table, so conversion updates the row it finds instead.
const schemaVersionDDL = `
CREATE TABLE schema_version (version INTEGER NOT NULL);
`

// ownershipTables is the one definition of native ownership storage, executed
// by fresh creation and by offline conversion alike. The cutover row is created
// prepared with empty provenance; conversion records where the store came from.
const ownershipTables = `
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
    state TEXT NOT NULL CHECK (state IN ('prepared', 'active')),
    source_schema INTEGER,
    converted_at TEXT,
    snapshot TEXT
);
INSERT INTO cutover(singleton, state, source_schema, converted_at, snapshot) VALUES (1, 'prepared', NULL, NULL, NULL);
CREATE TABLE endpoint_binding (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    instance_id TEXT NOT NULL,
    generation INTEGER NOT NULL,
    boot_id TEXT NOT NULL,
    pid INTEGER NOT NULL,
    process_start TEXT NOT NULL,
    bound_at TEXT NOT NULL
);
`

// firstGeneration is the ownership generation of the process that brings a
// canonical store into existence, by creation or by conversion.
const firstGeneration = 1

// openCanonical is the only place the canonical database is opened, by the
// owner, by offline conversion and by read-only inspection. Access is "rw" for
// an existing store, "rwc" for explicit creation and "ro" for inspection, which
// never creates a file. Every connection carries a bounded busy wait; callers
// add their own context deadline.
func openCanonical(path, access string, busy time.Duration) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", access)
	// Each opened connection uses a bounded SQLite busy wait. Acquisition and
	// transactions also carry their caller's context deadline.
	q.Set("_pragma", fmt.Sprintf("busy_timeout(%d)", busy.Milliseconds()))
	// Referential integrity is mandatory on every connection, including those
	// opened by database/sql after an earlier connection has been discarded.
	q.Add("_pragma", "foreign_keys(1)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open canonical store: %w", err)
	}
	db.SetMaxOpenConns(config.CanonicalConnections)
	return db, nil
}

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
	db, err := openCanonical(cfg.Database, "rw", cfg.TransactionTimeout)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			err = errors.Join(err, db.Close())
		}
	}()
	// Journal initialization belongs to explicit creation. Opening existing
	// storage must never change its journal mode before ownership is acquired.
	if cfg.Action == config.CreateStore {
		if err := ensureWALJournal(ctx, db); err != nil {
			return nil, err
		}
	}
	instance := rand.Text()
	var generation int64
	err = reserved(ctx, db, writeAccess, func(conn *sql.Conn) error {
		var journal string
		if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
			return err
		}
		if cfg.Action == config.CreateStore {
			if _, err := conn.ExecContext(ctx, schemaVersionDDL+ownershipTables); err != nil {
				return err
			}
			if _, err := conn.ExecContext(ctx, "INSERT INTO schema_version(version) VALUES (?)", config.NativeSchemaVersion); err != nil {
				return err
			}
			generation = firstGeneration
			return claimOwnership(ctx, conn, instance, self, sql.NullString{})
		}
		var count, minVersion, maxVersion int
		if err := conn.QueryRowContext(ctx, `SELECT count(*), coalesce(min(version), 0), coalesce(max(version), 0) FROM schema_version`).Scan(&count, &minVersion, &maxVersion); err != nil {
			return fmt.Errorf("%w: %v", ErrSchemaVersion, err)
		}
		if count != 1 || minVersion != config.NativeSchemaVersion || maxVersion != config.NativeSchemaVersion {
			return ErrSchemaVersion
		}
		if journal != config.CanonicalJournalMode {
			var state string
			if err := conn.QueryRowContext(ctx, "SELECT state FROM cutover WHERE singleton = 1").Scan(&state); err != nil {
				return fmt.Errorf("read cutover for pending journal switch: %w", err)
			}
			// Prepared native storage permits maintenance ownership so Activate
			// can finish an interrupted conversion. RequireActive still refuses
			// service; no journal change happens before ownership is acquired.
			if state != "prepared" {
				return fmt.Errorf("expected canonical journal mode %s for %s store, got %s", config.CanonicalJournalMode, state, journal)
			}
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
		drained: make(chan struct{}), idle: make(chan struct{}), stop: make(chan struct{}), finalDone: make(chan struct{}), failed: make(chan struct{}),
		interrupt: interrupt, cancel: interruptCancel,
		inspector: inspector,
	}}, nil
}

// claimOwnership records the first incarnation of a canonical store. Fresh
// creation records itself as the live owner. Offline conversion records the
// converting process already released: its exclusive access was proven by an OS
// census and ends with the conversion, so the next broker succeeds it normally.
func claimOwnership(ctx context.Context, conn *sql.Conn, instance string, self ProcessIdentity, released sql.NullString) error {
	_, err := conn.ExecContext(ctx, `INSERT INTO broker_owner
		(singleton, instance_id, generation, boot_id, pid, process_start, acquired_at, released_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?)`, instance, firstGeneration, self.BootID, self.PID, self.Start,
		time.Now().UTC().Format(time.RFC3339Nano), released)
	return err
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

// Activate is the only prepared → active transition. Fresh initialization runs
// it in the same transaction that creates the domain schema; `waggle store
// activate` runs it alone through Owner.Activate. Reaching the active state is
// success, so activating an already active store is not an error.
func (tx *WriteTx) Activate() error {
	res, err := tx.Exec("UPDATE cutover SET state = 'active' WHERE singleton = 1 AND state = 'prepared'")
	if err != nil {
		return err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 1 {
		return nil
	}
	if err := tx.RequireActive(); err != nil {
		return fmt.Errorf("%w: %w", ErrNotPrepared, err)
	}
	return nil
}

// Activation reports the transition and any legacy endpoints retired while
// finishing a conversion's post-commit work.
type Activation struct {
	AlreadyActive    bool
	RetiredEndpoints []string
}

// Activate finishes the post-commit tail before the owned transition. The
// journal switch cannot run inside a transaction, so it reserves a connection
// and checks the fence first. Admission keeps ownership held until both the
// switch and the fenced retirement/transition transaction finish.
func (o *Owner) Activate(ctx context.Context, cfg ConversionConfig) (result Activation, err error) {
	if err := cfg.Validate(); err != nil {
		return Activation{}, err
	}
	err = o.Do(ctx, func(op *Operation) error {
		s := op.state.owner
		if cfg.Database != s.config.Database {
			return fmt.Errorf("activation paths name a different store from the acquired owner")
		}
		ctx, cancel := context.WithTimeout(ctx, s.config.TransactionTimeout)
		defer cancel()
		conn, err := s.db.Conn(ctx)
		if err != nil {
			return fmt.Errorf("reserve activation connection: %w", err)
		}
		err = s.fence(ctx, conn)
		if err == nil {
			err = ensureWALJournal(ctx, conn)
		}
		if err = errors.Join(err, conn.Close()); err != nil {
			return err
		}
		return op.Write(ctx, func(tx *WriteTx) error {
			if err := tx.RequireActive(); err == nil {
				result.AlreadyActive = true
			} else if !errors.Is(err, ErrPrepared) {
				return err
			}
			var err error
			result.RetiredEndpoints, err = retireLegacyEndpoints(cfg)
			if err != nil {
				return err
			}
			return tx.Activate()
		})
	})
	return result, err
}
