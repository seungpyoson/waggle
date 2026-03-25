package messages

import (
	"log"
	"time"
)

// StartTTLChecker runs a periodic TTL expiry checker
// Mirrors the pattern from tasks.StartLeaseChecker
func StartTTLChecker(store *Store, period time.Duration, stopCh <-chan struct{}) {
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			if count, err := store.MarkExpired(); err != nil {
				log.Printf("ttl checker: %v", err)
			} else if count > 0 {
				log.Printf("ttl checker: expired %d messages", count)
			}
		}
	}
}

