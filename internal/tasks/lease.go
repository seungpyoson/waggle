package tasks

import (
	"log"
	"time"
)

// StartLeaseChecker starts a goroutine that periodically checks for expired leases
// and re-queues them. It runs until the stop channel is closed.
func StartLeaseChecker(store *Store, interval time.Duration, stop <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			count, err := store.RequeueExpiredLeases()
			if err != nil {
				log.Printf("lease checker: error requeuing expired leases: %v", err)
			} else if count > 0 {
				log.Printf("lease checker: requeued %d expired leases", count)
			}
		}
	}
}

