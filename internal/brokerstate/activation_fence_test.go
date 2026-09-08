package brokerstate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/seungpyoson/waggle/internal/config"
)

// Both journal and filesystem work must be unreachable through an expired
// incarnation, even though neither operation is itself a SQL domain write.
func TestActivateTailRequiresCurrentOwnership(t *testing.T) {
	for _, revoked := range []string{"released", "fenced"} {
		t.Run(revoked, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			paths := config.NewPaths(t.Name())
			for _, dir := range []string{paths.DataDir, filepath.Dir(paths.LegacySocket)} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			cfg := config.NewOwnershipConfig(paths.DB, config.CreateStore)
			owner, err := Acquire(t.Context(), cfg, processFixture{status: ProcessAlive})
			if err != nil {
				t.Fatal(err)
			}
			owner.BeginShutdown(nil)
			if err := owner.Wait(t.Context()); err != nil {
				t.Fatal(err)
			}
			db, err := openCanonical(paths.DB, "rw", cfg.TransactionTimeout)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec("PRAGMA journal_mode=delete"); err != nil {
				t.Fatal(err)
			}
			for _, endpoint := range []string{paths.LegacyPID, paths.LegacySocket} {
				if err := os.WriteFile(endpoint, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			want := ErrAdmissionClosed
			if revoked == "fenced" {
				cfg.Action = config.OpenStore
				owner, err = Acquire(t.Context(), cfg, processFixture{status: ProcessAlive})
				if err != nil {
					t.Fatal(err)
				}
				defer owner.state.db.Close() // stale fixture cannot release the successor
				if _, err := db.Exec("UPDATE broker_owner SET generation = generation + 1"); err != nil {
					t.Fatal(err)
				}
				want = ErrFenced
			}
			if _, err := owner.Activate(t.Context(), NewConversionConfig(paths)); !errors.Is(err, want) {
				t.Fatalf("activation after %s = %v, want %v", revoked, err, want)
			}
			var journal, cutover string
			if err := db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil || journal != "delete" {
				t.Fatalf("unowned journal switch: %s, %v", journal, err)
			}
			if err := db.QueryRow("SELECT state FROM cutover").Scan(&cutover); err != nil || cutover != "prepared" {
				t.Fatalf("unowned activation: %s, %v", cutover, err)
			}
			for _, endpoint := range []string{paths.LegacyPID, paths.LegacySocket} {
				if _, err := os.Stat(endpoint); err != nil {
					t.Fatalf("unowned endpoint retirement: %v", err)
				}
			}
		})
	}
}

func TestAcquirePendingJournalKeepsOwnerAndServiceGuards(t *testing.T) {
	owner, cfg := newOwner(t)
	owner.BeginShutdown(nil)
	if err := owner.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	db, err := openCanonical(cfg.Database, "rw", cfg.TransactionTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA journal_mode=delete"); err != nil {
		t.Fatal(err)
	}
	cfg.Action = config.OpenStore
	owner, err = Acquire(t.Context(), cfg, processFixture{status: ProcessAlive})
	if err != nil {
		t.Fatal(err)
	}
	for _, process := range []processFixture{{status: ProcessAlive}, {err: ErrIdentityUnavailable}} {
		if _, err := Acquire(t.Context(), cfg, process); err == nil {
			t.Fatal("pending journal allowed a competing owner")
		}
	}
	if err := write(owner, func(tx *WriteTx) error { return tx.RequireActive() }); !errors.Is(err, ErrPrepared) {
		t.Fatalf("pending journal admitted service: %v", err)
	}
	owner.BeginShutdown(nil)
	if err := owner.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE cutover SET state = 'active'"); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(t.Context(), cfg, processFixture{status: ProcessAlive}); err == nil || errors.Is(err, ErrSchemaVersion) {
		t.Fatalf("active non-WAL store must refuse as a journal failure: %v", err)
	}
}
