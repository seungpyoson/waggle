// Package driver defines native input observations. Only the broker may
// translate observations into canonical message transitions.
package driver

import (
	"context"
	"errors"

	"github.com/seungpyoson/waggle/internal/messages"
)

var ErrPeerIdentity = errors.New("native peer ownership could not be verified")
var ErrCloseFailed = errors.New("native transport close failed")

// Connector validates configured provider support before enrollment. Open
// binds one driver to the exact enrollment; it must never create or resume a
// native session. A failed Open has not submitted input and must join any
// work it started. Errors are safe diagnostics: exclude native response
// bodies and credentials. Only an enrollment worker calls Open.
type Connector interface {
	Check(messages.Enrollment) error
	Open(context.Context, messages.Enrollment) (Driver, error)
}

type Availability uint8

const (
	Unavailable Availability = iota + 1
	Idle
	Busy
)

type Readiness struct {
	Conversation string
	Availability Availability
	TurnID       string
	Evidence     string
}

type Possession uint8

const (
	NotSubmitted Possession = iota + 1
	Accepted
	Held
	Refused
	Uncertain
)

type Outcome struct {
	Possession  Possession
	ProviderRef string
	Evidence    string
}

// Implementations have no queue, database, model selection or permission
// policy. Identity is fixed by Connector.Open, never supplied again by a
// caller. Neither Close nor a context deadline proves native withdrawal.
type Driver interface {
	Probe(context.Context) (Readiness, error)
	Submit(context.Context, messages.Envelope) Outcome
	Close() error
}
