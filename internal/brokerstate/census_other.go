//go:build !darwin

package brokerstate

import "context"

// Initial native conformance targets macOS. Other hosts have no verified
// process-and-open-file census, and conversion may not proceed on an
// unanswered census, so both questions refuse rather than report an idle
// machine. A host gains conversion by having its census tools verified here,
// never by a caller deciding to skip the check.
func (OSWriterCensus) OpenHandles(context.Context, []string) ([]Handle, error) {
	return nil, ErrCensusUnavailable
}

func (OSWriterCensus) WaggleProcesses(context.Context) ([]Handle, error) {
	return nil, ErrCensusUnavailable
}
