package brokerstate_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seungpyoson/waggle/internal/broker"
	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/brokerstate/statetest"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/tasks"

	_ "modernc.org/sqlite"
)

// legacySchemaV1 is the verbatim schema text of the retired store, copied from
// main at commit 5fac053, internal/tasks/store.go lines 128-154. The brief cites
// 128-153, which stops one line short of the unique idempotency index that every
// real v1 store carries; the index is included so the fixture is a true v1 store.
// This text is frozen history and must never be edited to match current code.
const legacySchemaV1 = `
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
		lease_duration  INTEGER DEFAULT %d,
		max_retries     INTEGER DEFAULT %d,
		retry_count     INTEGER DEFAULT 0,
		result          TEXT,
		failure_reason  TEXT,
		created_at      TEXT NOT NULL DEFAULT (strftime('%%Y-%%m-%%dT%%H:%%M:%%SZ', 'now')),
		updated_at      TEXT NOT NULL DEFAULT (strftime('%%Y-%%m-%%dT%%H:%%M:%%SZ', 'now'))
	);

	CREATE INDEX IF NOT EXISTS idx_tasks_claimable ON tasks (state, blocked, priority DESC, created_at ASC);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_idempotency ON tasks (idempotency_key) WHERE idempotency_key IS NOT NULL;
	`

// legacyTaskCount is the number of rows every legacy fixture carries.
const legacyTaskCount = 3

// censusResult scripts one census answer.
type censusResult struct {
	handles []brokerstate.Handle
	err     error
}

// censusFixture answers each census method from its own script: entry n answers
// the nth call, and the last entry repeats. Convert must run a full census twice.
type censusFixture struct {
	processes []censusResult
	handles   []censusResult
	calls     struct{ processes, handles int }
	asked     [][]string
}

func (c *censusFixture) WaggleProcesses(context.Context) ([]brokerstate.Handle, error) {
	c.calls.processes++
	return scripted(c.processes, c.calls.processes)
}

func (c *censusFixture) OpenHandles(_ context.Context, paths []string) ([]brokerstate.Handle, error) {
	c.calls.handles++
	c.asked = append(c.asked, paths)
	return scripted(c.handles, c.calls.handles)
}

func scripted(script []censusResult, call int) ([]brokerstate.Handle, error) {
	if len(script) == 0 {
		return nil, nil
	}
	if call > len(script) {
		call = len(script)
	}
	return script[call-1].handles, script[call-1].err
}

func openDB(t *testing.T, path, mode string) *sql.DB {
	t.Helper()
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", mode)
	q.Set("_pragma", fmt.Sprintf("busy_timeout(%d)", config.Defaults.BusyTimeout.Milliseconds()))
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(config.CanonicalConnections)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close probe handle on %s: %v", path, err)
		}
	})
	return db
}

func exec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", strings.SplitN(strings.TrimSpace(query), "\n", 2)[0], err)
	}
}

