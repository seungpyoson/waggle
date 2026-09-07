// Package driver defines native input observations. Only the broker may
// translate observations into canonical message transitions.
package driver

import (
	"context"
	"errors"
	"io"

	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/messages"
)

var ErrPeerIdentity = errors.New("native peer ownership could not be verified")

// Connector validates configured provider support before enrollment. Open
// binds one driver to the exact enrollment; it must never create or resume a native session.
// A failed Open has not submitted input and must join any work it started.
// Errors are safe diagnostics: exclude native response bodies and credentials.
type Connector interface {
	Check(messages.Enrollment) error
	Open(context.Context, *brokerstate.Operation, messages.Enrollment) (*Handle, error)
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
// policy. Identity is fixed by Connector.Open, never supplied again by a caller.
// Submit runs inside the broker's admitted attempt lifetime.
// Neither Close nor a context deadline proves native withdrawal.
type Driver interface {
	Probe(context.Context) (Readiness, error)
	Submit(context.Context, messages.Envelope) Outcome
	Close() error
}

// Handle is the only provider handle returned by connectors. Mechanics remain
// private behind it; every call requires a current operation from its owner.
type Handle struct {
	resource  *brokerstate.Resource
	mechanics Driver
}

func Open(ctx context.Context, lifetime *brokerstate.Operation, open func() (Driver, error)) (*Handle, error) {
	var mechanics Driver
	resource, err := lifetime.Open(ctx, func() (io.Closer, error) {
		var err error
		mechanics, err = open()
		return mechanics, err
	})
	if err != nil {
		return nil, err
	}
	return &Handle{resource: resource, mechanics: mechanics}, nil
}

func (h *Handle) Probe(ctx context.Context, op *brokerstate.Operation) (view Readiness, err error) {
	err = h.resource.Use(ctx, op, func() error {
		view, err = h.mechanics.Probe(ctx)
		return err
	})
	return
}

func (h *Handle) Submit(ctx context.Context, op *brokerstate.Operation, envelope messages.Envelope) (outcome Outcome, err error) {
	err = h.resource.Use(ctx, op, func() error {
		outcome = h.mechanics.Submit(ctx, envelope)
		return nil
	})
	return
}

func (h *Handle) Close() error { return h.resource.Close() }
