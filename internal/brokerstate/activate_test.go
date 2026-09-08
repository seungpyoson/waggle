package brokerstate_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/seungpyoson/waggle/internal/broker"
	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/brokerstate/statetest"
	"github.com/seungpyoson/waggle/internal/config"
)

func preparedStore(t *testing.T, dir string, pendingJournal bool) config.Paths {
	t.Helper()
	paths := testPaths(t, dir)
	newLegacyStore(t, dir, fieldShapes()[1])
	if _, err := brokerstate.Convert(t.Context(), brokerstate.NewConversionConfig(paths), &censusFixture{}, statetest.Process{}, broker.UpgradeDomain); err != nil {
		t.Fatal(err)
	}
	if pendingJournal {
		db := openDB(t, paths.DB, "rw")
		var journal string
		if err := db.QueryRow("PRAGMA journal_mode=delete").Scan(&journal); err != nil || journal != "delete" {
			t.Fatalf("arrange pending journal: %s, %v", journal, err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return paths
}

func releaseOwner(t *testing.T, owner *brokerstate.Owner) {
	t.Helper()
	owner.BeginShutdown(nil)
	if err := owner.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestActivateResumesPostCommitTail(t *testing.T) {
	paths := preparedStore(t, shortDir(t), true)
	legacyEndpoints(t, paths)
	cfg := brokerstate.NewConversionConfig(paths)
	if _, err := brokerstate.Convert(t.Context(), cfg, &censusFixture{}, statetest.Process{}, broker.UpgradeDomain); !errors.Is(err, brokerstate.ErrNotLegacy) {
		t.Fatalf("convert prepared DELETE store: %v", err)
	}
	owner, err := brokerstate.Acquire(t.Context(), config.NewOwnershipConfig(paths.DB, config.OpenStore), statetest.Process{})
	if err != nil {
		t.Fatalf("acquire prepared store to resume activation: %v", err)
	}
	t.Cleanup(func() { releaseOwner(t, owner) })
	if err := statetest.Write(owner, func(tx *brokerstate.WriteTx) error { return tx.RequireActive() }); !errors.Is(err, brokerstate.ErrPrepared) {
		t.Fatalf("acquisition admitted service before activation: %v", err)
	}
	activation, err := owner.Activate(t.Context(), cfg)
	if err != nil {
		t.Fatalf("resume activation: %v", err)
	}
	if activation.AlreadyActive || !slices.Equal(activation.RetiredEndpoints, []string{paths.LegacyPID, paths.LegacySocket}) {
		t.Fatalf("activation report = %+v", activation)
	}
	var journal string
	if err := statetest.Write(owner, func(tx *brokerstate.WriteTx) error {
		if err := tx.RequireActive(); err != nil {
			return err
		}
		return tx.Scan("PRAGMA journal_mode", nil, &journal)
	}); err != nil || journal != config.CanonicalJournalMode {
		t.Fatalf("activation did not finish journal and cutover: %s, %v", journal, err)
	}
	mustNotExist(t, paths.LegacyPID)
	mustNotExist(t, paths.LegacySocket)
	mustExist(t, paths.PID)
	mustExist(t, paths.Socket)
	again, err := owner.Activate(t.Context(), cfg)
	if err != nil || !again.AlreadyActive || len(again.RetiredEndpoints) != 0 {
		t.Fatalf("repeated activation = %+v, %v", again, err)
	}
}

func TestActivateRetirementFailureStaysPreparedAndCanResume(t *testing.T) {
	paths := preparedStore(t, t.TempDir(), false)
	if err := os.Mkdir(paths.LegacySocket, 0700); err != nil {
		t.Fatal(err)
	}
	owner, err := brokerstate.Acquire(t.Context(), config.NewOwnershipConfig(paths.DB, config.OpenStore), statetest.Process{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { releaseOwner(t, owner) })
	if _, err := owner.Activate(t.Context(), brokerstate.NewConversionConfig(paths)); err == nil {
		t.Error("activated despite a legacy endpoint that cannot be retired")
	}
	if err := statetest.Write(owner, func(tx *brokerstate.WriteTx) error { return tx.RequireActive() }); !errors.Is(err, brokerstate.ErrPrepared) {
		t.Errorf("failed retirement left the prepared state: %v", err)
	}
	mustExist(t, paths.LegacySocket)
	if err := os.Remove(paths.LegacySocket); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Activate(t.Context(), brokerstate.NewConversionConfig(paths)); err != nil {
		t.Fatalf("resume after clearing the foreign endpoint: %v", err)
	}
}
