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
// # What this census can and cannot see
//
// The two halves have different reach, and Scope reports it so an operator is
// never left to assume otherwise:
//
//   - Processes: every user's. The process listing names every process on the
//     machine, and this census matches them by executable basename, so a
//     retired broker running under another account is still found.
//   - Open files: the invoking user's only. An unprivileged open-file census on
//     this target reads the file tables of the invoking user's processes and
//     silently reports nothing for anyone else's, which it cannot distinguish
//     from a file nobody holds. A handle held by root, or by another account, is
//     therefore invisible to it.
//
// So a conversion guarded by this census is safe against the same-user writers
// it was written for, and against a differently-owned broker that still carries
// the Waggle executable name, but not against a differently-owned process
// holding the store under some other name. Machine-wide cutover (M2) must run
// the census with enough privilege to read every user's open files, or add a
// check that closes that gap, before it treats the open-file half as
// machine-wide.
//
// Binary is the executable basename a running old broker carries, taken from
// this process's own executable rather than configured or guessed. Timeout
// bounds one census command. OpenFiles and Processes are the census tools as
// resolved at construction. Neither question is asked about a process's
// environment or arguments, and none is ever read.
type OSWriterCensus struct {
	Binary  string
	Timeout time.Duration
	// OpenFiles and Processes are the pathnames of the census tools, resolved
	// once by NewOSWriterCensus rather than looked up again per run, so a run
	// cannot silently answer from a different tool than the one that was found,
	// and a missing tool is refused before any store is examined. They are
	// resolved through PATH: a same-user attacker who could plant a tool there
	// already controls the converting executable itself, so an absolute pathname
	// would buy nothing, while the resolved pathname in an error tells the
	// operator which tool actually answered.
	OpenFiles string
	Processes string
}

// NewOSWriterCensus builds the census this executable can perform: the name it
// looks for is its own, so there is no second place that decides what a Waggle
// process is called, and the tools it will run are found now rather than at the
// moment an answer is needed.
//
// The census it returns sees the invoking user's open files and every user's
// processes; see OSWriterCensus for what that does not cover.
func NewOSWriterCensus() (OSWriterCensus, error) {
	self, err := os.Executable()
	if err != nil {
		return OSWriterCensus{}, fmt.Errorf("%w: resolve own executable: %w", ErrCensusUnavailable, err)
	}
	binary := filepath.Base(self)
	if binary == "." || binary == string(filepath.Separator) {
		return OSWriterCensus{}, fmt.Errorf("%w: own executable %q has no name to census", ErrCensusUnavailable, self)
	}
	openFiles, processes, err := resolveCensusTools()
	if err != nil {
		return OSWriterCensus{}, err
	}
	return OSWriterCensus{
		Binary:    binary,
		Timeout:   config.Defaults.CensusTimeout,
		OpenFiles: openFiles,
		Processes: processes,
	}, nil
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
