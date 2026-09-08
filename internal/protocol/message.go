package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
)

// Request represents a client → broker message
type Request struct {
	Version int `json:"version"`
	// Required field
	Cmd string `json:"cmd"`

	// Optional fields (all omitempty)
	Name           string           `json:"name,omitempty"`
	Topic          string           `json:"topic,omitempty"`
	Message        string           `json:"message,omitempty"`
	Payload        json.RawMessage  `json:"payload,omitempty"`
	Type           string           `json:"type,omitempty"`
	Tags           string           `json:"tags,omitempty"`
	DependsOn      string           `json:"depends_on,omitempty"`
	Priority       int              `json:"priority,omitempty"`
	Lease          int              `json:"lease,omitempty"`
	MaxRetries     int              `json:"max_retries,omitempty"`
	IdempotencyKey string           `json:"idempotency_key,omitempty"`
	Resource       string           `json:"resource,omitempty"`
	TaskID         string           `json:"task_id,omitempty"`
	ClaimToken     string           `json:"claim_token,omitempty"`
	Result         json.RawMessage  `json:"result,omitempty"`
	Reason         string           `json:"reason,omitempty"`
	Last           string           `json:"last,omitempty"`
	State          string           `json:"state,omitempty"`
	Owner          string           `json:"owner,omitempty"`
	MessageID      string           `json:"message_id,omitempty"`
	AttemptID      string           `json:"attempt_id,omitempty"`
	Credential     string           `json:"credential,omitempty"`
	Recipient      string           `json:"recipient,omitempty"`
	ConversationID string           `json:"conversation_id,omitempty"`
	After          string           `json:"after,omitempty"`
	Within         time.Duration    `json:"within_ns,omitempty"`
	Hops           int              `json:"hops,omitempty"`
	Enrollment     *EnrollmentInput `json:"enrollment,omitempty"`
	TTL            int              `json:"ttl,omitempty"`
}

type EnrollmentInput struct {
	Provider     string `json:"provider"`
	Conversation string `json:"conversation"`
	Endpoint     string `json:"endpoint"`
	Label        string `json:"label"`
}

// EnrollmentResponse is private hook-channel material. Callers must not print
// it to a terminal, prompt, log, or general command response.
type EnrollmentResponse struct {
	Incarnation string `json:"incarnation"`
	Credential  string `json:"credential"`
	State       string `json:"state"`
}

// DecodeRequest is the canonical wire decoder. Removed protocol fields are
// rejected rather than silently translated or ignored.
var ErrUnsupportedVersion = errors.New("unsupported protocol version")

func DecodeRequest(line []byte) (Request, error) {
	var req Request
	if !json.Valid(line) {
		return req, fmt.Errorf("request must contain one valid JSON value")
	}
	d := json.NewDecoder(bytes.NewReader(line))
	d.DisallowUnknownFields()
	err := d.Decode(&req)
	if req.Version != config.ProtocolVersion {
		return Request{}, ErrUnsupportedVersion
	}
	if err != nil {
		return Request{}, fmt.Errorf("invalid request framing or fields")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return Request{}, fmt.Errorf("request must contain one JSON object")
	}
	return req, nil
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
