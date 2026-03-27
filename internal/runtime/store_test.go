package runtime

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "runtime.db")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func TestStore_WatchPersistenceAndDeduplication(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime.db")

	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.UpsertWatch(Watch{ProjectID: "proj-a", AgentName: "agent-1", Source: "cli"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertWatch(Watch{ProjectID: "proj-a", AgentName: "agent-1", Source: "hook"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertWatch(Watch{ProjectID: "proj-b", AgentName: "agent-2", Source: "cli"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = reopened.Close()
	})

	watches, err := reopened.ListWatches()
	if err != nil {
		t.Fatal(err)
	}
	if len(watches) != 2 {
		t.Fatalf("watch count = %d, want 2", len(watches))
	}
	if watches[0].ProjectID != "proj-a" || watches[0].AgentName != "agent-1" || watches[0].Source != "hook" {
		t.Fatalf("first watch = %+v, want updated watch for proj-a/agent-1", watches[0])
	}
	if watches[1].ProjectID != "proj-b" || watches[1].AgentName != "agent-2" || watches[1].Source != "cli" {
		t.Fatalf("second watch = %+v, want proj-b/agent-2", watches[1])
	}
}

func TestStore_RemoveWatch(t *testing.T) {
	store := newTestStore(t)

	if err := store.UpsertWatch(Watch{ProjectID: "proj-a", AgentName: "agent-1", Source: "cli"}); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveWatch("proj-a", "agent-1"); err != nil {
		t.Fatal(err)
	}

	watches, err := store.ListWatches()
	if err != nil {
		t.Fatal(err)
	}
	if len(watches) != 0 {
		t.Fatalf("watch count = %d, want 0", len(watches))
	}
}

func TestStore_RecordPersistenceUnreadAndSurfaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime.db")

	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	rec1 := DeliveryRecord{
		ProjectID:  "proj-a",
		AgentName:  "agent-1",
		MessageID:  10,
		FromName:   "orchestrator",
		Body:       "first",
		SentAt:     time.Unix(1, 0).UTC(),
		ReceivedAt: time.Unix(2, 0).UTC(),
		NotifiedAt: time.Unix(3, 0).UTC(),
	}
	rec2 := DeliveryRecord{
		ProjectID:  "proj-a",
		AgentName:  "agent-1",
		MessageID:  11,
		FromName:   "orchestrator",
		Body:       "second",
		SentAt:     time.Unix(4, 0).UTC(),
		ReceivedAt: time.Unix(5, 0).UTC(),
		NotifiedAt: time.Unix(6, 0).UTC(),
	}
	recOther := DeliveryRecord{
		ProjectID:  "proj-b",
		AgentName:  "agent-9",
		MessageID:  99,
		FromName:   "orchestrator",
		Body:       "other",
		SentAt:     time.Unix(7, 0).UTC(),
		ReceivedAt: time.Unix(8, 0).UTC(),
		NotifiedAt: time.Unix(9, 0).UTC(),
	}

	if err := store.AddRecord(rec1); err != nil {
		t.Fatal(err)
	}
	if err := store.AddRecord(rec2); err != nil {
		t.Fatal(err)
	}
	if err := store.AddRecord(recOther); err != nil {
		t.Fatal(err)
	}

	unread, err := store.Unread("proj-a", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 2 {
		t.Fatalf("unread count = %d, want 2", len(unread))
	}
	if unread[0].MessageID != 10 || unread[1].MessageID != 11 {
		t.Fatalf("unexpected unread order: %+v", unread)
	}

	if err := store.MarkSurfaced("proj-a", "agent-1", 10); err != nil {
		t.Fatal(err)
	}

	unread, err = store.Unread("proj-a", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 1 {
		t.Fatalf("unread count = %d, want 1", len(unread))
	}
	if unread[0].MessageID != 11 {
		t.Fatalf("remaining unread message = %d, want 11", unread[0].MessageID)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = reopened.Close()
	})

	unread, err = reopened.Unread("proj-a", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 1 || unread[0].MessageID != 11 {
		t.Fatalf("persisted unread = %+v, want only message 11", unread)
	}
}

func TestStore_MarkSurfacedIsIdempotent(t *testing.T) {
	store := newTestStore(t)

	rec := DeliveryRecord{
		ProjectID:  "proj-a",
		AgentName:  "agent-1",
		MessageID:  42,
		FromName:   "orchestrator",
		Body:       "hello",
		SentAt:     time.Unix(1, 0).UTC(),
		ReceivedAt: time.Unix(2, 0).UTC(),
		NotifiedAt: time.Unix(3, 0).UTC(),
	}
	if err := store.AddRecord(rec); err != nil {
		t.Fatal(err)
	}

	if err := store.MarkSurfaced("proj-a", "agent-1", 42); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkSurfaced("proj-a", "agent-1", 42); err != nil {
		t.Fatal(err)
	}

	unread, err := store.Unread("proj-a", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 0 {
		t.Fatalf("unread count = %d, want 0", len(unread))
	}
}

func TestStore_DismissedRecordsStayDismissedAndUnreadExcludesThem(t *testing.T) {
	store := newTestStore(t)

	base := DeliveryRecord{
		ProjectID:   "proj-a",
		AgentName:   "agent-1",
		MessageID:   77,
		FromName:    "orchestrator",
		Body:        "initial",
		SentAt:      time.Unix(1, 0).UTC(),
		ReceivedAt:  time.Unix(2, 0).UTC(),
		NotifiedAt:  time.Unix(3, 0).UTC(),
		DismissedAt: time.Unix(4, 0).UTC(),
	}
	if err := store.AddRecord(base); err != nil {
		t.Fatal(err)
	}

	unread, err := store.Unread("proj-a", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 0 {
		t.Fatalf("unread count = %d, want 0 for dismissed record", len(unread))
	}

	updated := base
	updated.Body = "updated"
	updated.DismissedAt = time.Time{}
	if err := store.AddRecord(updated); err != nil {
		t.Fatal(err)
	}

	var dismissedAt string
	var body string
	err = store.db.QueryRow(
		`SELECT dismissed_at, body FROM delivery_records WHERE project_id = ? AND agent_name = ? AND message_id = ?`,
		"proj-a", "agent-1", 77,
	).Scan(&dismissedAt, &body)
	if err != nil {
		t.Fatal(err)
	}
	if dismissedAt == "" {
		t.Fatal("dismissed_at was cleared by duplicate upsert")
	}
	if body != "updated" {
		t.Fatalf("body = %q, want updated", body)
	}

	unread, err = store.Unread("proj-a", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 0 {
		t.Fatalf("unread count = %d, want 0 after duplicate upsert", len(unread))
	}
}
