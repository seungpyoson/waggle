package runtime

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// Baseline results (M4 Darwin, count=1):
// BenchmarkStore_InsertRecord-10               17506	     66261 ns/op	    1071 B/op	      18 allocs/op
// BenchmarkStore_AddRecordIfAbsent-10          22599	     57474 ns/op	    1071 B/op	      18 allocs/op
// BenchmarkStore_Unread-10                      6522	    190973 ns/op	  154354 B/op	    3035 allocs/op
// BenchmarkStore_MarkSurfacedBatch-10           7347	    208639 ns/op	    6873 B/op	      29 allocs/op
// BenchmarkStore_MarkNotified-10               27361	     49417 ns/op	     376 B/op	      12 allocs/op
// BenchmarkStore_ConcurrentInsert-10           17836	     74561 ns/op	    1224 B/op	      22 allocs/op
// BenchmarkStore_LargeInbox-10                    60	  20981172 ns/op	22227670 B/op	  319792 allocs/op
// BenchmarkStore_PruneDeliveryRecords-10         628	   2068503 ns/op	     185 B/op	       8 allocs/op
// BenchmarkStore_PendingNotifications-10       2451	    481660 ns/op	  365856 B/op	    8285 allocs/op
//
// Run with: go test ./internal/runtime -bench=BenchmarkStore -benchmem -count=1

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

// BenchmarkStore_AddRecordIfAbsent measures insert-if-absent throughput for delivery_records.
// Scenario: message intake deduplicating new messages.
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
		created, err := store.AddRecordIfAbsent(rec)
		if err != nil {
			b.Fatal(err)
		}
		if !created {
			b.Fatal("expected AddRecordIfAbsent to insert a fresh record")
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
	now := time.Now().UTC()
	messageIDs := make([]int64, 0, 50)
	for i := 0; i < 50; i++ {
		messageIDs = append(messageIDs, int64(i)+1)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		if _, err := store.db.Exec(`DELETE FROM delivery_records WHERE project_id = ? AND agent_name = ?`, projectID, agentName); err != nil {
			b.Fatal(err)
		}
		for _, id := range messageIDs {
			rec := DeliveryRecord{
				ProjectID:     projectID,
				AgentName:     agentName,
				MessageID:     id,
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
		b.StartTimer()
		if err := store.MarkSurfacedBatch(projectID, agentName, messageIDs); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStore_MarkNotified measures notification marking throughput.
// Scenario: daemon marking one pending delivery as notified.
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
	messageID := int64(1)
	now := time.Now().UTC()

	rec := DeliveryRecord{
		ProjectID:     projectID,
		AgentName:     agentName,
		MessageID:     messageID,
		FromName:      "bootstrap",
		Body:          "Test message",
		SentAt:        now.Add(-1 * time.Hour),
		ReceivedAt:    now,
		NotifiedAt:    time.Time{},
		SurfacedAt:    now,
		DismissedAt:   time.Time{},
		RetryAttempts: 0,
	}
	if err := store.AddRecord(rec); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		if _, err := store.db.Exec(`UPDATE delivery_records SET notified_at = '' WHERE project_id = ? AND agent_name = ? AND message_id = ?`, projectID, agentName, messageID); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if err := store.MarkNotified(projectID, agentName, messageID, time.Now().UTC()); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStore_ConcurrentInsert measures concurrent insert throughput.
// Uses b.RunParallel to simulate N agents inserting simultaneously.
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

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		counter := 0
		for pb.Next() {
			agentName := fmt.Sprintf("agent-%d", counter%10)
			rec := DeliveryRecord{
				ProjectID:     projectID,
				AgentName:     agentName,
				MessageID:     int64(counter) + 1,
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
			counter++
		}
	})
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

// BenchmarkStore_PruneDeliveryRecords measures pruning throughput on old resolved deliveries.
// Scenario: periodic cleanup of large prunable delivery history.
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
		b.StopTimer()
		if _, err := store.db.Exec(`DELETE FROM delivery_records WHERE project_id = ? AND agent_name = ?`, projectID, agentName); err != nil {
			b.Fatal(err)
		}
		for j := 0; j < 1000; j++ {
			rec := DeliveryRecord{
				ProjectID:     projectID,
				AgentName:     agentName,
				MessageID:     int64(j) + 1,
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
		b.StartTimer()
		if _, err := store.PruneDeliveryRecords(time.Now().UTC().Add(time.Hour)); err != nil {
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
