package brokerstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
)

// Reader is the read-only capability shared by WriteTx and ReadTx.
type Reader interface {
	Scan(query string, args []any, dest ...any) error
	Query(query string, args []any, visit func(*sql.Rows) error) error
}

var (
	_ Reader = (*WriteTx)(nil)
	_ Reader = (*ReadTx)(nil)
)

// WriteTx is minted only after a generation check on a checked-out connection.
// Domain functions cannot start, commit or roll back transactions themselves.
// Even a copied capability expires when the callback ends.
type WriteTx struct{ state *transactionState }

// ReadTx is a fenced read snapshot. It cannot execute statements.
type ReadTx struct{ state *transactionState }

type transactionState struct {
	mu     sync.Mutex
	conn   *sql.Conn
	ctx    context.Context
	active bool
}

func (op *Operation) Write(ctx context.Context, change func(*WriteTx) error) error {
	if op == nil || op.state == nil || change == nil {
		return ErrExpiredCapability
	}
	s := op.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return ErrExpiredCapability
	}
	return s.owner.transaction(ctx, change)
}

// Read runs view inside a deferred read transaction on the reserved connection
// with PRAGMA query_only=1 set for its duration, after the same generation fence
// check Write performs. It never takes the write lock. The capability expires
// when view returns. Read and Write must never be nested: the canonical store
// reserves a single connection.
func (op *Operation) Read(ctx context.Context, view func(*ReadTx) error) error {
	if op == nil || op.state == nil || view == nil {
		return ErrExpiredCapability
	}
	s := op.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return ErrExpiredCapability
	}
	return s.owner.snapshot(ctx, view)
}

func (s *ownerState) transaction(ctx context.Context, change func(*WriteTx) error) error {
	ctx, cancel := context.WithTimeout(ctx, s.config.TransactionTimeout)
	defer cancel()
	return reserved(ctx, s.db, writeAccess, func(conn *sql.Conn) error {
		if err := s.fence(ctx, conn); err != nil {
			return err
		}
		s.writes.Add(1)
		tx := &WriteTx{state: &transactionState{conn: conn, ctx: ctx, active: true}}
		defer func() { tx.state.mu.Lock(); tx.state.active = false; tx.state.mu.Unlock() }()
		return change(tx)
	})
}

func (s *ownerState) snapshot(ctx context.Context, view func(*ReadTx) error) error {
	ctx, cancel := context.WithTimeout(ctx, s.config.TransactionTimeout)
	defer cancel()
	return reserved(ctx, s.db, readAccess, func(conn *sql.Conn) error {
		if err := s.fence(ctx, conn); err != nil {
			return err
		}
		s.reads.Add(1)
		tx := &ReadTx{state: &transactionState{conn: conn, ctx: ctx, active: true}}
		defer func() { tx.state.mu.Lock(); tx.state.active = false; tx.state.mu.Unlock() }()
		return view(tx)
	})
}

