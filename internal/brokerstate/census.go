package brokerstate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
)

// OSWriterCensus is the operating system's own answer to the two questions
// conversion must ask before it changes anything: is a retired Waggle
// executable still running, and does any process still hold the canonical store
// open. It answers them with the platform's census tools, so it exists only for
// hosts whose tools this project has verified; every other host refuses.
//
// Binary is the executable basename a running old broker carries, taken from
// this process's own executable rather than configured or guessed. Timeout
// bounds one census command. Neither question is asked about a process's
// environment or arguments, and none is ever read.
type OSWriterCensus struct {
	Binary  string
	Timeout time.Duration
}

// NewOSWriterCensus builds the census this executable can perform: the name it
// looks for is its own, so there is no second place that decides what a Waggle
// process is called.
func NewOSWriterCensus() (OSWriterCensus, error) {
	self, err := os.Executable()
	if err != nil {
		return OSWriterCensus{}, fmt.Errorf("resolve own executable for the writer census: %w", err)
	}
	binary := filepath.Base(self)
	if binary == "." || binary == string(filepath.Separator) {
		return OSWriterCensus{}, fmt.Errorf("own executable %q has no name to census", self)
	}
	return OSWriterCensus{Binary: binary, Timeout: config.Defaults.CensusTimeout}, nil
}

// usable refuses a census that was never configured. A zero value would
// otherwise answer every question with an empty machine, which is the one
// answer a census must never invent.
func (c OSWriterCensus) usable() error {
	if c.Binary == "" || c.Timeout <= 0 {
		return fmt.Errorf("writer census needs the Waggle executable name and a positive timeout")
	}
	return nil
}

// censusSubjects expands each requested path with the journal sidecars that
// belong to it and keeps the files that are actually there. A requested store
// that is missing is a refusal: the census tools locate a file by device and
// inode, so a pathname that resolves to nothing cannot be asked about, and
// "nothing holds a file that is not there" would be an answer to a different
// question than the one conversion asked.
func censusSubjects(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("writer census asked about no paths")
	}
	var subjects, missing []string
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("writer census requires absolute paths, got %q", path)
		}
		if !exists(path) {
			missing = append(missing, path)
			continue
		}
		subjects = append(subjects, path)
		for _, sidecar := range sidecars(path) {
			if exists(sidecar) {
				subjects = append(subjects, sidecar)
			}
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("writer census subjects are not there: %s", strings.Join(missing, ", "))
	}
	return subjects, nil
}
