package runtime

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
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
	if watches[0].ExpiresAt.IsZero() {
		t.Fatal("expected hook watch to receive expiration")
	}
	if watches[1].ProjectID != "proj-b" || watches[1].AgentName != "agent-2" || watches[1].Source != "cli" {
		t.Fatalf("second watch = %+v, want proj-b/agent-2", watches[1])
	}
	if watches[1].ExpiresAt.IsZero() {
		t.Fatal("expected cli watch to receive expiration")
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

func TestStore_ExplicitWatchIsDurableWhileSessionWatchesExpire(t *testing.T) {
	store := newTestStore(t)

	if err := store.UpsertWatch(Watch{ProjectID: "proj-a", AgentName: "agent-1", Source: "explicit"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertWatch(Watch{
		ProjectID: "proj-a",
		AgentName: "agent-2",
		Source:    "claude-session-start",
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	pruned, err := store.PruneExpiredWatches(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 1 {
		t.Fatalf("pruned watches = %d, want 1", pruned)
	}

	watches, err := store.ListWatches()
	if err != nil {
		t.Fatal(err)
	}
	if len(watches) != 1 {
		t.Fatalf("watch count = %d, want 1", len(watches))
	}
	if watches[0].AgentName != "agent-1" || !watches[0].ExpiresAt.IsZero() {
		t.Fatalf("remaining watch = %+v, want durable explicit watch", watches[0])
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

func TestStore_AddRecordAllowsPendingNotifications(t *testing.T) {
	store := newTestStore(t)

	rec := DeliveryRecord{
		ProjectID:  "proj-a",
		AgentName:  "agent-1",
		MessageID:  88,
		FromName:   "orchestrator",
		Body:       "pending",
		SentAt:     time.Unix(1, 0).UTC(),
		ReceivedAt: time.Unix(2, 0).UTC(),
	}
	if err := store.AddRecord(rec); err != nil {
		t.Fatal(err)
	}

	pending, err := store.PendingNotifications("proj-a", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending count = %d, want 1", len(pending))
	}
	if pending[0].MessageID != 88 {
		t.Fatalf("pending message id = %d, want 88", pending[0].MessageID)
	}
}

func TestStore_AddRecordUpsertPreservesLifecycleTimestamps(t *testing.T) {
	store := newTestStore(t)

	base := DeliveryRecord{
		ProjectID:   "proj-a",
		AgentName:   "agent-1",
		MessageID:   99,
		FromName:    "orchestrator",
		Body:        "base",
		SentAt:      time.Unix(1, 0).UTC(),
		ReceivedAt:  time.Unix(2, 0).UTC(),
		NotifiedAt:  time.Unix(3, 0).UTC(),
		SurfacedAt:  time.Unix(4, 0).UTC(),
		DismissedAt: time.Unix(5, 0).UTC(),
	}
	if err := store.AddRecord(base); err != nil {
		t.Fatal(err)
	}

	updated := base
	updated.Body = "updated"
	updated.NotifiedAt = time.Time{}
	updated.SurfacedAt = time.Time{}
	updated.DismissedAt = time.Time{}
	if err := store.AddRecord(updated); err != nil {
		t.Fatal(err)
	}

	rec, err := store.GetRecord("proj-a", "agent-1", 99)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Body != "updated" {
		t.Fatalf("body = %q, want updated", rec.Body)
	}
	if rec.NotifiedAt.IsZero() {
		t.Fatal("notified_at was cleared by duplicate upsert")
	}
	if rec.SurfacedAt.IsZero() {
		t.Fatal("surfaced_at was cleared by duplicate upsert")
	}
	if rec.DismissedAt.IsZero() {
		t.Fatal("dismissed_at was cleared by duplicate upsert")
	}
}

func TestStore_ConcurrentConnectionsShareState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime.db")

	writer, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	reader, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	var wg sync.WaitGroup
	errCh := make(chan error, 8)
	start := make(chan struct{})

	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 10; i++ {
			if err := writer.UpsertWatch(Watch{
				ProjectID: "proj-a",
				AgentName: "agent-1",
				Source:    "writer",
			}); err != nil {
				errCh <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 10; i++ {
			if err := reader.UpsertWatch(Watch{
				ProjectID: "proj-b",
				AgentName: "agent-2",
				Source:    "reader",
			}); err != nil {
				errCh <- err
				return
			}
		}
	}()

	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	watches, err := writer.ListWatches()
	if err != nil {
		t.Fatal(err)
	}
	if len(watches) != 2 {
		t.Fatalf("watch count = %d, want 2", len(watches))
	}

	rec := DeliveryRecord{
		ProjectID:  "proj-a",
		AgentName:  "agent-1",
		MessageID:  1,
		FromName:   "orchestrator",
		Body:       "concurrent",
		SentAt:     time.Unix(10, 0).UTC(),
		ReceivedAt: time.Unix(11, 0).UTC(),
	}
	if err := writer.AddRecord(rec); err != nil {
		t.Fatal(err)
	}

	pending, err := reader.PendingNotifications("proj-a", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending count = %d, want 1", len(pending))
	}
}

func TestStore_PruneDeliveryRecordsRetainsUnresolvedRecords(t *testing.T) {
	store := newTestStore(t)

	oldResolved := DeliveryRecord{
		ProjectID:   "proj-a",
		AgentName:   "agent-1",
		MessageID:   1,
		FromName:    "orchestrator",
		Body:        "resolved",
		SentAt:      time.Unix(1, 0).UTC(),
		ReceivedAt:  time.Now().UTC().Add(-40 * 24 * time.Hour),
		NotifiedAt:  time.Unix(3, 0).UTC(),
		SurfacedAt:  time.Unix(4, 0).UTC(),
		DismissedAt: time.Unix(5, 0).UTC(),
	}
	oldUnresolved := DeliveryRecord{
		ProjectID:  "proj-a",
		AgentName:  "agent-1",
		MessageID:  2,
		FromName:   "orchestrator",
		Body:       "unresolved",
		SentAt:     time.Unix(1, 0).UTC(),
		ReceivedAt: time.Now().UTC().Add(-40 * 24 * time.Hour),
	}
	if err := store.AddRecord(oldResolved); err != nil {
		t.Fatal(err)
	}
	if err := store.AddRecord(oldUnresolved); err != nil {
		t.Fatal(err)
	}

	pruned, err := store.PruneDeliveryRecords(time.Now().UTC().Add(-30 * 24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 1 {
		t.Fatalf("pruned delivery records = %d, want 1", pruned)
	}

	if _, err := store.GetRecord("proj-a", "agent-1", 1); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("resolved record err = %v, want ErrRecordNotFound", err)
	}
	if _, err := store.GetRecord("proj-a", "agent-1", 2); err != nil {
		t.Fatalf("unresolved record should remain, got %v", err)
	}
}

func TestStore_MigrateNormalizesLegacyNullNotifiedAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE delivery_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id TEXT NOT NULL,
			agent_name TEXT NOT NULL,
			message_id INTEGER NOT NULL,
			from_name TEXT NOT NULL,
			body TEXT NOT NULL,
			sent_at TEXT NOT NULL,
			received_at TEXT NOT NULL,
			notified_at TEXT,
			surfaced_at TEXT,
			dismissed_at TEXT,
			UNIQUE (project_id, agent_name, message_id)
		);
		INSERT INTO delivery_records (
			project_id, agent_name, message_id, from_name, body, sent_at, received_at, notified_at
		) VALUES ('proj-a', 'agent-1', 1, 'planner', 'legacy', ?, ?, NULL);
	`, time.Unix(1, 0).UTC().Format(time.RFC3339Nano), time.Unix(2, 0).UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	pending, err := store.PendingNotifications("proj-a", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending count = %d, want 1", len(pending))
	}

	var notifiedAt string
	if err := store.db.QueryRow(`SELECT notified_at FROM delivery_records WHERE project_id = ? AND agent_name = ? AND message_id = ?`, "proj-a", "agent-1", 1).Scan(&notifiedAt); err != nil {
		t.Fatal(err)
	}
	if notifiedAt != "" {
		t.Fatalf("notified_at = %q, want canonical empty string", notifiedAt)
	}
}