// fence proves this incarnation still holds the current generation. Every
// admitted transaction, read or write, passes it before any domain statement.
func (s *ownerState) fence(ctx context.Context, conn *sql.Conn) error {
	var matched int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM broker_owner
		WHERE singleton = 1 AND instance_id = ? AND generation = ? AND released_at IS NULL`,
		s.identity, s.generation).Scan(&matched); err != nil {
		return fmt.Errorf("verify owner: %w", err)
	}
	if matched != 1 {
		return ErrFenced
	}
	return nil
}

// access is the statements that open and release one reserved connection. A
// deferred BEGIN on WAL is a read snapshot that takes no write lock; restrict
// makes SQLite itself, not convention, refuse every change attempted through
// it, and release lifts that restriction before the connection is pooled again.
type access struct {
	begin    string
	restrict string
	release  string
}

var (
	writeAccess = access{begin: "BEGIN IMMEDIATE"}
	readAccess  = access{begin: "BEGIN", restrict: "PRAGMA query_only=1", release: "PRAGMA query_only=0"}
)

// reserved reserves one connection for BEGIN, all statements, and COMMIT.
//
// Every undo is registered before the statement it undoes, and runs whether or
// not that statement reported success: a driver returns an error for a
// statement it has already applied whenever the context finishes after the step
// but before the call returns. The canonical store owns exactly one connection,
// so an open transaction or a surviving read restriction on it would fail every
// later transaction, ownership release included. An undo that itself fails
// discards the connection instead of pooling one whose state is unknown, and
// nothing is retried on the caller's behalf.
func reserved(ctx context.Context, db *sql.DB, mode access, change func(*sql.Conn) error) (err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("reserve canonical connection: %w", err)
	}
	// discarded is set by whichever undo removed the connection from the pool.
	// The undos after it must not run on a closed connection: they would report
	// only their own ErrConnDone and bury the failure that actually happened.
	discarded := false
	defer func() {
		if discarded {
			return
		}
		err = errors.Join(err, conn.Close())
	}()
	if mode.restrict != "" {
		defer func() {
			// The restriction belongs to this snapshot, not to the pooled
			// connection. Lifting one that was never applied is a no-op, and
			// cancellation must not prevent the removal of one that was.
			if discarded {
				return
			}
			if _, resetErr := conn.ExecContext(context.WithoutCancel(ctx), mode.release); resetErr != nil {
				// database/sql discards a connection on driver.ErrBadConn.
				err = errors.Join(err, fmt.Errorf("restore canonical connection writes: %w", resetErr), discard(conn))
				discarded = true
			}
		}()
		if _, err = conn.ExecContext(ctx, mode.restrict); err != nil {
			return fmt.Errorf("restrict canonical connection to reads: %w", err)
		}
	}
	committed := false
	defer func() {
		// Rolling back is what makes an applied-but-failed BEGIN safe, so it
		// cannot be conditional on BEGIN having reported success. When no
		// transaction was opened the rollback fails, and discarding the
		// connection is the correct outcome: it leaves nothing ambiguous in
		// the pool.
		if committed || discarded {
			return
		}
		if _, rollbackErr := conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK"); rollbackErr != nil {
			err = errors.Join(err, fmt.Errorf("rollback canonical transaction: %w", rollbackErr), discard(conn))
			discarded = true
		}
	}()
	if _, err = conn.ExecContext(ctx, mode.begin); err != nil {
		return fmt.Errorf("begin canonical transaction: %w", err)
	}
	if err = change(conn); err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit canonical transaction: %w", err)
	}
	committed = true
	return nil
}

// Exec executes a statement while this transaction capability is valid.
func (tx *WriteTx) Exec(query string, args ...any) (sql.Result, error) {
	if tx == nil || tx.state == nil {
		return nil, ErrExpiredCapability
	}
	s := tx.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return nil, ErrExpiredCapability
	}
	return s.conn.ExecContext(s.ctx, query, args...)
}

// Scan consumes a single row inside the capability's lifetime. No raw connection
// or lazy sql.Row is returned for callers to use after commit.
func (tx *WriteTx) Scan(query string, args []any, dest ...any) error {
	if tx == nil {
		return ErrExpiredCapability
	}
	return tx.state.scan(query, args, dest...)
}

// Query consumes and closes rows before returning. visit may scan rows only;
// further domain queries run after this method has released its rows.
func (tx *WriteTx) Query(query string, args []any, visit func(*sql.Rows) error) error {
	if tx == nil {
		return ErrExpiredCapability
	}
	return tx.state.query(query, args, visit)
}

// Scan consumes a single row inside the snapshot's lifetime.
func (tx *ReadTx) Scan(query string, args []any, dest ...any) error {
	if tx == nil {
		return ErrExpiredCapability
	}
	return tx.state.scan(query, args, dest...)
}

// Query consumes and closes rows before returning, exactly as the write
// capability does. SQLite refuses any statement that would change the store.
func (tx *ReadTx) Query(query string, args []any, visit func(*sql.Rows) error) error {
	if tx == nil {
		return ErrExpiredCapability
	}
	return tx.state.query(query, args, visit)
}

func (s *transactionState) scan(query string, args []any, dest ...any) error {
	if s == nil {
		return ErrExpiredCapability
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return ErrExpiredCapability
	}
	return s.conn.QueryRowContext(s.ctx, query, args...).Scan(dest...)
}

func (s *transactionState) query(query string, args []any, visit func(*sql.Rows) error) (err error) {
	if s == nil || visit == nil {
		return ErrExpiredCapability
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return ErrExpiredCapability
	}
	rows, err := s.conn.QueryContext(s.ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	return errors.Join(visit(rows), rows.Err())
}
