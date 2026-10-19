//go:build !darwin

package brokerstate

import "context"

// Initial native conformance targets macOS. Other hosts must provide verified
// process-incarnation inspection before their broker can acquire ownership.
func (OSProcessInspector) Current(context.Context) (ProcessIdentity, error) {
	return ProcessIdentity{}, ErrIdentityUnavailable
}

func (OSProcessInspector) Inspect(context.Context, ProcessIdentity) (ProcessStatus, error) {
	return 0, ErrIdentityUnavailable
}
