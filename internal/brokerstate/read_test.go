package brokerstate

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seungpyoson/waggle/internal/config"
)

func read(o *Owner, view func(*ReadTx) error) error {
	return o.Do(context.Background(), func(op *Operation) error {
		return op.Read(context.Background(), view)
	})
}

// TestReadSnapshotIsFencedReadOnlyAndExpires proves the read capability carries
// the same admission and lifetime as a write, that SQLite itself refuses a write
// through it, and that the restriction never outlives the snapshot.
func TestReadSnapshotIsFencedReadOnlyAndExpires(t *testing.T) {
	cfg := config.NewOwnershipConfig(filepath.Join(t.TempDir(), "state.db"), config.CreateStore)
	stale, err := Acquire(t.Context(), cfg, processFixture{status: ProcessAlive})
	if err != nil {
		t.Fatal(err)
	}
	defer stale.state.db.Close() // test-only stale-handle cleanup; it cannot release a successor's row
	successor, err := sql.Open("sqlite", cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer successor.Close()
	if _, err := successor.Exec("UPDATE broker_owner SET generation = generation + 1, instance_id = 'successor' WHERE singleton = 1"); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := read(stale, func(*ReadTx) error { called = true; return nil }); !errors.Is(err, ErrFenced) || called {
		t.Fatalf("stale read = %v, callback ran = %v", err, called)
	}
	if got := stale.Stats(); got.Reads != 0 {
		t.Fatalf("fenced read was counted as admitted: %+v", got)
	}
	stale.BeginShutdown(nil)
	if err := stale.Wait(t.Context()); !errors.Is(err, ErrFenced) {
		t.Fatalf("stale release = %v", err)
	}

	o, _ := newOwner(t)
	if err := write(o, func(tx *WriteTx) error { _, err := tx.Exec("CREATE TABLE read_probe (value TEXT)"); return err }); err != nil {
		t.Fatal(err)
	}
	var escaped *ReadTx
	var insertErr error
	var seen int
	if err := read(o, func(tx *ReadTx) error {
		escaped = tx
		if err := tx.Scan("SELECT count(*) FROM read_probe", nil, &seen); err != nil {
			return err
		}
		insertErr = tx.Query("INSERT INTO read_probe VALUES ('snapshot')", nil, func(*sql.Rows) error { return nil })
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if seen != 0 {
		t.Fatalf("snapshot read %d rows before any insert", seen)
	}
	if insertErr == nil || !strings.Contains(strings.ToLower(insertErr.Error()), "readonly") {
		t.Fatalf("snapshot insert = %v, want a SQLite readonly refusal", insertErr)
	}
	if err := escaped.Scan("SELECT count(*) FROM read_probe", nil, &seen); !errors.Is(err, ErrExpiredCapability) {
		t.Fatalf("escaped read capability = %v", err)
	}
	// A write on the same owner must still succeed: the pooled connection cannot
	// carry the snapshot's read-only restriction into it.
	var rows int
	if err := write(o, func(tx *WriteTx) error {
		if _, err := tx.Exec("INSERT INTO read_probe VALUES ('write after read')"); err != nil {
			return err
		}
		return tx.Scan("SELECT count(*) FROM read_probe", nil, &rows)
	}); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("read_probe holds %d rows; the snapshot wrote one of its own", rows)
	}
	if got := (Stats{Writes: 2, Reads: 1}); o.Stats() != got {
		t.Fatalf("Stats() = %+v, want %+v", o.Stats(), got)
	}
}
