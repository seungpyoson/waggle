package brokerstate

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seungpyoson/waggle/internal/config"
)

// censusHelperEnv turns a copy of this test binary into a process that holds a
// file open and waits, so the census has a real second process to find: one
// running an executable with a chosen name, one holding a chosen file open.
//
// The executable is a copy of this test binary rather than a copy of a system
// tool such as /bin/sleep, because macOS kills a copy of a platform binary: its
// signature is only trusted at the location the system installed it.
const (
	censusHelperEnv   = "WAGGLE_CENSUS_HELPER"
	censusHelperFile  = "WAGGLE_CENSUS_HELPER_FILE"
	censusHelperReady = "census-helper-ready"
)

func TestCensusHelperProcess(t *testing.T) {
	if os.Getenv(censusHelperEnv) != "1" {
		t.Skip("child process of the OS census tests")
	}
	if path := os.Getenv(censusHelperFile); path != "" {
		held, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
	}
	fmt.Println(censusHelperReady)
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}

// censusHelper starts a copy of this test binary under the given name, holding
// open the file at hold (when it is not empty), and returns its process id. The
// child announces itself before the census runs, so no test waits on a process
// it only hopes has started. stop ends it and reaps it, so the process is truly
// gone from the machine's listing and not left as a zombie the census would
// still see.
func censusHelper(t *testing.T, name, hold string) (pid int, executable string, stop func()) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(target, image, 0700); err != nil {
		t.Fatal(err)
	}

	child := exec.Command(target, "-test.run=^TestCensusHelperProcess$")
	child.Env = append(os.Environ(), censusHelperEnv+"=1", censusHelperFile+"="+hold)
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	running := true
	stop = func() {
		if !running {
			return
		}
		running = false
		if err := input.Close(); err != nil {
			t.Error(err)
		}
		if err := child.Wait(); err != nil {
			t.Errorf("census helper exit: %v", err)
		}
	}
	t.Cleanup(stop)
	ready, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || strings.TrimSpace(ready) != censusHelperReady {
		t.Fatalf("census helper readiness = %q: %v", ready, err)
	}
	return child.Process.Pid, target, stop
}

// censusBinaryName is a name no other process on the machine carries, so a
// census that finds it found exactly the process this test started.
func censusBinaryName(t *testing.T) string {
	t.Helper()
	return "waggle-census-" + strings.ToLower(rand.Text()[:10])
}

func testCensus(t *testing.T) OSWriterCensus {
	t.Helper()
	return OSWriterCensus{Binary: censusBinaryName(t), Timeout: config.Defaults.CensusTimeout}
}

// holds reports whether the census saw pid holding path.
func holds(handles []Handle, pid int, path string) bool {
	for _, h := range handles {
		if h.PID == pid && h.Path == path && h.Command != "" {
			return true
		}
	}
	return false
}

