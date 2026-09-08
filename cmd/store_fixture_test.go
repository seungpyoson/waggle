package cmd

import (
	"database/sql"
	"fmt"
	"net/url"
	"testing"

	"github.com/seungpyoson/waggle/internal/config"
	_ "modernc.org/sqlite"
)

// The frozen schema-v1 store, as the retired broker actually left it on disk:
// its CREATE text plus the ALTERs it ran on every open. All of this text is
// history and must never be edited to match current code.
//
// It lives in a test file because the architecture guard admits exactly one
// non-test site that opens the canonical database, which belongs to
// internal/brokerstate. internal/brokerstate/convert_test.go carries the same
// frozen statements for the conversion tests; the two copies are the price of
// keeping fixture SQL out of the shipped binary, and they must move together.
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

// The rows the fixture writes, so a test can assert that a conversion carried
// exactly what the store held.
const (
	legacyTaskCount    = 3
	legacyMessageCount = 2
)

// writeLegacyStore writes a schema-v1 store at path, in the shape a broker that
// had been running for a while left behind: WAL journal, the tasks table with
// the ttl column its runtime migration added, the retired name-addressed
// messages table, and exactly one version row. It is the store
// `waggle store convert` exists to upgrade.
func writeLegacyStore(t *testing.T, path string) {
	t.Helper()
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "rwc")
	q.Set("_pragma", fmt.Sprintf("busy_timeout(%d)", config.Defaults.BusyTimeout.Milliseconds()))
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(config.CanonicalConnections)
	run := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatalf("legacy fixture: %v", err)
		}
	}
	var journal string
	if err := db.QueryRow("PRAGMA journal_mode=" + config.CanonicalJournalMode).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if journal != config.CanonicalJournalMode {
		t.Fatalf("legacy fixture journal mode = %q", journal)
	}
	run(fmt.Sprintf(legacyTasksSchemaV1, int(config.Defaults.LeaseDuration.Seconds()), config.Defaults.MaxRetries))
	run(legacyTasksTTLMigration)
	run("INSERT INTO schema_version(version) VALUES (?)", config.LegacySchemaVersion)
	for i := 1; i <= legacyTaskCount; i++ {
		run(`INSERT INTO tasks(idempotency_key, type, tags, payload, priority, state, blocked,
			depends_on, claim_token, claimed_by, claimed_at, lease_expires_at, retry_count, result, failure_reason, ttl)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("key-%d", i), "review", `["a","b"]`, fmt.Sprintf(`{"step":%d}`, i), i,
			"claimed", i%2, "[]", fmt.Sprintf("token-%d", i), "worker", "2026-01-01T00:00:00Z",
			"2026-01-01T00:05:00Z", i, `{"ok":true}`, "", 60*i)
	}
	run(legacyMessagesSchemaV1)
	for _, migration := range legacyMessagesMigrations {
		run(migration)
	}
	for i := 1; i <= legacyMessageCount; i++ {
		run(`INSERT INTO messages(from_name, to_name, body, state, created_at, pushed_at, seen_at, acked_at, priority, ttl)
			VALUES (?, ?, ?, 'queued', '2026-01-01T00:00:00Z', NULL, NULL, NULL, 'normal', ?)`,
			"alice", "bob", fmt.Sprintf("body %d", i), 60*i)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}
