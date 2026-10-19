package messages

// Schema replaces name-addressed delivery. There is one message per request,
// one immutable attempt per message, and at most one barrier per recipient.
// Status is a projection of evidence and disposition, never a delivery flag.
const Schema = `
CREATE TABLE enrollments (
 id TEXT PRIMARY KEY,
 provider TEXT NOT NULL,
 conversation TEXT NOT NULL,
 endpoint TEXT NOT NULL,
 label TEXT NOT NULL,
 credential_hash BLOB NOT NULL UNIQUE,
 state TEXT NOT NULL CHECK(state IN ('pending','bound','disconnected','failed','retired')),
 pending_until INTEGER NOT NULL,
 verified_generation INTEGER,
 evidence TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX enrollment_conversation ON enrollments(provider, conversation) WHERE state != 'retired';
CREATE UNIQUE INDEX enrollment_label ON enrollments(label) WHERE state != 'retired';
CREATE TABLE conversations (
 id TEXT PRIMARY KEY,
 deadline INTEGER NOT NULL,
 remaining_hops INTEGER NOT NULL CHECK(remaining_hops >= 0),
 stopped_reason TEXT NOT NULL DEFAULT ''
);
CREATE TABLE messages (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE,
 sender TEXT NOT NULL REFERENCES enrollments(id),
 recipient TEXT NOT NULL REFERENCES enrollments(id),
 conversation_id TEXT NOT NULL REFERENCES conversations(id),
 reply_to TEXT REFERENCES messages(id),
 request_id TEXT NOT NULL,
 request_hash BLOB NOT NULL,
 body TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 UNIQUE(sender, request_id)
);
CREATE INDEX messages_recipient_sequence ON messages(recipient, sequence);
CREATE TABLE attempts (
 id TEXT PRIMARY KEY,
 message_id TEXT NOT NULL UNIQUE REFERENCES messages(id),
 owner_id TEXT NOT NULL,
 generation INTEGER NOT NULL,
 intent_at INTEGER NOT NULL
);
CREATE TABLE receipts (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 attempt_id TEXT NOT NULL REFERENCES attempts(id),
 kind TEXT NOT NULL CHECK(kind IN ('uncertain','accepted','held','not_submitted','refused','dropped','consumed')),
 detail TEXT NOT NULL,
 provider_ref TEXT NOT NULL,
 observed_at INTEGER NOT NULL,
 UNIQUE(attempt_id, kind, detail, provider_ref)
);
CREATE TABLE recipient_barrier (
 recipient TEXT PRIMARY KEY REFERENCES enrollments(id),
 attempt_id TEXT NOT NULL UNIQUE REFERENCES attempts(id)
);
CREATE VIEW message_status AS
 SELECT m.*, c.deadline, c.remaining_hops, c.stopped_reason,
 a.id AS attempt_id,
 CASE
  WHEN a.id IS NULL THEN 'stored'
  WHEN EXISTS(SELECT 1 FROM receipts r WHERE r.attempt_id=a.id AND r.kind='consumed') THEN 'consumed'
  WHEN EXISTS(SELECT 1 FROM receipts r WHERE r.attempt_id=a.id AND r.kind IN ('not_submitted','refused','dropped')) THEN 'not_retained'
  ELSE COALESCE((SELECT r.kind FROM receipts r WHERE r.attempt_id=a.id AND r.kind IN ('accepted','held') ORDER BY r.sequence DESC LIMIT 1), 'uncertain')
 END AS possession
 FROM messages m JOIN conversations c ON c.id=m.conversation_id
 LEFT JOIN attempts a ON a.message_id=m.id;
`
