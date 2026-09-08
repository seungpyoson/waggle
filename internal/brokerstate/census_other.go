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

// Scope answers what this census covers, which is nothing at all.
func (OSWriterCensus) Scope() string { return "no census on this host" }

// resolveCensusTools finds no tools because this host runs none: a census that
// answers nothing needs nothing to answer with. Construction still succeeds, so
// a caller here meets the same explicit refusal from the questions themselves
// that it would meet from a host whose tools went missing.
func resolveCensusTools() (openFiles, processes string, err error) {
	return "", "", nil
}