// newLegacyStore writes a schema-v1 store with the given version rows (default
// one row of LegacySchemaVersion) and legacyTaskCount tasks.
func newLegacyStore(t *testing.T, dir string, versions ...int) string {
	t.Helper()
	if len(versions) == 0 {
		versions = []int{config.LegacySchemaVersion}
	}
	path := filepath.Join(dir, config.Defaults.DBFile)
	db := openDB(t, path, "rwc")
	exec(t, db, fmt.Sprintf(legacySchemaV1, int(config.Defaults.LeaseDuration.Seconds()), config.Defaults.MaxRetries))
	for _, v := range versions {
		exec(t, db, "INSERT INTO schema_version(version) VALUES (?)", v)
	}
	for i := 1; i <= legacyTaskCount; i++ {
		exec(t, db, "INSERT INTO tasks(payload, type, priority) VALUES (?, ?, ?)",
			fmt.Sprintf(`{"step":%d}`, i), "legacy", i)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// newNativeStore builds a store the way a fresh broker start does.
func newNativeStore(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, config.Defaults.DBFile)
	owner, err := brokerstate.Acquire(t.Context(), config.NewOwnershipConfig(path, config.CreateStore), statetest.Process{})
	if err != nil {
		t.Fatal(err)
	}
	if err := broker.Initialize(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	owner.BeginShutdown(nil)
	if err := owner.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	return path
}

func testPaths(t *testing.T, dir string) config.Paths {
	t.Helper()
	return config.Paths{
		DataDir:      dir,
		DB:           filepath.Join(dir, config.Defaults.DBFile),
		LegacyPID:    filepath.Join(dir, config.Defaults.LegacyPIDFile),
		LegacySocket: filepath.Join(dir, config.Defaults.LegacySocketFile),
		SnapshotDir:  filepath.Join(dir, config.Defaults.SnapshotDir),
	}
}

func fileHash(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s to be absent, stat error = %v", path, err)
	}
}

// tasksShape renders every property of the tasks table that a fresh store and a
// converted store must share: column order, types, defaults and index DDL.
func tasksShape(t *testing.T, path string) string {
	t.Helper()
	db := openDB(t, path, "ro")
	var out strings.Builder
	rows, err := db.Query(`SELECT cid, name, type, "notnull", coalesce(dflt_value, ''), pk
		FROM pragma_table_info('tasks') ORDER BY cid`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind, dflt string
		if err := rows.Scan(&cid, &name, &kind, &notNull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&out, "column %d %s %s notnull=%d default=%q pk=%d\n", cid, name, kind, notNull, dflt, pk)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	objects, err := db.Query(`SELECT type, name, coalesce(sql, '') FROM sqlite_schema
		WHERE tbl_name = 'tasks' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for objects.Next() {
		var kind, name, ddl string
		if err := objects.Scan(&kind, &name, &ddl); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&out, "object %s %s %s\n", kind, name, strings.Join(strings.Fields(ddl), " "))
	}
	if err := errors.Join(objects.Err(), objects.Close()); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func scan(t *testing.T, path, query string, dest ...any) error {
	t.Helper()
	return openDB(t, path, "ro").QueryRow(query).Scan(dest...)
}

// TestConvertUpgradesLegacyStoreAndPreservesTasks is the whole point of the
// unit: a v1 store becomes a native store that the new broker can acquire, with
// every task preserved and activation still an explicit, separate decision.
func TestConvertUpgradesLegacyStoreAndPreservesTasks(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(t, dir)
	source := newLegacyStore(t, dir)
	census := &censusFixture{}

	report, err := brokerstate.Convert(t.Context(), brokerstate.NewConversionConfig(paths), census, statetest.Process{}, broker.UpgradeDomain)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if report.Database != source || report.FromVersion != config.LegacySchemaVersion ||
		report.ToVersion != config.NativeSchemaVersion || report.Tasks != legacyTaskCount {
		t.Fatalf("report = %+v", report)
	}
	if report.ConvertedAt.IsZero() {
		t.Fatal("report carries no conversion time")
	}
	if info, err := os.Lstat(report.Snapshot); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("snapshot %s: %v", report.Snapshot, err)
	}
	if census.calls.processes != 2 || census.calls.handles != 2 {
		t.Fatalf("census calls = %+v, want two full censuses", census.calls)
	}
	for _, asked := range census.asked {
		if len(asked) != 1 || asked[0] != source {
			t.Fatalf("census asked about %v, want the canonical database", asked)
		}
	}

	var count, low, high int
	if err := scan(t, source, "SELECT count(*), coalesce(min(version), 0), coalesce(max(version), 0) FROM schema_version", &count, &low, &high); err != nil {
		t.Fatal(err)
	}
	if count != 1 || low != config.NativeSchemaVersion || high != config.NativeSchemaVersion {
		t.Fatalf("schema_version rows=%d min=%d max=%d", count, low, high)
	}

	var state, convertedAt, snapshot string
	var sourceSchema int
	if err := scan(t, source, "SELECT state, source_schema, converted_at, snapshot FROM cutover WHERE singleton = 1",
		&state, &sourceSchema, &convertedAt, &snapshot); err != nil {
		t.Fatal(err)
	}
	if state != "prepared" || sourceSchema != config.LegacySchemaVersion || convertedAt == "" || snapshot != report.Snapshot {
		t.Fatalf("cutover state=%q source=%d at=%q snapshot=%q", state, sourceSchema, convertedAt, snapshot)
	}

	var kept, nullTTL int
	if err := scan(t, source, "SELECT count(*), sum(ttl IS NULL) FROM tasks WHERE type = 'legacy'", &kept, &nullTTL); err != nil {
		t.Fatal(err)
	}
	if kept != legacyTaskCount || nullTTL != legacyTaskCount {
		t.Fatalf("tasks kept=%d with null ttl=%d, want %d", kept, nullTTL, legacyTaskCount)
	}
	var payload string
	if err := scan(t, source, "SELECT payload FROM tasks WHERE id = 2", &payload); err != nil {
		t.Fatal(err)
	}
	if payload != `{"step":2}` {
		t.Fatalf("task payload = %q", payload)
	}

	var domain int
	if err := scan(t, source, `SELECT count(*) FROM sqlite_schema WHERE name IN
		('enrollments','conversations','messages','attempts','receipts','recipient_barrier','message_status')`, &domain); err != nil {
		t.Fatal(err)
	}
	if domain != 7 {
		t.Fatalf("message tables present = %d, want 7", domain)
	}

	var journal string
	if err := scan(t, source, "PRAGMA journal_mode", &journal); err != nil {
		t.Fatal(err)
	}
	if journal != config.CanonicalJournalMode {
		t.Fatalf("journal mode = %q", journal)
	}

	fresh := newNativeStore(t, t.TempDir())
	if converted, expected := tasksShape(t, source), tasksShape(t, fresh); converted != expected {
		t.Fatalf("converted tasks schema differs from a fresh store:\n--- converted ---\n%s\n--- fresh ---\n%s", converted, expected)
	}

	owner, err := brokerstate.Acquire(t.Context(), config.NewOwnershipConfig(source, config.OpenStore), statetest.Process{})
	if err != nil {
		t.Fatalf("acquire converted store: %v", err)
	}
	t.Cleanup(func() {
		owner.BeginShutdown(nil)
		if err := owner.Wait(context.Background()); err != nil {
			t.Errorf("release converted store: %v", err)
		}
	})
	if err := statetest.Write(owner, func(tx *brokerstate.WriteTx) error { return tx.RequireActive() }); !errors.Is(err, brokerstate.ErrPrepared) {
		t.Fatalf("RequireActive before activation = %v, want ErrPrepared", err)
	}
	if err := owner.Activate(t.Context()); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if err := statetest.Write(owner, func(tx *brokerstate.WriteTx) error { return tx.RequireActive() }); err != nil {
		t.Fatalf("RequireActive after activation = %v", err)
	}
	if err := owner.Activate(t.Context()); err != nil {
		t.Fatalf("second activation = %v, want a repeatable transition", err)
	}
}

// TestConvertRefusesNonLegacyAndDuplicateVersions proves every refusal leaves
// the source byte-identical and never writes a snapshot.
func TestConvertRefusesNonLegacyAndDuplicateVersions(t *testing.T) {
	t.Run("native store", func(t *testing.T) {
		dir := t.TempDir()
		paths := testPaths(t, dir)
		source := newNativeStore(t, dir)
		before := fileHash(t, source)
		if _, err := brokerstate.Convert(t.Context(), brokerstate.NewConversionConfig(paths), &censusFixture{}, statetest.Process{}, broker.UpgradeDomain); !errors.Is(err, brokerstate.ErrNotLegacy) {
			t.Fatalf("convert native store = %v, want ErrNotLegacy", err)
		}
		if fileHash(t, source) != before {
			t.Fatal("refused conversion changed the source")
		}
		mustNotExist(t, paths.SnapshotDir)
	})

	t.Run("duplicate version rows", func(t *testing.T) {
		dir := t.TempDir()
		paths := testPaths(t, dir)
		source := newLegacyStore(t, dir, config.LegacySchemaVersion, config.LegacySchemaVersion)
		before := fileHash(t, source)
		_, err := brokerstate.Convert(t.Context(), brokerstate.NewConversionConfig(paths), &censusFixture{}, statetest.Process{}, broker.UpgradeDomain)
		if err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("convert duplicate versions = %v, want a duplicate-row error", err)
		}
		if fileHash(t, source) != before {
			t.Fatal("refused conversion changed the source")
		}
		mustNotExist(t, paths.SnapshotDir)
	})

	t.Run("missing database", func(t *testing.T) {
		dir := t.TempDir()
		paths := testPaths(t, dir)
		if _, err := brokerstate.Convert(t.Context(), brokerstate.NewConversionConfig(paths), &censusFixture{}, statetest.Process{}, broker.UpgradeDomain); err == nil {
			t.Fatal("convert missing database succeeded")
		}
		mustNotExist(t, paths.DB)
		mustNotExist(t, paths.SnapshotDir)
	})

	t.Run("symlinked database", func(t *testing.T) {
		dir := t.TempDir()
		target := newLegacyStore(t, t.TempDir())
		before := fileHash(t, target)
		paths := testPaths(t, dir)
		if err := os.Symlink(target, paths.DB); err != nil {
			t.Fatal(err)
		}
		_, err := brokerstate.Convert(t.Context(), brokerstate.NewConversionConfig(paths), &censusFixture{}, statetest.Process{}, broker.UpgradeDomain)
		if err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("convert through symlink = %v, want a regular-file refusal", err)
		}
		if fileHash(t, target) != before {
			t.Fatal("refused conversion changed the symlink target")
		}
		mustNotExist(t, paths.SnapshotDir)
	})
}

// TestConvertBlocksOnCensus proves an uncertain or occupied census stops the
// conversion before anything is written, including the snapshot.
func TestConvertBlocksOnCensus(t *testing.T) {
	t.Run("writer present", func(t *testing.T) {
		dir := t.TempDir()
		paths := testPaths(t, dir)
		source := newLegacyStore(t, dir)
		before := fileHash(t, source)
		census := &censusFixture{handles: []censusResult{{handles: []brokerstate.Handle{{PID: 4242, Command: "waggle", Path: source}}}}}
		_, err := brokerstate.Convert(t.Context(), brokerstate.NewConversionConfig(paths), census, statetest.Process{}, broker.UpgradeDomain)
		if !errors.Is(err, brokerstate.ErrWritersPresent) {
			t.Fatalf("convert with an open handle = %v, want ErrWritersPresent", err)
		}
		if !strings.Contains(err.Error(), "4242") {
			t.Fatalf("blocked conversion does not name the holder: %v", err)
		}
		if fileHash(t, source) != before {
			t.Fatal("blocked conversion changed the source")
		}
		mustNotExist(t, paths.SnapshotDir)
	})

	t.Run("census unavailable", func(t *testing.T) {
		dir := t.TempDir()
		paths := testPaths(t, dir)
		source := newLegacyStore(t, dir)
		before := fileHash(t, source)
		census := &censusFixture{processes: []censusResult{{err: errors.New("lsof missing")}}}
		_, err := brokerstate.Convert(t.Context(), brokerstate.NewConversionConfig(paths), census, statetest.Process{}, broker.UpgradeDomain)
		if !errors.Is(err, brokerstate.ErrCensusUnavailable) {
			t.Fatalf("convert with an unusable census = %v, want ErrCensusUnavailable", err)
		}
		if fileHash(t, source) != before {
			t.Fatal("blocked conversion changed the source")
		}
		mustNotExist(t, paths.SnapshotDir)
	})
}

// TestConvertIsAtomicOnFailure fails the census re-check that runs immediately
// before the transaction: the snapshot is already taken, and the source must
// still be untouched.
func TestConvertIsAtomicOnFailure(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(t, dir)
	source := newLegacyStore(t, dir)
	before := fileHash(t, source)
	census := &censusFixture{processes: []censusResult{
		{},
		{handles: []brokerstate.Handle{{PID: 77, Command: "waggle", Path: "/usr/local/bin/waggle"}}},
	}}

	_, err := brokerstate.Convert(t.Context(), brokerstate.NewConversionConfig(paths), census, statetest.Process{}, broker.UpgradeDomain)
	if !errors.Is(err, brokerstate.ErrWritersPresent) {
		t.Fatalf("convert = %v, want ErrWritersPresent from the re-check", err)
	}
	if fileHash(t, source) != before {
		t.Fatal("interrupted conversion changed the source")
	}
	if census.calls.processes != 2 {
		t.Fatalf("census ran %d times, want a re-check immediately before conversion", census.calls.processes)
	}
	entries, err := os.ReadDir(paths.SnapshotDir)
	if err != nil {
		t.Fatalf("snapshot directory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("snapshot directory holds %d files, want the snapshot taken before the re-check", len(entries))
	}
	snapshot := filepath.Join(paths.SnapshotDir, entries[0].Name())
	var version, kept int
	if err := scan(t, snapshot, "SELECT (SELECT version FROM schema_version), (SELECT count(*) FROM tasks)", &version, &kept); err != nil {
		t.Fatal(err)
	}
	if version != config.LegacySchemaVersion || kept != legacyTaskCount {
		t.Fatalf("snapshot version=%d tasks=%d", version, kept)
	}
}

// TestRollbackRestoresSnapshotWhilePrepared proves the escape hatch works while
// the store is prepared and closes once it is active.
func TestRollbackRestoresSnapshotWhilePrepared(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(t, dir)
	source := newLegacyStore(t, dir)
	cfg := brokerstate.NewConversionConfig(paths)

	converted, err := brokerstate.Convert(t.Context(), cfg, &censusFixture{}, statetest.Process{}, broker.UpgradeDomain)
	if err != nil {
		t.Fatal(err)
	}
	report, err := brokerstate.Rollback(t.Context(), cfg, &censusFixture{})
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if report.Snapshot != converted.Snapshot || report.FromVersion != config.NativeSchemaVersion || report.ToVersion != config.LegacySchemaVersion {
		t.Fatalf("rollback report = %+v", report)
	}
	mustNotExist(t, source+"-wal")
	mustNotExist(t, source+"-shm")
	var version, kept, restored int
	if err := scan(t, source, `SELECT (SELECT count(*) FROM schema_version), (SELECT version FROM schema_version),
		(SELECT count(*) FROM tasks WHERE type = 'legacy')`, &restored, &version, &kept); err != nil {
		t.Fatal(err)
	}
	if restored != 1 || version != config.LegacySchemaVersion || kept != legacyTaskCount {
		t.Fatalf("restored rows=%d version=%d tasks=%d", restored, version, kept)
	}
	if report.Tasks != legacyTaskCount {
		t.Fatalf("rollback report tasks = %d", report.Tasks)
	}
	var cutover int
	if err := scan(t, source, "SELECT count(*) FROM sqlite_schema WHERE name = 'cutover'", &cutover); err != nil {
		t.Fatal(err)
	}
	if cutover != 0 {
		t.Fatal("restored store still carries native ownership tables")
	}

	if _, err := brokerstate.Convert(t.Context(), cfg, &censusFixture{}, statetest.Process{}, broker.UpgradeDomain); err != nil {
		t.Fatalf("convert restored store: %v", err)
	}
	owner, err := brokerstate.Acquire(t.Context(), config.NewOwnershipConfig(source, config.OpenStore), statetest.Process{})
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Activate(t.Context()); err != nil {
		t.Fatal(err)
	}
	owner.BeginShutdown(nil)
	if err := owner.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := brokerstate.Rollback(t.Context(), cfg, &censusFixture{}); !errors.Is(err, brokerstate.ErrNotPrepared) {
		t.Fatalf("rollback of an active store = %v, want ErrNotPrepared", err)
	}

	blocked := &censusFixture{handles: []censusResult{{handles: []brokerstate.Handle{{PID: 99, Command: "waggle", Path: source}}}}}
	if _, err := brokerstate.Rollback(t.Context(), cfg, blocked); !errors.Is(err, brokerstate.ErrWritersPresent) {
		t.Fatalf("rollback with an open handle = %v, want ErrWritersPresent", err)
	}
}

// TestAcquireRejectsLegacyStoreSoActivationIsUnreachable is the reason offline
// conversion has to exist: a legacy store cannot be owned, so nothing can
// activate one or write to it through ownership. Activation is only reachable
// through a converted store.
func TestAcquireRejectsLegacyStoreSoActivationIsUnreachable(t *testing.T) {
	dir := t.TempDir()
	source := newLegacyStore(t, dir)
	before := fileHash(t, source)
	owner, err := brokerstate.Acquire(t.Context(), config.NewOwnershipConfig(source, config.OpenStore), statetest.Process{})
	if !errors.Is(err, brokerstate.ErrSchemaVersion) {
		t.Fatalf("acquire legacy store = %v, want ErrSchemaVersion", err)
	}
	if owner != nil {
		t.Fatal("acquisition of a legacy store handed out an owner")
	}
	if fileHash(t, source) != before {
		t.Fatal("refused acquisition changed the legacy store")
	}
	if _, err := brokerstate.Rollback(t.Context(), brokerstate.NewConversionConfig(testPaths(t, dir)), &censusFixture{}); !errors.Is(err, brokerstate.ErrNotPrepared) {
		t.Fatalf("rollback of a legacy store = %v, want ErrNotPrepared", err)
	}
}

// TestInspectIsReadOnly proves the operator's view creates and changes nothing.
func TestInspectIsReadOnly(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(t, dir)
	source := newLegacyStore(t, dir)
	if err := os.WriteFile(paths.LegacyPID, []byte("1234\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before := fileHash(t, source)
	cfg := brokerstate.NewConversionConfig(paths)

	view, err := brokerstate.Inspect(t.Context(), cfg, paths)
	if err != nil {
		t.Fatalf("inspect legacy store: %v", err)
	}
	if !view.Exists || view.Database != source || view.SchemaVersion != config.LegacySchemaVersion {
		t.Fatalf("legacy inspection = %+v", view)
	}
	if view.Cutover != "" || view.Provenance != nil || len(view.Snapshots) != 0 {
		t.Fatalf("legacy inspection reports native state: %+v", view)
	}
	if !view.LegacyPIDPresent || view.LegacySocketPresent {
		t.Fatalf("legacy endpoint flags = pid:%v socket:%v", view.LegacyPIDPresent, view.LegacySocketPresent)
	}
	if fileHash(t, source) != before {
		t.Fatal("inspection changed the database")
	}
	mustNotExist(t, paths.SnapshotDir)
	mustNotExist(t, source+"-wal")
	mustNotExist(t, source+"-shm")

	missing := testPaths(t, t.TempDir())
	empty, err := brokerstate.Inspect(t.Context(), brokerstate.NewConversionConfig(missing), missing)
	if err != nil {
		t.Fatalf("inspect missing store: %v", err)
	}
	if empty.Exists || empty.SchemaVersion != 0 || empty.Cutover != "" {
		t.Fatalf("missing-store inspection = %+v", empty)
	}
	mustNotExist(t, missing.DB)

	report, err := brokerstate.Convert(t.Context(), cfg, &censusFixture{}, statetest.Process{}, broker.UpgradeDomain)
	if err != nil {
		t.Fatal(err)
	}
	after, err := brokerstate.Inspect(t.Context(), cfg, paths)
	if err != nil {
		t.Fatalf("inspect converted store: %v", err)
	}
	if after.SchemaVersion != config.NativeSchemaVersion || after.Cutover != "prepared" {
		t.Fatalf("converted inspection = %+v", after)
	}
	if after.Provenance == nil || after.Provenance.SourceSchema != config.LegacySchemaVersion ||
		after.Provenance.Snapshot != report.Snapshot || after.Provenance.ConvertedAt == "" {
		t.Fatalf("converted provenance = %+v", after.Provenance)
	}
	if len(after.Snapshots) != 1 || after.Snapshots[0] != report.Snapshot {
		t.Fatalf("snapshot listing = %v", after.Snapshots)
	}
}

// TestUpgradeFromV1MatchesFreshSchema keeps the upgrade honest: the tasks table
// a converted store carries must be the table Schema() creates, not an
// approximation of it. It lives beside the v1 fixture so that frozen text has
// exactly one copy in the tree.
func TestUpgradeFromV1MatchesFreshSchema(t *testing.T) {
	upgraded := filepath.Join(t.TempDir(), config.Defaults.DBFile)
	db := openDB(t, upgraded, "rwc")
	exec(t, db, fmt.Sprintf(legacySchemaV1, int(config.Defaults.LeaseDuration.Seconds()), config.Defaults.MaxRetries))
	exec(t, db, "INSERT INTO tasks(payload, type) VALUES ('{}', 'legacy')")
	exec(t, db, tasks.UpgradeFromV1())
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	fresh := filepath.Join(t.TempDir(), config.Defaults.DBFile)
	freshDB := openDB(t, fresh, "rwc")
	exec(t, freshDB, tasks.Schema())
	if err := freshDB.Close(); err != nil {
		t.Fatal(err)
	}

	if got, want := tasksShape(t, upgraded), tasksShape(t, fresh); got != want {
		t.Fatalf("upgraded schema differs from Schema():\n--- upgraded ---\n%s\n--- fresh ---\n%s", got, want)
	}
	var kept int
	var ttl sql.NullInt64
	if err := scan(t, upgraded, "SELECT (SELECT count(*) FROM tasks), (SELECT ttl FROM tasks WHERE id = 1)", &kept, &ttl); err != nil {
		t.Fatal(err)
	}
	if kept != 1 || ttl.Valid {
		t.Fatalf("upgraded rows=%d ttl valid=%v", kept, ttl.Valid)
	}
}
