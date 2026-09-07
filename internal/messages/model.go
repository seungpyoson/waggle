package messages

import "time"

type Enrollment struct {
	ID           string `json:"id"`
	Provider     string `json:"provider"`
	Conversation string `json:"conversation"`
	Endpoint     string `json:"-"`
	Label        string `json:"label"`
	State        string `json:"state"`
	Evidence     string `json:"evidence,omitempty"`
}

type EnrollmentScope uint8

const (
	AllEnrollments EnrollmentScope = iota + 1
	ObservableEnrollments
)

type Message struct {
	ID            string    `json:"id"`
	Sender        string    `json:"sender"`
	Recipient     string    `json:"recipient"`
	Conversation  string    `json:"conversation_id"`
	ReplyTo       string    `json:"reply_to,omitempty"`
	RequestID     string    `json:"request_id"`
	Body          string    `json:"body"`
	Attempt       string    `json:"attempt_id,omitempty"`
	BlockedBy     string    `json:"blocked_by_message,omitempty"`
	Possession    string    `json:"possession"`
	Disposition   string    `json:"disposition"`
	Deadline      time.Time `json:"deadline"`
	RemainingHops int       `json:"remaining_hops"`
}

// Envelope metadata is authored by Waggle. Body remains untrusted peer text.
// Neither the endpoint nor any credential is included in a native prompt.
type Envelope struct {
	Message         Message `json:"message"`
	SenderLabel     string  `json:"sender_label"`
	Acknowledgement string  `json:"acknowledgement"`
}

type Dispatch struct {
	Recipient Enrollment
	Envelope  Envelope
}

// Command is closed to domain-defined transitions. All callers use Apply in
// one fenced transaction; no transport may update an individual receipt flag.
type Command interface{ messageCommand() }

type Enroll struct{ Provider, Conversation, Endpoint, Label string }

func (Enroll) messageCommand() {}

// Bind is internal native evidence, never an agent-facing RPC. The broker's
// driver must verify this exact conversation on this exact endpoint first.
type Bind struct{ ID, Conversation, Endpoint, Evidence string }

func (Bind) messageCommand() {}

// Unavailable records inability to verify native receive availability. A
// pending enrollment remains pending until its deadline; a bound one disconnects.
type Unavailable struct{ ID, Evidence string }

func (Unavailable) messageCommand() {}

type Restart struct{}

func (Restart) messageCommand() {}

type Enqueue struct {
	Credential string
	Recipient  string
	RequestID  string
	Body       string
	Within     time.Duration
	Hops       int
	Offline    bool
}

func (Enqueue) messageCommand() {}

type Reply struct{ Credential, MessageID, RequestID, Body string }

func (Reply) messageCommand() {}

type Select struct{}

func (Select) messageCommand() {}

type Observe struct{ Attempt, Recipient, Kind, Evidence, ProviderRef string }

func (Observe) messageCommand() {}

type Consume struct{ Credential, MessageID, Attempt string }

func (Consume) messageCommand() {}

type Stop struct{ Credential, Conversation, Reason string }

func (Stop) messageCommand() {}

// Retire is an operator operation. It never clears a retained-input barrier.
type Retire struct{ ID, Reason string }

func (Retire) messageCommand() {}

type Expire struct{}

func (Expire) messageCommand() {}

type Result struct {
	Enrollment Enrollment
	Credential string `json:"-"` // delivered only over the enrollment channel, never logged
	Message    Message
	Dispatches []Dispatch
}
