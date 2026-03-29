package runtime

import "time"

// Watch describes a runtime watch registration scoped to a project and agent.
type Watch struct {
	ProjectID string    `json:"project_id"`
	AgentName string    `json:"agent_name"`
	Source    string    `json:"source"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// DeliveryRecord captures the lifecycle of a runtime delivery.
type DeliveryRecord struct {
	ProjectID        string    `json:"project_id"`
	AgentName        string    `json:"agent_name"`
	MessageID        int64     `json:"message_id"`
	FromName         string    `json:"from_name"`
	Body             string    `json:"body"`
	SentAt           time.Time `json:"sent_at"`
	ReceivedAt       time.Time `json:"received_at"`
	NotifiedAt       time.Time `json:"notified_at"`
	SurfacedAt       time.Time `json:"surfaced_at"`
	DismissedAt      time.Time `json:"dismissed_at"`
	RetryAttempts    int       `json:"-"`
	RetryNextAt      time.Time `json:"-"`
	RetryExhaustedAt time.Time `json:"-"`
}
