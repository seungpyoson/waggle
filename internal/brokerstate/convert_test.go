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
	"sort"
	"strings"
	"testing"

	"github.com/seungpyoson/waggle/internal/broker"
	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/brokerstate/statetest"
	"github.com/seungpyoson/waggle/internal/config"

	_ "modernc.org/sqlite"
)

// The frozen legacy store. A real schema-v1 store is not the retired broker's
// CREATE text alone: that broker migrated the store on every open, so the shape
// in the field also carries the runtime ALTERs below. All of this text is
// history and must never be edited to match current code.
//
// legacyTasksSchemaV1 is main (commit 5fac053) internal/tasks/store.go:128-154.
const legacyTasksSchemaV1 = `
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

// legacyTasksTTLMigration is main:internal/tasks/store.go:193, run on every open
// by migrateTaskSchema, so ttl is the last column of any store that broker ran.
const legacyTasksTTLMigration = `ALTER TABLE tasks ADD COLUMN ttl INTEGER`

// legacyMessagesSchemaV1 is main:internal/messages/store.go:42-50 and its index
// at line 58, created by NewStore on every open of the old broker.
const legacyMessagesSchemaV1 = `
	CREATE TABLE IF NOT EXISTS messages (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		from_name   TEXT NOT NULL,
		to_name     TEXT NOT NULL,
		body        TEXT NOT NULL,
		state       TEXT DEFAULT 'queued',
		created_at  TEXT NOT NULL,
		pushed_at   TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_messages_to_name ON messages(to_name, state);
	`

// legacyMessagesMigrations is main:internal/messages/store.go:79-82.
var legacyMessagesMigrations = []string{
	`ALTER TABLE messages ADD COLUMN seen_at TEXT`,
	`ALTER TABLE messages ADD COLUMN acked_at TEXT`,
	`ALTER TABLE messages ADD COLUMN priority TEXT NOT NULL DEFAULT 'normal'`,
	`ALTER TABLE messages ADD COLUMN ttl INTEGER`,
}

const (
	legacyTaskCount    = 3
	legacyMessageCount = 2
	// retiredSequence is the AUTOINCREMENT high-water mark of the fixtures: a
	// fourth task is inserted and deleted so the sequence outruns max(id) and a
	// rebuild that drops it becomes visible.
	retiredSequence = 4
)

// legacyShape is one of the store shapes that exist in the field.
type legacyShape struct {
	name     string
	ttl      bool // the retired broker's task migration had run
	messages bool // the retired broker had opened its message store
	wal      bool
	versions []int
}

// fieldShapes are the two shapes every conversion test runs over: a store the
// retired broker created but never migrated, and the shape it leaves behind
// once it has actually run.
func fieldShapes() []legacyShape {
	return []legacyShape{
		{name: "never migrated", versions: []int{config.LegacySchemaVersion}},
		{name: "migrated wal store", ttl: true, messages: true, wal: true, versions: []int{config.LegacySchemaVersion}},
	}
}

// duplicateVersionShape is the migrated shape with a version record conversion
// must refuse.
func duplicateVersionShape() legacyShape {
	shape := fieldShapes()[1]
	shape.name = "duplicate version rows"
	shape.versions = []int{config.LegacySchemaVersion, config.LegacySchemaVersion}
	return shape
}

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

// newLegacyStore builds the given field shape through the retired broker's own
// statements, in the order that broker ran them.
func newLegacyStore(t *testing.T, dir string, shape legacyShape) string {
	t.Helper()
	path := filepath.Join(dir, config.Defaults.DBFile)
	db := openDB(t, path, "rwc")
	if shape.wal {
		var mode string
		if err := db.QueryRow("PRAGMA journal_mode=" + config.CanonicalJournalMode).Scan(&mode); err != nil {
			t.Fatal(err)
		}
		if mode != config.CanonicalJournalMode {
			t.Fatalf("legacy fixture journal mode = %q", mode)
		}
	}
	exec(t, db, fmt.Sprintf(legacyTasksSchemaV1, int(config.Defaults.LeaseDuration.Seconds()), config.Defaults.MaxRetries))
	if shape.ttl {
		exec(t, db, legacyTasksTTLMigration)
	}
	for _, v := range shape.versions {
		exec(t, db, "INSERT INTO schema_version(version) VALUES (?)", v)
	}
	for i := 1; i <= legacyTaskCount; i++ {
		exec(t, db, `INSERT INTO tasks(idempotency_key, type, tags, payload, priority, state, blocked,
			depends_on, claim_token, claimed_by, claimed_at, lease_expires_at, retry_count, result, failure_reason)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("key-%d", i), "review", `["a","b"]`, fmt.Sprintf(`{"step":%d}`, i), i,
			"claimed", i%2, "[]", fmt.Sprintf("token-%d", i), "worker", "2026-01-01T00:00:00Z",
			"2026-01-01T00:05:00Z", i, `{"ok":true}`, "")
	}
	if shape.ttl {
		// Only a migrated store can carry ttl values, and one row keeping NULL
		// proves the column is copied rather than defaulted.
		exec(t, db, "UPDATE tasks SET ttl = 600 WHERE id = 1")
		exec(t, db, "UPDATE tasks SET ttl = 30 WHERE id = 2")
	}
	// Retire a row so the AUTOINCREMENT sequence outruns max(id).
	exec(t, db, "INSERT INTO tasks(payload) VALUES ('{}')")
	exec(t, db, "DELETE FROM tasks WHERE id = ?", retiredSequence)
	if shape.messages {
		exec(t, db, legacyMessagesSchemaV1)
		for _, migration := range legacyMessagesMigrations {
			exec(t, db, migration)
		}
		for i := 1; i <= legacyMessageCount; i++ {
			exec(t, db, `INSERT INTO messages(from_name, to_name, body, state, created_at, pushed_at, seen_at, acked_at, priority, ttl)
				VALUES (?, ?, ?, 'queued', '2026-01-01T00:00:00Z', NULL, NULL, NULL, 'normal', ?)`,
				"alice", "bob", fmt.Sprintf("body %d", i), 60*i)
		}
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

func mustHaveNoSidecars(t *testing.T, database string) {
	t.Helper()
	mustNotExist(t, database+"-wal")
	mustNotExist(t, database+"-shm")
}

// tableShape renders every property of a table that a fresh store and a
// converted store must share: column order, types, defaults and index DDL.
func tableShape(t *testing.T, path, table string) string {
	t.Helper()
	db := openDB(t, path, "ro")
	var out strings.Builder
	rows, err := db.Query(`SELECT cid, name, type, "notnull", coalesce(dflt_value, ''), pk
		FROM pragma_table_info(?) ORDER BY cid`, table)
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
		WHERE tbl_name = ? ORDER BY name`, table)
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

// columnsOf reports a table's columns in declaration order.
func columnsOf(t *testing.T, path, table string) []string {
	t.Helper()
	db := openDB(t, path, "ro")
	rows, err := db.Query("SELECT name FROM pragma_table_info(?) ORDER BY cid", table)
	if err != nil {
		t.Fatal(err)
	}
	var columns []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	if len(columns) == 0 {
		t.Fatalf("table %s has no columns in %s", table, path)
	}
	return columns
}

// dumpRows renders the named columns of every row as name=value pairs sorted by
// column name, so two stores can be compared whatever order their columns are
// in. Naming the columns lets a v1 store be compared against the v2 table it
// became, which has one column the source never had.
func dumpRows(t *testing.T, path, table, order string, columns []string) string {
	t.Helper()
	db := openDB(t, path, "ro")
	rows, err := db.Query("SELECT " + strings.Join(columns, ", ") + " FROM " + table + " ORDER BY " + order)
	if err != nil {
		t.Fatalf("dump %s: %v", table, err)
	}
	var out strings.Builder
	for rows.Next() {
		cells := make([]any, len(columns))
		for i := range cells {
			cells[i] = new(sql.NullString)
		}
		if err := rows.Scan(cells...); err != nil {
			t.Fatal(err)
		}
		pairs := make([]string, 0, len(columns))
		for i, name := range columns {
			value := cells[i].(*sql.NullString)
			pairs = append(pairs, fmt.Sprintf("%s=%v/%q", name, value.Valid, value.String))
		}
		sort.Strings(pairs)
		fmt.Fprintln(&out, strings.Join(pairs, " "))
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func scan(t *testing.T, path, query string, dest ...any) error {
	t.Helper()
	return openDB(t, path, "ro").QueryRow(query).Scan(dest...)
}

func taskSequence(t *testing.T, path string) int {
	t.Helper()
	var seq sql.NullInt64
	if err := scan(t, path, "SELECT seq FROM sqlite_sequence WHERE name = 'tasks'", &seq); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	return int(seq.Int64)
}

// TestConvertUpgradesLegacyStoreAndPreservesTasks is the whole point of the
// unit: a real v1 store becomes a native store the new broker can acquire, with
// every task row, its ttl and its id sequence preserved, the retired message
// table kept aside, and activation still an explicit, separate decision.
func TestConvertUpgradesLegacyStoreAndPreservesTasks(t *testing.T) {
	for _, shape := range fieldShapes() {
		t.Run(shape.name, func(t *testing.T) {
			dir := t.TempDir()
			paths := testPaths(t, dir)
			source := newLegacyStore(t, dir, shape)
			carried := columnsOf(t, source, "tasks")
			before := dumpRows(t, source, "tasks", "id", carried)
			beforeSequence := taskSequence(t, source)
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

			// Every task row, every column, whatever order the columns are in.
			if after := dumpRows(t, source, "tasks", "id", carried); after != before {
				t.Fatalf("task rows changed:\n--- before ---\n%s\n--- after ---\n%s", before, after)
			}
			if got := taskSequence(t, source); got != beforeSequence || got != retiredSequence {
				t.Fatalf("task id sequence = %d, was %d, want %d", got, beforeSequence, retiredSequence)
			}

			if shape.messages {
				if len(report.PreservedLegacyTables) != 1 || report.PreservedLegacyTables[0] != "legacy_messages" {
					t.Fatalf("preserved legacy tables = %v", report.PreservedLegacyTables)
				}
				var kept int
				if err := scan(t, source, "SELECT count(*) FROM legacy_messages", &kept); err != nil {
					t.Fatal(err)
				}
				if kept != legacyMessageCount {
					t.Fatalf("retired messages kept = %d, want %d", kept, legacyMessageCount)
				}
				var native int
				if err := scan(t, source, "SELECT count(*) FROM messages", &native); err != nil {
					t.Fatal(err)
				}
				if native != 0 {
					t.Fatalf("native messages table is not empty: %d rows", native)
				}
			} else if len(report.PreservedLegacyTables) != 0 {
				t.Fatalf("preserved legacy tables = %v on a store with none", report.PreservedLegacyTables)
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
			if converted, expected := tableShape(t, source, "tasks"), tableShape(t, fresh, "tasks"); converted != expected {
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
		})
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
		source := newLegacyStore(t, dir, duplicateVersionShape())
		before := fileHash(t, source)
		_, err := brokerstate.Convert(t.Context(), brokerstate.NewConversionConfig(paths), &censusFixture{}, statetest.Process{}, broker.UpgradeDomain)
		if err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("convert duplicate versions = %v, want a duplicate-row error", err)
		}
		if fileHash(t, source) != before {
			t.Fatal("refused conversion changed the source")
		}
		mustNotExist(t, paths.SnapshotDir)

		// The operator sees the reason before meeting the refusal.
		view, err := brokerstate.Inspect(t.Context(), brokerstate.NewConversionConfig(paths), paths)
		if err != nil {
			t.Fatal(err)
		}
		if view.VersionRows != 2 {
			t.Fatalf("inspection reports %d version rows", view.VersionRows)
		}
	})

	t.Run("unrecognized table", func(t *testing.T) {
		dir := t.TempDir()
		paths := testPaths(t, dir)
		source := newLegacyStore(t, dir, fieldShapes()[1])
		db := openDB(t, source, "rw")
		exec(t, db, "CREATE TABLE watches (id INTEGER PRIMARY KEY, pattern TEXT)")
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		before := fileHash(t, source)
		_, err := brokerstate.Convert(t.Context(), brokerstate.NewConversionConfig(paths), &censusFixture{}, statetest.Process{}, broker.UpgradeDomain)
		if err == nil || !strings.Contains(err.Error(), "watches") {
			t.Fatalf("convert unrecognized store = %v, want a refusal naming the table", err)
		}
		if fileHash(t, source) != before {
			t.Fatal("refused conversion changed the source")
		}
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
		target := newLegacyStore(t, t.TempDir(), fieldShapes()[1])
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
	for _, shape := range fieldShapes() {
		t.Run(shape.name, func(t *testing.T) {
			t.Run("writer present", func(t *testing.T) {
				dir := t.TempDir()
				paths := testPaths(t, dir)
				source := newLegacyStore(t, dir, shape)
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
				source := newLegacyStore(t, dir, shape)
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
		})
	}
}

// TestConvertIsAtomicOnFailure fails the census re-check that runs immediately
// before the transaction: the snapshot is already taken, and the source must
// still be untouched.
func TestConvertIsAtomicOnFailure(t *testing.T) {
	for _, shape := range fieldShapes() {
		t.Run(shape.name, func(t *testing.T) {
			dir := t.TempDir()
			paths := testPaths(t, dir)
			source := newLegacyStore(t, dir, shape)
			before := fileHash(t, source)
			carried := columnsOf(t, source, "tasks")
			rows := dumpRows(t, source, "tasks", "id", carried)
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
			var version int
			if err := scan(t, snapshot, "SELECT version FROM schema_version", &version); err != nil {
				t.Fatal(err)
			}
			if version != config.LegacySchemaVersion {
				t.Fatalf("snapshot version=%d", version)
			}
			if got := dumpRows(t, snapshot, "tasks", "id", carried); got != rows {
				t.Fatalf("snapshot task rows differ from the source:\n--- source ---\n%s\n--- snapshot ---\n%s", rows, got)
			}
		})
	}
}

// TestRollbackRestoresSnapshotWhilePrepared proves the escape hatch works while
// the store is prepared and closes once it is active.
func TestRollbackRestoresSnapshotWhilePrepared(t *testing.T) {
	for _, shape := range fieldShapes() {
		t.Run(shape.name, func(t *testing.T) {
			dir := t.TempDir()
			paths := testPaths(t, dir)
			source := newLegacyStore(t, dir, shape)
			cfg := brokerstate.NewConversionConfig(paths)
			carried := columnsOf(t, source, "tasks")
			before := dumpRows(t, source, "tasks", "id", carried)
			beforeSequence := taskSequence(t, source)

			// The retired broker's store is group and world readable, and a
			// restore must not quietly tighten that.
			if err := os.Chmod(source, 0644); err != nil {
				t.Fatal(err)
			}
			converted, err := brokerstate.Convert(t.Context(), cfg, &censusFixture{}, statetest.Process{}, broker.UpgradeDomain)
			if err != nil {
				t.Fatal(err)
			}
			// A restore interrupted before its rename leaves this file behind.
			// It must not block every later rollback.
			staged := source + ".restoring"
			if err := os.WriteFile(staged, []byte("interrupted"), 0600); err != nil {
				t.Fatal(err)
			}
			report, err := brokerstate.Rollback(t.Context(), cfg, &censusFixture{})
			if err != nil {
				t.Fatalf("rollback: %v", err)
			}
			if report.Snapshot != converted.Snapshot || report.FromVersion != config.NativeSchemaVersion ||
				report.ToVersion != config.LegacySchemaVersion || report.Tasks != legacyTaskCount {
				t.Fatalf("rollback report = %+v", report)
			}
			mustHaveNoSidecars(t, source)
			mustNotExist(t, staged)
			info, err := os.Lstat(source)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0644 {
				t.Fatalf("restored store permissions = %v, want the ones it replaced", info.Mode().Perm())
			}
			// A snapshot is written by VACUUM INTO, so a restored store carries
			// SQLite's default journal mode until a broker opens it again.
			var journal string
			if err := scan(t, source, "PRAGMA journal_mode", &journal); err != nil {
				t.Fatal(err)
			}
			if journal == config.CanonicalJournalMode {
				t.Fatal("restored store kept the native journal mode")
			}
			var version, restored int
			if err := scan(t, source, `SELECT (SELECT count(*) FROM schema_version), (SELECT version FROM schema_version)`,
				&restored, &version); err != nil {
				t.Fatal(err)
			}
			if restored != 1 || version != config.LegacySchemaVersion {
				t.Fatalf("restored rows=%d version=%d", restored, version)
			}
			if after := dumpRows(t, source, "tasks", "id", carried); after != before {
				t.Fatalf("restored task rows differ:\n--- before ---\n%s\n--- after ---\n%s", before, after)
			}
			if got := taskSequence(t, source); got != beforeSequence {
				t.Fatalf("restored task sequence = %d, want %d", got, beforeSequence)
			}
			var native int
			if err := scan(t, source, "SELECT count(*) FROM sqlite_schema WHERE name IN ('cutover', 'legacy_messages')", &native); err != nil {
				t.Fatal(err)
			}
			if native != 0 {
				t.Fatal("restored store still carries converted tables")
			}
			if shape.messages {
				var kept int
				if err := scan(t, source, "SELECT count(*) FROM messages", &kept); err != nil {
					t.Fatal(err)
				}
				if kept != legacyMessageCount {
					t.Fatalf("restored legacy messages = %d, want %d", kept, legacyMessageCount)
				}
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
		})
	}
}

// TestRollbackRefusesAFreshStoreWithNoProvenance keeps the escape hatch tied to
// a conversion: a store that was never converted has nothing to restore.
func TestRollbackRefusesAFreshStoreWithNoProvenance(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(t, dir)
	source := filepath.Join(dir, config.Defaults.DBFile)
	owner, err := brokerstate.Acquire(t.Context(), config.NewOwnershipConfig(source, config.CreateStore), statetest.Process{})
	if err != nil {
		t.Fatal(err)
	}
	owner.BeginShutdown(nil)
	if err := owner.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := fileHash(t, source)
	if _, err := brokerstate.Rollback(t.Context(), brokerstate.NewConversionConfig(paths), &censusFixture{}); !errors.Is(err, brokerstate.ErrNoProvenance) {
		t.Fatalf("rollback of a never-converted store = %v, want ErrNoProvenance", err)
	}
	if fileHash(t, source) != before {
		t.Fatal("refused rollback changed the store")
	}
}

// TestAcquireRejectsLegacyStoreSoActivationIsUnreachable is the reason offline
// conversion has to exist: a legacy store cannot be owned, so nothing can
// activate one or write to it through ownership. Activation is only reachable
// through a converted store.
func TestAcquireRejectsLegacyStoreSoActivationIsUnreachable(t *testing.T) {
	dir := t.TempDir()
	source := newLegacyStore(t, dir, fieldShapes()[1])
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

// TestInspectIsReadOnly proves the operator's view creates and changes nothing,
// including on the WAL stores that exist in the field: a read that materializes
// SQLite's journal sidecars must leave none behind.
func TestInspectIsReadOnly(t *testing.T) {
	for _, shape := range fieldShapes() {
		t.Run(shape.name, func(t *testing.T) {
			dir := t.TempDir()
			paths := testPaths(t, dir)
			source := newLegacyStore(t, dir, shape)
			if err := os.WriteFile(paths.LegacyPID, []byte("1234\n"), 0600); err != nil {
				t.Fatal(err)
			}
			before := fileHash(t, source)
			cfg := brokerstate.NewConversionConfig(paths)

			view, err := brokerstate.Inspect(t.Context(), cfg, paths)
			if err != nil {
				t.Fatalf("inspect legacy store: %v", err)
			}
			if !view.Exists || view.Database != source || view.SchemaVersion != config.LegacySchemaVersion || view.VersionRows != 1 {
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
			mustHaveNoSidecars(t, source)

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
			converted := fileHash(t, source)
			after, err := brokerstate.Inspect(t.Context(), cfg, paths)
			if err != nil {
				t.Fatalf("inspect converted store: %v", err)
			}
			if after.SchemaVersion != config.NativeSchemaVersion || after.VersionRows != 1 || after.Cutover != "prepared" {
				t.Fatalf("converted inspection = %+v", after)
			}
			if after.Provenance == nil || after.Provenance.SourceSchema != config.LegacySchemaVersion ||
				after.Provenance.Snapshot != report.Snapshot || after.Provenance.ConvertedAt == "" {
				t.Fatalf("converted provenance = %+v", after.Provenance)
			}
			if len(after.Snapshots) != 1 || after.Snapshots[0] != report.Snapshot {
				t.Fatalf("snapshot listing = %v", after.Snapshots)
			}
			if fileHash(t, source) != converted {
				t.Fatal("inspection changed the converted database")
			}
			mustHaveNoSidecars(t, source)
		})
	}
}

// TestUpgradeFromV1MatchesFreshSchema keeps the upgrade honest: the tasks table
// a converted store carries must be the table Schema() creates, not an
// approximation of it, for both shapes that exist in the field.
func TestUpgradeFromV1MatchesFreshSchema(t *testing.T) {
	fresh := newNativeStore(t, t.TempDir())
	want := tableShape(t, fresh, "tasks")
	for _, shape := range fieldShapes() {
		t.Run(shape.name, func(t *testing.T) {
			dir := t.TempDir()
			paths := testPaths(t, dir)
			source := newLegacyStore(t, dir, shape)
			if _, err := brokerstate.Convert(t.Context(), brokerstate.NewConversionConfig(paths), &censusFixture{}, statetest.Process{}, broker.UpgradeDomain); err != nil {
				t.Fatal(err)
			}
			if got := tableShape(t, source, "tasks"); got != want {
				t.Fatalf("upgraded schema differs from Schema():\n--- upgraded ---\n%s\n--- fresh ---\n%s", got, want)
			}
			var withTTL, kept int
			if err := scan(t, source, "SELECT count(*), coalesce(sum(ttl IS NOT NULL), 0) FROM tasks", &kept, &withTTL); err != nil {
				t.Fatal(err)
			}
			expected := 0
			if shape.ttl {
				expected = 2
			}
			if kept != legacyTaskCount || withTTL != expected {
				t.Fatalf("converted rows=%d with ttl=%d, want %d with %d", kept, withTTL, legacyTaskCount, expected)
			}
			// The sequence survives, so no retired id is ever handed out again.
			if got := taskSequence(t, source); got != retiredSequence {
				t.Fatalf("task id sequence = %d, want %d", got, retiredSequence)
			}
		})
	}
}
