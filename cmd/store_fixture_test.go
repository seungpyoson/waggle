package cmd

import (
	"database/sql"
	"fmt"
	"net/url"
	"testing"

	"github.com/seungpyoson/waggle/internal/brokerstate/statetest"
	"github.com/seungpyoson/waggle/internal/config"
	_ "modernc.org/sqlite"
)

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
//
// The statements are the retired broker's own, frozen in
// internal/brokerstate/statetest, which is also where the conversion tests take
// them from. The connection stays here: the architecture guard admits exactly
// one non-test site that opens the canonical database, and it belongs to
// internal/brokerstate.
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
	for _, statement := range statetest.LegacyStatements(true, true) {
		run(statement)
	}
	run("INSERT INTO schema_version(version) VALUES (?)", config.LegacySchemaVersion)
	for i := 1; i <= legacyTaskCount; i++ {
		run(`INSERT INTO tasks(idempotency_key, type, tags, payload, priority, state, blocked,
			depends_on, claim_token, claimed_by, claimed_at, lease_expires_at, retry_count, result, failure_reason, ttl)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("key-%d", i), "review", `["a","b"]`, fmt.Sprintf(`{"step":%d}`, i), i,
			"claimed", i%2, "[]", fmt.Sprintf("token-%d", i), "worker", "2026-01-01T00:00:00Z",
			"2026-01-01T00:05:00Z", i, `{"ok":true}`, "", 60*i)
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
