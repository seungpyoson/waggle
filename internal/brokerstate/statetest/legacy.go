package statetest

import (
	"fmt"

	"github.com/seungpyoson/waggle/internal/config"
)

// The frozen schema-v1 store. A real legacy store is not the retired broker's
// CREATE text alone: that broker migrated the store on every open, so the shape
// in the field also carries the runtime ALTERs below.
//
// All of this text is history. It must never be edited to match current code,
// and a change to it is only ever a correction of what the retired broker
// actually wrote. Byte-identity with the sources named below is a review
// obligation, not something a test can check without shelling out to git; what
// the tests do check is that a store built from this text converts into a
// schema structurally identical to a fresh native one.
//
// This package is the one home for the text: it is a non-test package that
// opens no database, so both internal/brokerstate/convert_test.go and
// cmd/store_fixture_test.go build their fixtures from here without adding a
// second canonical open site to the architecture guard.

// LegacyTasksSchemaV1 is main (commit 5fac053) internal/tasks/store.go:128-154,
// verbatim, including its two format verbs for the lease and retry defaults.
const LegacyTasksSchemaV1 = `
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

// LegacyTasksTTLMigration is main:internal/tasks/store.go:193, run on every open
// by migrateTaskSchema, so ttl is the last column of any store that broker ran.
const LegacyTasksTTLMigration = `ALTER TABLE tasks ADD COLUMN ttl INTEGER`

// LegacyMessagesSchemaV1 is main:internal/messages/store.go:42-50 and its index
// at line 58, created by NewStore on every open of the old broker.
const LegacyMessagesSchemaV1 = `
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

// LegacyMessagesMigrations is main:internal/messages/store.go:79-82.
var LegacyMessagesMigrations = []string{
	`ALTER TABLE messages ADD COLUMN seen_at TEXT`,
	`ALTER TABLE messages ADD COLUMN acked_at TEXT`,
	`ALTER TABLE messages ADD COLUMN priority TEXT NOT NULL DEFAULT 'normal'`,
	`ALTER TABLE messages ADD COLUMN ttl INTEGER`,
}

// LegacyStatements is the schema of one legacy store shape, in the order the
// retired broker ran the statements: the tasks schema it created, the task
// migration it applied on every open once it carried one, and the message store
// it created the first time a message was sent. withTTL and withMessages select
// which of those a given store had reached; rows are the caller's to insert,
// because the caller owns the connection and this package opens none.
func LegacyStatements(withTTL, withMessages bool) []string {
	statements := []string{
		fmt.Sprintf(LegacyTasksSchemaV1, int(config.Defaults.LeaseDuration.Seconds()), config.Defaults.MaxRetries),
	}
	if withTTL {
		statements = append(statements, LegacyTasksTTLMigration)
	}
	if withMessages {
		statements = append(statements, LegacyMessagesSchemaV1)
		statements = append(statements, LegacyMessagesMigrations...)
	}
	return statements
}
