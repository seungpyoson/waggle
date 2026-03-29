package runtime

import (
	"path/filepath"
	"testing"
	"time"
)

// Baseline results (M4 Darwin, 3 runs averaged):
// BenchmarkStore_InsertRecord-10          	   19801	     61587 ns/op	    1071 B/op	      18 allocs/op
// BenchmarkStore_Unread-10                	    6398	    170990 ns/op	  154352 B/op	    3035 allocs/op
// BenchmarkStore_MarkSurfacedBatch-10     	    8660	    125288 ns/op	    6796 B/op	      29 allocs/op
// BenchmarkStore_ConcurrentInsert-10      	   12326	    114301 ns/op	    1222 B/op	      22 allocs/op
// BenchmarkStore_LargeInbox-10            	      62	  17151282 ns/op	22227635 B/op	  319792 allocs/op
// BenchmarkStore_PendingNotifications-10  	    2583	    441550 ns/op	  365856 B/op	    8285 allocs/op
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
			agentName := "agent-" + string(rune(counter%10))
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
