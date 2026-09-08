package brokerstate

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// The census tools of the initial native target. lsof locates a file by device
// and inode, so it answers for the store's identity rather than for a pathname,
// and ps reports the executable of every process, so a running old broker is
// found by what it is rather than by a PID file it may have outlived.
const (
	openFilesCommand = "lsof"
	processesCommand = "ps"
	// openFilesFields selects lsof's machine-readable output: the process id
	// and command name of a process set, and the descriptor and name of each
	// file it holds. Environments and arguments are never requested.
	openFilesFields = "-Fpcfn"
	// processesFormat prints one line per process as "<pid> <executable>". comm
	// is the executable, not the command line: arguments stay unread.
	processesFormat = "-axo"
	processesFields = "pid=,comm="
	// nothingFound is lsof's exit status when it could not locate a search item.
	// With one file per run it has exactly one item to locate, so this status
	// means that file has no open instances, and nothing else.
	nothingFound = 1
)

// OpenHandles reports every process holding one of these stores, or a journal
// sidecar of one, open.
//
// Each file is asked about in its own run, because lsof reports "I could not
// locate a search item" with the same exit status whether it located none of
// them or all but one, and this census may not confuse "nothing holds these
// files" with "one of these files was not visible to me".
func (c OSWriterCensus) OpenHandles(ctx context.Context, paths []string) ([]Handle, error) {
	if err := c.usable(); err != nil {
		return nil, err
	}
	subjects, err := censusSubjects(paths)
	if err != nil {
		return nil, err
	}
	var handles []Handle
	for _, subject := range subjects {
		held, err := c.holdersOf(ctx, subject)
		if err != nil {
			return nil, err
		}
		handles = append(handles, held...)
	}
	return handles, nil
}

func (c OSWriterCensus) holdersOf(ctx context.Context, path string) ([]Handle, error) {
	stdout, stderr, err := c.run(ctx, openFilesCommand, openFilesFields, "--", path)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == nothingFound && len(stdout) == 0 && len(stderr) == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("%s %s: %w%s", openFilesCommand, path, err, reported(stderr))
	}
	handles, err := parseOpenFiles(stdout)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", openFilesCommand, path, err)
	}
	if len(handles) == 0 {
		// Success means every search item was located, which cannot be true of a
		// run that listed nothing. Reporting an idle file here would be reporting
		// an answer the tool did not give.
		return nil, fmt.Errorf("%s %s: reported success and listed nothing%s", openFilesCommand, path, reported(stderr))
	}
	return handles, nil
}

// WaggleProcesses reports every running process whose executable carries the
// Waggle basename, except this one: the process taking the census is not a
// writer it has to wait for.
func (c OSWriterCensus) WaggleProcesses(ctx context.Context) ([]Handle, error) {
	if err := c.usable(); err != nil {
		return nil, err
	}
	stdout, stderr, err := c.run(ctx, processesCommand, processesFormat, processesFields)
	if err != nil {
		return nil, fmt.Errorf("%s: %w%s", processesCommand, err, reported(stderr))
	}
	processes, err := parseProcesses(stdout, c.Binary, os.Getpid())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", processesCommand, err)
	}
	return processes, nil
}

// run executes one census command under its own deadline and hands back both
// streams, because what a census tool wrote to each is part of reading its
// answer.
func (c OSWriterCensus) run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error) {
	deadline, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	command := exec.CommandContext(deadline, name, args...)
	var out, errs bytes.Buffer
	command.Stdout = &out
	command.Stderr = &errs
	err = command.Run()
	if expired := deadline.Err(); expired != nil {
		// A killed command's exit status says nothing about the machine, so the
		// deadline is reported instead of a status this census cannot read.
		err = fmt.Errorf("did not answer within %v: %w", c.Timeout, expired)
	}
	return out.Bytes(), errs.Bytes(), err
}

func reported(stderr []byte) string {
	if message := strings.TrimSpace(string(stderr)); message != "" {
		return ": " + message
	}
	return ""
}

// parseOpenFiles reads lsof's field format. A process set opens with the
// process id (p) and carries its command name (c); each file set within it
// opens with a descriptor (f) and names the file (n). Fields this census did
// not select are ignored, but a file set that never names its file is an error:
// a handle this package could not read must never become a handle it did not
// report.
func parseOpenFiles(out []byte) ([]Handle, error) {
	var handles []Handle
	var process Handle
	var known, unnamed bool
	lines := bufio.NewScanner(bytes.NewReader(out))
	for lines.Scan() {
		line := lines.Text()
		if line == "" {
			return nil, fmt.Errorf("open-file census has a line with no field")
		}
		field, value := line[0], line[1:]
		if field != 'p' && !known {
			return nil, fmt.Errorf("open-file census reports %q before naming a process", line)
		}
		switch field {
		case 'p':
			if unnamed {
				return nil, fmt.Errorf("open-file census left a file of process %d unnamed", process.PID)
			}
			pid, err := strconv.Atoi(value)
			if err != nil || pid <= 0 {
				return nil, fmt.Errorf("open-file census reports process %q", value)
			}
			process, known = Handle{PID: pid}, true
		case 'c':
			process.Command = value
		case 'f':
			if unnamed {
				return nil, fmt.Errorf("open-file census left a file of process %d unnamed", process.PID)
			}
			unnamed = true
		case 'n':
			if !unnamed {
				return nil, fmt.Errorf("open-file census names %q outside a file", value)
			}
			if value == "" {
				return nil, fmt.Errorf("open-file census reports a file of process %d with no name", process.PID)
			}
			handles = append(handles, Handle{PID: process.PID, Command: process.Command, Path: value})
			unnamed = false
		}
	}
	if err := lines.Err(); err != nil {
		return nil, fmt.Errorf("read open-file census: %w", err)
	}
	if unnamed {
		return nil, fmt.Errorf("open-file census left a file of process %d unnamed", process.PID)
	}
	return handles, nil
}

// parseProcesses reads the process listing and keeps the processes running the
// named executable, except self. The executable is the rest of the line, spaces
// and all, so a Waggle installed under a pathname with spaces is still counted.
func parseProcesses(out []byte, binary string, self int) ([]Handle, error) {
	var found []Handle
	listed := 0
	lines := bufio.NewScanner(bytes.NewReader(out))
	for lines.Scan() {
		line := strings.TrimSpace(lines.Text())
		if line == "" {
			return nil, fmt.Errorf("process census has an empty line")
		}
		id, executable, ok := strings.Cut(line, " ")
		if !ok {
			return nil, fmt.Errorf("process census reports %q with no executable", line)
		}
		pid, err := strconv.Atoi(id)
		if err != nil || pid <= 0 {
			return nil, fmt.Errorf("process census reports process %q", id)
		}
		executable = strings.TrimSpace(executable)
		if executable == "" {
			return nil, fmt.Errorf("process census names no executable for process %d", pid)
		}
		listed++
		if pid == self || filepath.Base(executable) != binary {
			continue
		}
		found = append(found, Handle{PID: pid, Command: filepath.Base(executable), Path: executable})
	}
	if err := lines.Err(); err != nil {
		return nil, fmt.Errorf("read process census: %w", err)
	}
	if listed == 0 {
		// A machine running this code is running at least this process, so an
		// empty listing is a census that failed, not an idle machine.
		return nil, fmt.Errorf("process census listed no processes")
	}
	return found, nil
}
