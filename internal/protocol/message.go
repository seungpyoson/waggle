package protocol

import "encoding/json"

// Request represents a client → broker message
type Request struct {
	// Required field
	Cmd string `json:"cmd"`

	// Optional fields (all omitempty)
	Name            string          `json:"name,omitempty"`
	Topic           string          `json:"topic,omitempty"`
	Message         string          `json:"message,omitempty"`
	Payload         json.RawMessage `json:"payload,omitempty"`
	Type            string          `json:"type,omitempty"`
	Tags            string          `json:"tags,omitempty"`
	DependsOn       string          `json:"depends_on,omitempty"`
	Priority        int             `json:"priority,omitempty"`
	Lease           int             `json:"lease,omitempty"`
	MaxRetries      int             `json:"max_retries,omitempty"`
	IdempotencyKey  string          `json:"idempotency_key,omitempty"`
	Resource        string          `json:"resource,omitempty"`
	TaskID          string          `json:"task_id,omitempty"`
	ClaimToken      string          `json:"claim_token,omitempty"`
	Result          json.RawMessage `json:"result,omitempty"`
	Reason          string          `json:"reason,omitempty"`
	Last            string          `json:"last,omitempty"`
	State           string          `json:"state,omitempty"`
	Owner           string          `json:"owner,omitempty"`
	MessageID       int64           `json:"message_id,omitempty"`
	MsgPriority     string          `json:"msg_priority,omitempty"`
	TTL             int             `json:"ttl,omitempty"`
	AwaitAck        bool            `json:"await_ack,omitempty"`
	Timeout         int             `json:"timeout,omitempty"`
}

// Response represents a broker → client response
type Response struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
	Code  string          `json:"code,omitempty"`
}

// Event represents a broker → client streamed event
type Event struct {
	Topic string          `json:"topic"`
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data,omitempty"`
	TS    string          `json:"ts"`
}

// OKResponse constructs a successful Response with OK=true
func OKResponse(data json.RawMessage) Response {
	return Response{
		OK:   true,
		Data: data,
	}
}

// ErrResponse constructs an error Response with OK=false and the given code
func ErrResponse(code, message string) Response {
	return Response{
		OK:    false,
		Code:  code,
		Error: message,
	}
}