// resolved is the pathname the operating system reports for a file, which is
// not the pathname the test asked about: a temporary directory sits under a
// symlinked /var on this target.
func resolved(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

// TestOSWriterCensusSeesOwnOpenHandle is the census's whole claim: a file this
// process holds open is reported, and only while it is held.
func TestOSWriterCensusSeesOwnOpenHandle(t *testing.T) {
	census := testCensus(t)
	path := filepath.Join(t.TempDir(), config.Defaults.DBFile)
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	handles, err := census.OpenHandles(t.Context(), []string{path})
	if err != nil {
		t.Fatalf("open handles: %v", err)
	}
	if !holds(handles, os.Getpid(), resolved(t, path)) {
		t.Fatalf("census missed this process holding %s: %+v", path, handles)
	}

	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	handles, err = census.OpenHandles(t.Context(), []string{path})
	if err != nil {
		t.Fatalf("open handles after close: %v", err)
	}
	if len(handles) != 0 {
		t.Fatalf("released file still has holders: %+v", handles)
	}
}

// TestOSWriterCensusSeesJournalSidecarHandle is the case a census of the
// database file alone would miss: a writer can hold the WAL open while the
// database itself has no handle.
func TestOSWriterCensusSeesJournalSidecarHandle(t *testing.T) {
	census := testCensus(t)
	database := filepath.Join(t.TempDir(), config.Defaults.DBFile)
	if err := os.WriteFile(database, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, sidecar := range sidecars(database) {
		t.Run(filepath.Ext(sidecar), func(t *testing.T) {
			file, err := os.Create(sidecar)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Remove(sidecar) })
			handles, err := census.OpenHandles(t.Context(), []string{database})
			if err != nil {
				t.Fatalf("open handles: %v", err)
			}
			if !holds(handles, os.Getpid(), resolved(t, sidecar)) {
				t.Fatalf("census missed a handle on %s: %+v", sidecar, handles)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestOSWriterCensusRefusesWhatItCannotSee proves the census never answers
// "nobody holds it" for a question it could not ask.
func TestOSWriterCensusRefusesWhatItCannotSee(t *testing.T) {
	census := testCensus(t)
	missing := filepath.Join(t.TempDir(), config.Defaults.DBFile)

	handles, err := census.OpenHandles(t.Context(), []string{missing})
	if err == nil {
		t.Fatalf("census of a missing store reported %+v", handles)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("refusal does not name the missing store: %v", err)
	}
	if handles, err := census.OpenHandles(t.Context(), nil); err == nil {
		t.Fatalf("census of nothing reported %+v", handles)
	}
	if handles, err := census.OpenHandles(t.Context(), []string{config.Defaults.DBFile}); err == nil {
		t.Fatalf("census of a relative path reported %+v", handles)
	}
}

// TestOSWriterCensusRefusesWhenUnconfigured keeps a zero value from passing for
// a census: it would otherwise report an empty machine.
func TestOSWriterCensusRefusesWhenUnconfigured(t *testing.T) {
	path := filepath.Join(t.TempDir(), config.Defaults.DBFile)
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, census := range []OSWriterCensus{
		{},
		{Binary: "waggle"},
		{Timeout: config.Defaults.CensusTimeout},
	} {
		if handles, err := census.OpenHandles(t.Context(), []string{path}); err == nil {
			t.Fatalf("%+v reported open handles %+v", census, handles)
		}
		if processes, err := census.WaggleProcesses(t.Context()); err == nil {
			t.Fatalf("%+v reported processes %+v", census, processes)
		}
	}
}

// TestOSWriterCensusDetectsRunningBinary proves the process half of the census:
// a running executable with the Waggle basename is found by name alone, with no
// PID file involved, and is gone from the listing once it exits.
func TestOSWriterCensusDetectsRunningBinary(t *testing.T) {
	binary := censusBinaryName(t)
	census := OSWriterCensus{Binary: binary, Timeout: config.Defaults.CensusTimeout}
	pid, executable, stop := censusHelper(t, binary, "")

	processes, err := census.WaggleProcesses(t.Context())
	if err != nil {
		t.Fatalf("process census: %v", err)
	}
	if len(processes) != 1 || processes[0].PID != pid || processes[0].Command != binary {
		t.Fatalf("process census = %+v, want only pid %d running %s", processes, pid, binary)
	}
	if processes[0].Path != executable {
		t.Fatalf("census reports executable %q, want %q", processes[0].Path, executable)
	}

	stop()
	processes, err = census.WaggleProcesses(t.Context())
	if err != nil {
		t.Fatalf("process census after exit: %v", err)
	}
	if len(processes) != 0 {
		t.Fatalf("exited child still counted: %+v", processes)
	}
}

// TestOSWriterCensusSeesAnotherProcessHandle is the census's real subject: not
// this process's own handles, but a handle held by a process it does not
// control and cannot ask.
func TestOSWriterCensusSeesAnotherProcessHandle(t *testing.T) {
	census := testCensus(t)
	database := filepath.Join(t.TempDir(), config.Defaults.DBFile)
	if err := os.WriteFile(database, nil, 0600); err != nil {
		t.Fatal(err)
	}
	pid, _, stop := censusHelper(t, censusBinaryName(t), database)

	handles, err := census.OpenHandles(t.Context(), []string{database})
	if err != nil {
		t.Fatalf("open handles: %v", err)
	}
	if !holds(handles, pid, resolved(t, database)) {
		t.Fatalf("census missed pid %d holding %s: %+v", pid, database, handles)
	}

	stop()
	handles, err = census.OpenHandles(t.Context(), []string{database})
	if err != nil {
		t.Fatalf("open handles after exit: %v", err)
	}
	if len(handles) != 0 {
		t.Fatalf("store still has holders after the child exited: %+v", handles)
	}
}

// TestNewOSWriterCensusNamesThisBinaryAndExcludesIt covers the constructor and
// the one process a census must never report: this test binary carries the name
// the census looks for, so a census that failed to exclude itself would block
// every conversion it is asked to guard.
func TestNewOSWriterCensusNamesThisBinaryAndExcludesIt(t *testing.T) {
	census, err := NewOSWriterCensus()
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if census.Binary != filepath.Base(self) {
		t.Fatalf("census binary = %q, want %q", census.Binary, filepath.Base(self))
	}
	if census.Timeout != config.Defaults.CensusTimeout {
		t.Fatalf("census timeout = %v, want %v", census.Timeout, config.Defaults.CensusTimeout)
	}
	processes, err := census.WaggleProcesses(t.Context())
	if err != nil {
		t.Fatalf("process census: %v", err)
	}
	for _, p := range processes {
		if p.PID == os.Getpid() {
			t.Fatalf("census counted itself: %+v", p)
		}
	}
}

// TestParseOpenFilesReadsFieldFormat pins the lsof field format this package
// reads: a process set opens with its pid and command, and each file set that
// follows names one open file.
func TestParseOpenFilesReadsFieldFormat(t *testing.T) {
	out := strings.Join([]string{
		"p1234", "cwaggle", "f7", "n/data/state.db", "f8", "n/data/state.db-wal",
		"p99", "cWaggle Helper", "f3", "n/data/state.db",
	}, "\n") + "\n"
	handles, err := parseOpenFiles([]byte(out))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []Handle{
		{PID: 1234, Command: "waggle", Path: "/data/state.db"},
		{PID: 1234, Command: "waggle", Path: "/data/state.db-wal"},
		{PID: 99, Command: "Waggle Helper", Path: "/data/state.db"},
	}
	if fmt.Sprint(handles) != fmt.Sprint(want) {
		t.Fatalf("handles = %+v, want %+v", handles, want)
	}
	if handles, err := parseOpenFiles(nil); err != nil || len(handles) != 0 {
		t.Fatalf("empty census = %+v, %v", handles, err)
	}
}

// TestParseOpenFilesRefusesUnreadableOutput proves no shape of unreadable
// output can become a shorter listing.
func TestParseOpenFilesRefusesUnreadableOutput(t *testing.T) {
	for name, out := range map[string]string{
		"file before any process": "f3\nn/data/state.db\n",
		"name before any process": "n/data/state.db\n",
		"unreadable pid":          "pnineteen\ncwaggle\nf3\nn/data/state.db\n",
		"negative pid":            "p-3\ncwaggle\nf3\nn/data/state.db\n",
		"unnamed file":            "p12\ncwaggle\nf3\nf4\nn/data/state.db\n",
		"unnamed trailing file":   "p12\ncwaggle\nf3\nn/data/state.db\nf4\n",
		"unnamed file at new pid": "p12\ncwaggle\nf3\np13\ncwaggle\nf4\nn/data/state.db\n",
		"empty line":              "p12\n\ncwaggle\nf3\nn/data/state.db\n",
		"empty name":              "p12\ncwaggle\nf3\nn\n",
	} {
		t.Run(name, func(t *testing.T) {
			if handles, err := parseOpenFiles([]byte(out)); err == nil {
				t.Fatalf("parsed %q as %+v", out, handles)
			}
		})
	}
}

// TestParseProcessesMatchesBasenameAndExcludesSelf pins the ps listing rule:
// the executable basename decides, wherever the executable lives, and this
// process is never one of the answers.
func TestParseProcessesMatchesBasenameAndExcludesSelf(t *testing.T) {
	out := strings.Join([]string{
		"  501 /usr/local/bin/waggle",
		"  502 /Users/x/Apple Silicon/waggle",
		"  503 /usr/local/bin/waggle-shim",
		"  504 /usr/bin/ssh",
		"  505 /usr/local/bin/waggle",
	}, "\n") + "\n"

	processes, err := parseProcesses([]byte(out), "waggle", 505)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []Handle{
		{PID: 501, Command: "waggle", Path: "/usr/local/bin/waggle"},
		{PID: 502, Command: "waggle", Path: "/Users/x/Apple Silicon/waggle"},
	}
	if fmt.Sprint(processes) != fmt.Sprint(want) {
		t.Fatalf("processes = %+v, want %+v", processes, want)
	}
	if processes, err := parseProcesses([]byte(out), "waggle-shim", 1); err != nil ||
		len(processes) != 1 || processes[0].PID != 503 {
		t.Fatalf("basename match = %+v, %v", processes, err)
	}
}

// TestParseProcessesRefusesUnreadableOutput proves an unreadable or empty
// listing is never read as an idle machine.
func TestParseProcessesRefusesUnreadableOutput(t *testing.T) {
	for name, out := range map[string]string{
		"no processes at all": "",
		"unreadable pid":      "root /usr/local/bin/waggle\n",
		"no executable":       "  501\n",
		"empty line":          "  501 /usr/local/bin/waggle\n\n  502 /usr/bin/ssh\n",
	} {
		t.Run(name, func(t *testing.T) {
			if processes, err := parseProcesses([]byte(out), "waggle", 1); err == nil {
				t.Fatalf("parsed %q as %+v", out, processes)
			}
		})
	}
}
