package runtime

import (
	"testing"
	"time"
)

func TestMarkDismissed_Basic(t *testing.T) {
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

	// Mark as surfaced first
	if err := store.MarkSurfaced("proj-a", "agent-1", 42); err != nil {
		t.Fatal(err)
	}

	// Mark as dismissed
	if err := store.MarkDismissed("proj-a", "agent-1", 42); err != nil {
		t.Fatal(err)
	}

	// Verify dismissed_at is set
	retrieved, err := store.GetRecord("proj-a", "agent-1", 42)
	if err != nil {
		t.Fatal(err)
	}
	if retrieved.DismissedAt.IsZero() {
		t.Fatalf("dismissed_at should be set, got zero time")
	}
}

func TestMarkDismissed_DoesNotDismissUnread(t *testing.T) {
	store := newTestStore(t)

	rec := DeliveryRecord{
		ProjectID:  "proj-a",
		AgentName:  "agent-1",
		MessageID:  43,
		FromName:   "orchestrator",
		Body:       "hello",
		SentAt:     time.Unix(1, 0).UTC(),
		ReceivedAt: time.Unix(2, 0).UTC(),
		NotifiedAt: time.Unix(3, 0).UTC(),
	}
	if err := store.AddRecord(rec); err != nil {
		t.Fatal(err)
	}

	if err := store.MarkDismissed("proj-a", "agent-1", 43); err != nil {
		t.Fatal(err)
	}

	retrieved, err := store.GetRecord("proj-a", "agent-1", 43)
	if err != nil {
		t.Fatal(err)
	}
	if !retrieved.SurfacedAt.IsZero() {
		t.Fatalf("surfaced_at = %v, want zero", retrieved.SurfacedAt)
	}
	if !retrieved.DismissedAt.IsZero() {
		t.Fatalf("dismissed_at = %v, want zero", retrieved.DismissedAt)
	}

	unread, err := store.Unread("proj-a", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 1 {
		t.Fatalf("unread count = %d, want 1", len(unread))
	}
}

func TestMarkDismissed_NotFound(t *testing.T) {
	store := newTestStore(t)

	err := store.MarkDismissed("proj-a", "agent-1", 999)
	if err != ErrRecordNotFound {
		t.Fatalf("MarkDismissed missing record err = %v, want %v", err, ErrRecordNotFound)
	}
}

func TestMarkDismissed_Idempotent(t *testing.T) {
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

	// Mark dismissed twice
	if err := store.MarkDismissed("proj-a", "agent-1", 42); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDismissed("proj-a", "agent-1", 42); err != nil {
		t.Fatal(err)
	}

	// Should be dismissed exactly once with no error
	retrieved, err := store.GetRecord("proj-a", "agent-1", 42)
	if err != nil {
		t.Fatal(err)
	}
	if retrieved.DismissedAt.IsZero() {
		t.Fatalf("dismissed_at should be set after idempotent calls")
	}
}

func TestMarkDismissedBatch_Empty(t *testing.T) {
	store := newTestStore(t)

	if err := store.MarkDismissedBatch("proj-a", "agent-1", []int64{}); err != nil {
		t.Fatal(err)
	}
}

func TestMarkDismissedBatch_Atomic(t *testing.T) {
	store := newTestStore(t)

	for _, id := range []int64{41, 42, 43} {
		rec := DeliveryRecord{
			ProjectID:  "proj-a",
			AgentName:  "agent-1",
			MessageID:  id,
			FromName:   "orchestrator",
			Body:       "msg",
			SentAt:     time.Unix(1, 0).UTC(),
			ReceivedAt: time.Unix(2, 0).UTC(),
			NotifiedAt: time.Unix(3, 0).UTC(),
		}
		if err := store.AddRecord(rec); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.MarkSurfacedBatch("proj-a", "agent-1", []int64{41, 42, 43}); err != nil {
		t.Fatal(err)
	}

	if err := store.MarkDismissedBatch("proj-a", "agent-1", []int64{41, 42, 43}); err != nil {
		t.Fatal(err)
	}

	for _, id := range []int64{41, 42, 43} {
		rec, err := store.GetRecord("proj-a", "agent-1", id)
		if err != nil {
			t.Fatal(err)
		}
		if rec.DismissedAt.IsZero() {
			t.Fatalf("message %d not dismissed", id)
		}
	}
}

func TestMarkSurfacedAndDismissBatch_ConsumesUnreadAtomically(t *testing.T) {
	store := newTestStore(t)

	rec := DeliveryRecord{
		ProjectID:  "proj-a",
		AgentName:  "agent-1",
		MessageID:  101,
		FromName:   "planner",
		Body:       "do work",
		SentAt:     time.Unix(1, 0).UTC(),
		ReceivedAt: time.Unix(2, 0).UTC(),
	}
	if err := store.AddRecord(rec); err != nil {
		t.Fatal(err)
	}

	// Simulate Bootstrap: unread → surfaced+dismissed in one authoritative transition.
	unread, err := store.Unread("proj-a", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 1 {
		t.Fatalf("unread count = %d, want 1", len(unread))
	}

	messageIDs := make([]int64, 0, len(unread))
	for _, rec := range unread {
		messageIDs = append(messageIDs, rec.MessageID)
	}

	if err := store.MarkSurfacedAndDismissBatch("proj-a", "agent-1", messageIDs); err != nil {
		t.Fatal(err)
	}

	// Record should be surfaced and dismissed
	final, err := store.GetRecord("proj-a", "agent-1", 101)
	if err != nil {
		t.Fatal(err)
	}
	if final.SurfacedAt.IsZero() {
		t.Fatalf("surfaced_at should be set")
	}
	if final.DismissedAt.IsZero() {
		t.Fatalf("dismissed_at should be set")
	}
}

func TestDismissAllSurfaced_Zero(t *testing.T) {
	store := newTestStore(t)

	rec := DeliveryRecord{
		ProjectID:  "proj-a",
		AgentName:  "agent-1",
		MessageID:  44,
		FromName:   "planner",
		Body:       "still unread",
		SentAt:     time.Unix(1, 0).UTC(),
		ReceivedAt: time.Unix(2, 0).UTC(),
	}
	if err := store.AddRecord(rec); err != nil {
		t.Fatal(err)
	}

	count, err := store.DismissAllSurfaced("proj-a", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("dismissed count = %d, want 0", count)
	}

	retrieved, err := store.GetRecord("proj-a", "agent-1", 44)
	if err != nil {
		t.Fatal(err)
	}
	if !retrieved.DismissedAt.IsZero() {
		t.Fatalf("dismissed_at = %v, want zero", retrieved.DismissedAt)
	}
}
