package brokerstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
)

// WriteTx is minted only after a generation check on a checked-out connection.
// Domain functions cannot start, commit or roll back transactions themselves.
// Even a copied capability expires when the callback ends.
type WriteTx struct{ state *transactionState }

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

func (s *ownerState) transaction(ctx context.Context, change func(*WriteTx) error) error {
	ctx, cancel := context.WithTimeout(ctx, s.config.TransactionTimeout)
	defer cancel()
	return immediate(ctx, s.db, func(conn *sql.Conn) error {
		var matched int
		if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM broker_owner
			WHERE singleton = 1 AND instance_id = ? AND generation = ? AND released_at IS NULL`,
			s.identity, s.generation).Scan(&matched); err != nil {
			return fmt.Errorf("verify owner: %w", err)
		}
		if matched != 1 {
			return ErrFenced
		}
		tx := &WriteTx{state: &transactionState{conn: conn, ctx: ctx, active: true}}
		defer func() { tx.state.mu.Lock(); tx.state.active = false; tx.state.mu.Unlock() }()
		return change(tx)
	})
}

// immediate reserves one connection for BEGIN, all statements, and COMMIT.
// A failed rollback discards that connection instead of returning a possibly
// open transaction to the pool. It never retries the caller's operation.
func immediate(ctx context.Context, db *sql.DB, change func(*sql.Conn) error) (err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("reserve canonical connection: %w", err)
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin canonical transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			// Cancellation must not prevent rollback of an admitted transaction.
			if _, rollbackErr := conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK"); rollbackErr != nil {
				err = errors.Join(err, fmt.Errorf("rollback canonical transaction: %w", rollbackErr))
				// database/sql discards a connection on driver.ErrBadConn.
				err = errors.Join(err, discard(conn))
			}
		}
	}()
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
	if tx == nil || tx.state == nil {
		return ErrExpiredCapability
	}
	s := tx.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return ErrExpiredCapability
	}
	return s.conn.QueryRowContext(s.ctx, query, args...).Scan(dest...)
}

// Query consumes and closes rows before returning. visit may scan rows only;
// further domain queries run after this method has released its rows.
func (tx *WriteTx) Query(query string, args []any, visit func(*sql.Rows) error) (err error) {
	if tx == nil || tx.state == nil || visit == nil {
		return ErrExpiredCapability
	}
	s := tx.state
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
