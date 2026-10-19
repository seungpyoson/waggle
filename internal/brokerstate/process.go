package brokerstate

import "context"

// ProcessIdentity includes native start identity. Start is deliberately opaque:
// Darwin uses seconds/microseconds and Linux uses kernel clock ticks. Neither
// is guessed from a timestamp, executable pathname, or PID file.
type ProcessIdentity struct {
	BootID string
	PID    int
	Start  string
}

func (p ProcessIdentity) validate() error {
	if p.BootID == "" || p.PID <= 0 || p.Start == "" {
		return ErrIdentityUnavailable
	}
	return nil
}

type ProcessStatus uint8

const (
	ProcessAlive ProcessStatus = iota + 1
	ProcessExited
)

// Errors mean unverifiable, never exited. Inspect must use the recorded native
// process identity and obey ctx; a status probe or kill(pid, 0) is insufficient.
type ProcessInspector interface {
	Current(context.Context) (ProcessIdentity, error)
	Inspect(context.Context, ProcessIdentity) (ProcessStatus, error)
}

type OSProcessInspector struct{}
