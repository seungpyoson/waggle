package tasks

import (
	"encoding/json"
	"log"
	"time"

	"github.com/seungpyoson/waggle/internal/events"
)

// StartTaskTTLChecker runs a periodic checker that cancels expired-TTL tasks
// and publishes task.stale events when tasks exceed the stale threshold.
func StartTaskTTLChecker(store *Store, hub *events.Hub, period time.Duration, staleThreshold time.Duration, stopCh <-chan struct{}) {
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			// 1. Cancel expired TTL tasks
			if count, err := store.CancelExpiredTTL(); err != nil {
				log.Printf("task ttl checker: %v", err)
			} else if count > 0 {
				log.Printf("task ttl checker: canceled %d expired tasks", count)
			}

			// 2. Check for stale tasks and publish event
			health, err := store.QueueHealth(staleThreshold)
			if err != nil {
				log.Printf("task ttl checker: queue health error: %v", err)
				continue
			}
			if health.StaleCount > 0 {
				data, _ := json.Marshal(map[string]any{
					"stale_count":        health.StaleCount,
					"oldest_age_seconds": health.OldestPendingAge,
				})
				evt, _ := json.Marshal(map[string]any{
					"topic": "task.events",
					"event": "task.stale",
					"data":  json.RawMessage(data),
					"ts":    time.Now().UTC().Format(time.RFC3339),
				})
				hub.Publish("task.events", evt)
			}
		}
	}
}

