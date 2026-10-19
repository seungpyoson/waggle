// Package statetest constructs real owned SQLite stores for domain and broker
// tests. Only OS process observation is deterministic; ownership is not bypassed.
package statetest

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/config"
)

type Process struct{}

func (Process) Current(context.Context) (brokerstate.ProcessIdentity, error) {
	return brokerstate.ProcessIdentity{BootID: "fixture-boot", PID: os.Getpid(), Start: "fixture-start"}, nil
}
func (Process) Inspect(context.Context, brokerstate.ProcessIdentity) (brokerstate.ProcessStatus, error) {
	return brokerstate.ProcessAlive, nil
}

func New(t *testing.T, schema string) *brokerstate.Owner {
	t.Helper()
	o, err := brokerstate.Acquire(t.Context(), config.NewOwnershipConfig(filepath.Join(t.TempDir(), "state.db"), config.CreateStore), Process{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		o.BeginShutdown(nil)
		if err := o.Wait(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if err := Write(o, func(tx *brokerstate.WriteTx) error { _, err := tx.Exec(schema); return err }); err != nil {
		t.Fatal(err)
	}
	return o
}

func Write(o *brokerstate.Owner, change func(*brokerstate.WriteTx) error) error {
	return o.Do(context.Background(), func(op *brokerstate.Operation) error { return op.Write(context.Background(), change) })
}

// SQL lets tests arrange domain state through the same fenced transaction API.
// It never exposes a database handle or changes process ownership.
type SQL struct{ Owner *brokerstate.Owner }

func (s SQL) Exec(query string, args ...any) (result sql.Result, err error) {
	err = Write(s.Owner, func(tx *brokerstate.WriteTx) error { result, err = tx.Exec(query, args...); return err })
	return
}

type Row struct {
	owner *brokerstate.Owner
	query string
	args  []any
}

func (s SQL) QueryRow(query string, args ...any) Row { return Row{s.Owner, query, args} }
func (r Row) Scan(dest ...any) error {
	return Write(r.owner, func(tx *brokerstate.WriteTx) error { return tx.Scan(r.query, r.args, dest...) })
}
