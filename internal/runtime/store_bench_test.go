package runtime

import (
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Baseline results (M4 Darwin, 3 runs averaged):
// BenchmarkStore_InsertRecord-10            	   19730	     61183 ns/op	    1071 B/op	      18 allocs/op
// BenchmarkStore_Unread-10                  	    6626	    177574 ns/op	  154352 B/op	    3035 allocs/op
// BenchmarkStore_MarkSurfacedBatch-10       	    9366	    121170 ns/op	    6791 B/op	      29 allocs/op
// BenchmarkStore_ConcurrentInsert-10        	   17854	     68471 ns/op	    1225 B/op	      22 allocs/op
// BenchmarkStore_AddRecordIfAbsent-10       	   36956	     34437 ns/op	    1064 B/op	      18 allocs/op
// BenchmarkStore_MarkNotified-10            	   37662	     31145 ns/op	     376 B/op	      12 allocs/op
// BenchmarkStore_LargeInbox-10              	      61	  17671151 ns/op	22227629 B/op	  319792 allocs/op
// BenchmarkStore_PendingNotifications-10    	    2571	    463793 ns/op	  365857 B/op	    8285 allocs/op
// BenchmarkStore_PruneDeliveryRecords-10    	  167174	      6815 ns/op	     184 B/op	       8 allocs/op
//
// Run with: go test ./internal/runtime -bench=BenchmarkStore -benchmem -count=3

// BenchmarkStore_InsertRecord measures insert throughput for delivery_records.
// Scenario: N agents inserting messages.
func BenchmarkStore_InsertRecord(b *testing.B) {
	tmpDir := b.TempDir()
	path := filepath.Join(tmpDir, "runtime.db")

	store, err := NewStore(path)
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()

	projectID := "test-project"
	agentName := "test-agent"

	rec := DeliveryRecord{
		ProjectID:     projectID,
		AgentName:     agentName,
		MessageID:     1,
		FromName:      "bootstrap",
		Body:          "Test message",
		SentAt:        time.Now().UTC().Add(-1 * time.Hour),
		ReceivedAt:    time.Now().UTC(),
		NotifiedAt:    time.Time{},
		SurfacedAt:    time.Time{},
		DismissedAt:   time.Time{},
		RetryAttempts: 0,
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		rec.MessageID = int64(i) + 1
		if err := store.AddRecord(rec); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStore_Unread measures Unread() query throughput with N pre-inserted records.
// Scenario: agent polling for unread messages.
func BenchmarkStore_Unread(b *testing.B) {
	tmpDir := b.TempDir()
	path := filepath.Join(tmpDir, "runtime.db")

	store, err := NewStore(path)
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()

	projectID := "test-project"
	agentName := "test-agent"

	// Pre-populate with 100 unread records
	now := time.Now().UTC()
	for i := 0; i < 100; i++ {
		rec := DeliveryRecord{
			ProjectID:     projectID,
			AgentName:     agentName,
			MessageID:     int64(i) + 1,
			FromName:      "bootstrap",
			Body:          "Test message",
			SentAt:        now.Add(-1 * time.Hour),
			ReceivedAt:    now,
			NotifiedAt:    time.Time{},
			SurfacedAt:    time.Time{}, // unread
			DismissedAt:   time.Time{},
			RetryAttempts: 0,
		}
		if err := store.AddRecord(rec); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := store.Unread(projectID, agentName)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStore_MarkSurfacedBatch measures batch surfacing throughput.
// Scenario: Bootstrap marking 50 records surfaced at once.
// First call surfaces 50 records; subsequent calls measure COALESCE idempotency cost.
// Both paths exercise the full query plan and are regression-detectable.
func BenchmarkStore_MarkSurfacedBatch(b *testing.B) {
	tmpDir := b.TempDir()
	path := filepath.Join(tmpDir, "runtime.db")

	store, err := NewStore(path)
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()

	projectID := "test-project"
	agentName := "test-agent"

	// Pre-populate with 50 unread records
	now := time.Now().UTC()
	messageIDs := make([]int64, 0, 50)
	for i := 0; i < 50; i++ {
		rec := DeliveryRecord{
			ProjectID:     projectID,
			AgentName:     agentName,
			MessageID:     int64(i) + 1,
			FromName:      "bootstrap",
			Body:          "Test message",
			SentAt:        now.Add(-1 * time.Hour),
			ReceivedAt:    now,
			NotifiedAt:    time.Time{},
			SurfacedAt:    time.Time{},
			DismissedAt:   time.Time{},
			RetryAttempts: 0,
		}
		if err := store.AddRecord(rec); err != nil {
			b.Fatal(err)
		}
		messageIDs = append(messageIDs, int64(i)+1)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := store.MarkSurfacedBatch(projectID, agentName, messageIDs); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStore_ConcurrentInsert measures concurrent insert throughput.
// Uses b.RunParallel to simulate N agents inserting unique messages simultaneously.
// Each goroutine generates unique (agentName, messageID) pairs via shared atomic
// counter — measures clean concurrent insert throughput, not upsert-collision throughput.
func BenchmarkStore_ConcurrentInsert(b *testing.B) {
	tmpDir := b.TempDir()
	path := filepath.Join(tmpDir, "runtime.db")

	store, err := NewStore(path)
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()

	projectID := "test-project"

	now := time.Now().UTC()
	var counter atomic.Int64

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			n := counter.Add(1)
			agentName := fmt.Sprintf("agent-%d", n%10)
			rec := DeliveryRecord{
				ProjectID:     projectID,
				AgentName:     agentName,
				MessageID:     n,
				FromName:      "bootstrap",
				Body:          "Test message",
				SentAt:        now.Add(-1 * time.Hour),
				ReceivedAt:    now,
				NotifiedAt:    time.Time{},
				SurfacedAt:    time.Time{},
				DismissedAt:   time.Time{},
				RetryAttempts: 0,
			}
			if err := store.AddRecord(rec); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkStore_AddRecordIfAbsent measures deduplication throughput.
// Scenario: manager receiving duplicate messages after reconnect.
// Measures the ON CONFLICT DO NOTHING path (record already present).
// The insert path is covered by BenchmarkStore_InsertRecord.
func BenchmarkStore_AddRecordIfAbsent(b *testing.B) {
	tmpDir := b.TempDir()
	path := filepath.Join(tmpDir, "runtime.db")

	store, err := NewStore(path)
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()

	projectID := "test-project"
	agentName := "test-agent"

	const poolSize = 100
	now := time.Now().UTC()
	messageIDs := make([]int64, poolSize)
	for i := 0; i < poolSize; i++ {
		messageIDs[i] = int64(i) + 1
		rec := DeliveryRecord{
			ProjectID:     projectID,
			AgentName:     agentName,
			MessageID:     int64(i) + 1,
			FromName:      "bootstrap",
			Body:          "Test message",
			SentAt:        now.Add(-1 * time.Hour),
			ReceivedAt:    now,
			NotifiedAt:    time.Time{},
			SurfacedAt:    time.Time{},
			DismissedAt:   time.Time{},
			RetryAttempts: 0,
		}
		if err := store.AddRecord(rec); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		created, err := store.AddRecordIfAbsent(DeliveryRecord{
			ProjectID:     projectID,
			AgentName:     agentName,
			MessageID:     messageIDs[i%poolSize],
			FromName:      "bootstrap",
			Body:          "Test message",
			SentAt:        now.Add(-1 * time.Hour),
			ReceivedAt:    now,
			NotifiedAt:    time.Time{},
			SurfacedAt:    time.Time{},
			DismissedAt:   time.Time{},
			RetryAttempts: 0,
		})
		if err != nil {
			b.Fatal(err)
		}
		if created {
			b.Fatal("expected existing record (dedup path), got insert")
		}
	}
}

// BenchmarkStore_MarkNotified measures notification marking throughput.
// Scenario: daemon marking pending deliveries as notified after OS notification.
// MarkNotified is an unconditional UPDATE by primary key — cycling through
// pre-inserted records produces real writes every iteration without state reset.
func BenchmarkStore_MarkNotified(b *testing.B) {
	tmpDir := b.TempDir()
	path := filepath.Join(tmpDir, "runtime.db")

	store, err := NewStore(path)
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()

	projectID := "test-project"
	agentName := "test-agent"

	const poolSize = 100
	now := time.Now().UTC()
	notifiedAt := now.Add(time.Minute)
	messageIDs := make([]int64, poolSize)
	for i := 0; i < poolSize; i++ {
		messageIDs[i] = int64(i) + 1
		rec := DeliveryRecord{
			ProjectID:     projectID,
			AgentName:     agentName,
			MessageID:     int64(i) + 1,
			FromName:      "bootstrap",
			Body:          "Test message",
			SentAt:        now.Add(-1 * time.Hour),
			ReceivedAt:    now,
			NotifiedAt:    time.Time{},
			SurfacedAt:    time.Time{},
			DismissedAt:   time.Time{},
			RetryAttempts: 0,
		}
		if err := store.AddRecord(rec); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := store.MarkNotified(projectID, agentName, messageIDs[i%poolSize], notifiedAt); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStore_LargeInbox measures Unread() on a store with 10,000 existing records.
// Scenario: agent with a huge inbox polling for unread.
func BenchmarkStore_LargeInbox(b *testing.B) {
	tmpDir := b.TempDir()
	path := filepath.Join(tmpDir, "runtime.db")

	store, err := NewStore(path)
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()

	projectID := "test-project"
	agentName := "test-agent"

	// Pre-populate with 10,000 unread records
	now := time.Now().UTC()
	for i := 0; i < 10000; i++ {
		rec := DeliveryRecord{
			ProjectID:     projectID,
			AgentName:     agentName,
			MessageID:     int64(i) + 1,
			FromName:      "bootstrap",
			Body:          "Test message with some content to simulate real data",
			SentAt:        now.Add(-time.Duration(i) * time.Second),
			ReceivedAt:    now,
			NotifiedAt:    time.Time{},
			SurfacedAt:    time.Time{}, // unread
			DismissedAt:   time.Time{},
			RetryAttempts: 0,
		}
		if err := store.AddRecord(rec); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := store.Unread(projectID, agentName)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStore_PendingNotifications measures PendingNotifications() query throughput.
// Scenario: daemon polling for pending notifications across all agents.
func BenchmarkStore_PendingNotifications(b *testing.B) {
	tmpDir := b.TempDir()
	path := filepath.Join(tmpDir, "runtime.db")

	store, err := NewStore(path)
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()

	// Pre-populate with pending notifications across multiple agents
	now := time.Now().UTC()
	for agentIdx := 0; agentIdx < 5; agentIdx++ {
		projectID := "test-project"
		agentName := "agent-" + string(rune('a'+agentIdx))

		for i := 0; i < 50; i++ {
			rec := DeliveryRecord{
				ProjectID:     projectID,
				AgentName:     agentName,
				MessageID:     int64(agentIdx*50 + i + 1),
				FromName:      "bootstrap",
				Body:          "Test message",
				SentAt:        now.Add(-1 * time.Hour),
				ReceivedAt:    now,
				NotifiedAt:    time.Time{}, // pending
				SurfacedAt:    now,
				DismissedAt:   time.Time{},
				RetryAttempts: 0,
			}
			if err := store.AddRecord(rec); err != nil {
				b.Fatal(err)
			}
		}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := store.PendingNotificationsAll()
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStore_PruneDeliveryRecords measures pruning query throughput.
// Scenario: periodic cleanup of old resolved delivery records.
// Pre-populates 1000 eligible records. First iteration prunes all 1000.
// Subsequent iterations measure the no-op DELETE query plan cost (0 rows matched
// but full WHERE clause and index evaluation still occurs).
// Regression signal: index removal, WHERE clause degradation, or query plan change.
func BenchmarkStore_PruneDeliveryRecords(b *testing.B) {
	tmpDir := b.TempDir()
	path := filepath.Join(tmpDir, "runtime.db")

	store, err := NewStore(path)
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()

	projectID := "test-project"
	agentName := "test-agent"

	now := time.Now().UTC()
	before := now.Add(time.Hour)
	for i := 0; i < 1000; i++ {
		rec := DeliveryRecord{
			ProjectID:     projectID,
			AgentName:     agentName,
			MessageID:     int64(i) + 1,
			FromName:      "bootstrap",
			Body:          "Test message",
			SentAt:        now.Add(-3 * time.Hour),
			ReceivedAt:    now.Add(-2 * time.Hour),
			NotifiedAt:    time.Time{},
			SurfacedAt:    now.Add(-30 * time.Minute),
			DismissedAt:   time.Time{},
			RetryAttempts: 0,
		}
		if err := store.AddRecord(rec); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := store.PruneDeliveryRecords(before); err != nil {
			b.Fatal(err)
		}
	}
}
