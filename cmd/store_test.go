package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/spf13/cobra"
)

// fixtureScope is what the test census claims to have seen, so a report that
// carries it proves the CLI passed this census through rather than building its
// own.
const fixtureScope = "fixture census"

// fakeCensus answers the two census questions from a script instead of from the
// operating system. A conversion in a test must never depend on what else is
// running on the machine that runs it.
type fakeCensus struct {
	processes []brokerstate.Handle
	handles   []brokerstate.Handle
	err       error
}

func (c fakeCensus) OpenHandles(context.Context, []string) ([]brokerstate.Handle, error) {
	return c.handles, c.err
}

func (c fakeCensus) WaggleProcesses(context.Context) ([]brokerstate.Handle, error) {
	return c.processes, c.err
}

func (c fakeCensus) Scope() string { return fixtureScope }

func clearCensus() storeCommands {
	return storeCommands{newCensus: func() (brokerstate.WriterCensus, error) { return fakeCensus{}, nil }}
}

// refusedCensus stands in where a command must not need a census at all: if one
// is built, the command fails loudly instead of quietly consulting the machine.
func refusedCensus() storeCommands {
	return storeCommands{newCensus: func() (brokerstate.WriterCensus, error) {
		return nil, errors.New("this command must not take a census")
	}}
}

// storeProject points HOME and the project at a temporary directory, the way
// every store command resolves them, and returns the paths that follow.
func storeProject(t *testing.T) config.Paths {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WAGGLE_PROJECT_ID", "store-fixture")
	projectID, err := config.ResolveProjectID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	resolved := config.NewPaths(projectID)
	if resolved.DataDir == "" || resolved.DB == "" {
		t.Fatal("project fixture resolved no data paths")
	}
	return resolved
}

// legacyProject is a project whose store is the one an old broker left behind,
// endpoints included.
func legacyProject(t *testing.T) config.Paths {
	t.Helper()
	resolved := storeProject(t)
	for _, dir := range []string{resolved.DataDir, filepath.Dir(resolved.LegacySocket)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeLegacyStore(t, resolved.DB)
	for _, endpoint := range []string{resolved.LegacyPID, resolved.LegacySocket} {
		if err := os.WriteFile(endpoint, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return resolved
}

// runStore executes the real cobra command. Failures are never run this way:
// printErr ends the process, which is why every refusal below is asserted
// against the command's own logic function instead.
func runStore(t *testing.T, commands storeCommands, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	t.Cleanup(func() {
		rootCmd.SetOut(os.Stdout)
		rootCmd.SetErr(os.Stderr)
	})
	command := newStoreCommand(commands)
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SilenceErrors, command.SilenceUsage = true, true
	command.SetArgs(args)
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("waggle store %s: %v (%s)", strings.Join(args, " "), err, stderr.String())
	}
	if stderr.Len() > 0 {
		t.Fatalf("waggle store %s wrote to stderr: %s", strings.Join(args, " "), stderr.String())
	}
	return stdout.String()
}

type inspectionView struct {
	OK                  bool
	Database            string
	Exists              bool
	SchemaVersion       int
	VersionRows         int
	Cutover             string
	Provenance          *brokerstate.Provenance
	LegacyPIDPresent    bool
	LegacySocketPresent bool
	Snapshots           []string
}

type reportView struct {
	OK                    bool
	Database              string
	Snapshot              string
	FromVersion           int
	ToVersion             int
	Tasks                 int
	PreservedLegacyTables []string
	RetiredEndpoints      []string
	CensusScope           string
}

func decode[T any](t *testing.T, output string) T {
	t.Helper()
	var value T
	if err := json.Unmarshal([]byte(output), &value); err != nil {
		t.Fatalf("decode %q: %v", output, err)
	}
	return value
}

func TestStoreInspectReportsAMissingStoreWithoutCreatingIt(t *testing.T) {
	resolved := storeProject(t)

	view := decode[inspectionView](t, runStore(t, refusedCensus(), "inspect"))

	if !view.OK || view.Exists || view.Database != resolved.DB {
		t.Fatalf("inspect of a missing store = %+v", view)
	}
	if view.SchemaVersion != 0 || view.Cutover != "" || view.Provenance != nil {
		t.Fatalf("inspect invented state for a missing store: %+v", view)
	}
	for _, path := range []string{resolved.DB, resolved.DataDir} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("inspect created %s: %v", path, err)
		}
	}
}

func TestStoreConvertPreparesTheLegacyStoreAndActivateAdmitsIt(t *testing.T) {
	resolved := legacyProject(t)
	commands := clearCensus()

	before := decode[inspectionView](t, runStore(t, refusedCensus(), "inspect"))
	if !before.Exists || before.SchemaVersion != config.LegacySchemaVersion || before.Cutover != "" {
		t.Fatalf("legacy store inspected as %+v", before)
	}
	if !before.LegacyPIDPresent || !before.LegacySocketPresent {
		t.Fatalf("inspect missed the retired broker's endpoints: %+v", before)
	}

	report := decode[reportView](t, runStore(t, commands, "convert"))
	if !report.OK || report.Database != resolved.DB ||
		report.FromVersion != config.LegacySchemaVersion || report.ToVersion != config.NativeSchemaVersion {
		t.Fatalf("conversion report = %+v", report)
	}
	if report.Tasks != legacyTaskCount {
		t.Fatalf("conversion carried %d tasks, want %d", report.Tasks, legacyTaskCount)
	}
	if report.CensusScope != fixtureScope {
		t.Fatalf("report census scope = %q; the command took its own census", report.CensusScope)
	}
	if len(report.RetiredEndpoints) != 2 {
		t.Fatalf("retired endpoints = %v", report.RetiredEndpoints)
	}
	if _, err := os.Lstat(report.Snapshot); err != nil {
		t.Fatalf("reported snapshot: %v", err)
	}

	prepared := decode[inspectionView](t, runStore(t, refusedCensus(), "inspect"))
	if prepared.SchemaVersion != config.NativeSchemaVersion || prepared.VersionRows != 1 || prepared.Cutover != "prepared" {
		t.Fatalf("converted store inspected as %+v", prepared)
	}
	if prepared.Provenance == nil || prepared.Provenance.SourceSchema != config.LegacySchemaVersion ||
		prepared.Provenance.Snapshot != report.Snapshot {
		t.Fatalf("converted store records provenance %+v", prepared.Provenance)
	}
	if prepared.LegacyPIDPresent || prepared.LegacySocketPresent {
		t.Fatalf("retired endpoints survived conversion: %+v", prepared)
	}
	if len(prepared.Snapshots) != 1 || prepared.Snapshots[0] != report.Snapshot {
		t.Fatalf("snapshot listing = %v", prepared.Snapshots)
	}

	activated := decode[struct {
		OK      bool
		Message string
	}](t, runStore(t, refusedCensus(), "activate"))
	if !activated.OK || activated.Message != "store activated" {
		t.Fatalf("activation reported %+v", activated)
	}

	active := decode[inspectionView](t, runStore(t, refusedCensus(), "inspect"))
	if active.Cutover != "active" || active.SchemaVersion != config.NativeSchemaVersion {
		t.Fatalf("activated store inspected as %+v", active)
	}

	again := decode[struct {
		OK      bool
		Message string
	}](t, runStore(t, refusedCensus(), "activate"))
	if !again.OK || again.Message != "store already active" {
		t.Fatalf("second activation reported %+v", again)
	}
}

func TestStoreActivateReleasesOwnershipSoTheBrokerCanStart(t *testing.T) {
	resolved := legacyProject(t)
	if _, code, err := clearCensus().convert(t.Context()); err != nil {
		t.Fatal(code, err)
	}
	if _, code, err := refusedCensus().activate(t.Context()); err != nil {
		t.Fatal(code, err)
	}

	// The store must be free the moment activation returns: a retained owner
	// row would keep the next broker out until its process was proven exited.
	owner, err := brokerstate.Acquire(t.Context(), config.NewOwnershipConfig(resolved.DB, config.OpenStore), brokerstate.OSProcessInspector{})
	if err != nil {
		t.Fatalf("activation retained ownership: %v", err)
	}
	owner.BeginShutdown(nil)
	if err := owner.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestStoreRollbackRestoresTheStoreItConverted(t *testing.T) {
	resolved := legacyProject(t)
	commands := clearCensus()

	converted := decode[reportView](t, runStore(t, commands, "convert"))

	report := decode[reportView](t, runStore(t, commands, "rollback"))
	if !report.OK || report.Database != resolved.DB || report.Snapshot != converted.Snapshot {
		t.Fatalf("rollback report = %+v", report)
	}
	if report.FromVersion != config.NativeSchemaVersion || report.ToVersion != config.LegacySchemaVersion {
		t.Fatalf("rollback moved %d → %d", report.FromVersion, report.ToVersion)
	}
	if report.Tasks != legacyTaskCount {
		t.Fatalf("rollback restored %d tasks, want %d", report.Tasks, legacyTaskCount)
	}

	restored := decode[inspectionView](t, runStore(t, refusedCensus(), "inspect"))
	if restored.SchemaVersion != config.LegacySchemaVersion || restored.VersionRows != 1 || restored.Cutover != "" {
		t.Fatalf("restored store inspected as %+v", restored)
	}
}

func TestStoreRollbackRefusesAnActivatedStore(t *testing.T) {
	legacyProject(t)
	commands := clearCensus()
	if _, code, err := commands.convert(t.Context()); err != nil {
		t.Fatal(code, err)
	}
	if _, code, err := refusedCensus().activate(t.Context()); err != nil {
		t.Fatal(code, err)
	}

	result, code, err := commands.rollback(t.Context())
	if err == nil || result != nil {
		t.Fatalf("rollback of an activated store returned %v (%v)", result, err)
	}
	if code != "NOT_PREPARED" || !errors.Is(err, brokerstate.ErrNotPrepared) {
		t.Fatalf("rollback of an activated store reported %s: %v", code, err)
	}

	view := decode[inspectionView](t, runStore(t, refusedCensus(), "inspect"))
	if view.Cutover != "active" || view.SchemaVersion != config.NativeSchemaVersion {
		t.Fatalf("refused rollback changed the store: %+v", view)
	}
}

func TestStoreConvertRefusalsCarryTheirOwnCode(t *testing.T) {
	writer := brokerstate.Handle{PID: os.Getpid() + 1, Command: "waggle", Path: "state.db"}
	for _, testCase := range []struct {
		name     string
		commands storeCommands
		arrange  func(*testing.T) config.Paths
		code     string
		is       error
	}{
		{
			name:     "census tools unavailable",
			commands: storeCommands{newCensus: func() (brokerstate.WriterCensus, error) { return nil, brokerstate.ErrCensusUnavailable }},
			arrange:  legacyProject,
			code:     "CONVERSION_BLOCKED",
			is:       brokerstate.ErrCensusUnavailable,
		},
		{
			name: "census cannot answer",
			commands: storeCommands{newCensus: func() (brokerstate.WriterCensus, error) {
				return fakeCensus{err: errors.New("lsof exploded")}, nil
			}},
			arrange: legacyProject,
			code:    "CONVERSION_BLOCKED",
			is:      brokerstate.ErrCensusUnavailable,
		},
		{
			name: "old writer still running",
			commands: storeCommands{newCensus: func() (brokerstate.WriterCensus, error) {
				return fakeCensus{processes: []brokerstate.Handle{writer}}, nil
			}},
			arrange: legacyProject,
			code:    "CONVERSION_BLOCKED",
			is:      brokerstate.ErrWritersPresent,
		},
		{
			name:     "no store to convert",
			commands: clearCensus(),
			arrange:  storeProject,
			code:     "CONVERSION_FAILED",
			is:       os.ErrNotExist,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resolved := testCase.arrange(t)
			result, code, err := testCase.commands.convert(t.Context())
			if err == nil || result != nil {
				t.Fatalf("refused conversion returned %v (%v)", result, err)
			}
			if code != testCase.code || !errors.Is(err, testCase.is) {
				t.Fatalf("conversion reported %s: %v", code, err)
			}
			if _, statErr := os.Lstat(resolved.SnapshotDir); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("a refused conversion left snapshots behind: %v", statErr)
			}
		})
	}
}

// A store that was already converted is the mistake an operator is most likely
// to make twice, and "not a legacy store" is the only honest answer to it,
// whether the store is still prepared or already carrying a broker.
func TestStoreConvertRefusesAnAlreadyConvertedStore(t *testing.T) {
	for _, activate := range []bool{false, true} {
		name := "prepared"
		if activate {
			name = "active"
		}
		t.Run(name, func(t *testing.T) {
			legacyProject(t)
			commands := clearCensus()
			if _, code, err := commands.convert(t.Context()); err != nil {
				t.Fatal(code, err)
			}
			if activate {
				if _, code, err := refusedCensus().activate(t.Context()); err != nil {
					t.Fatal(code, err)
				}
			}

			result, code, err := commands.convert(t.Context())
			if err == nil || result != nil {
				t.Fatalf("second conversion returned %v (%v)", result, err)
			}
			if code != "NOT_LEGACY" || !errors.Is(err, brokerstate.ErrNotLegacy) {
				t.Fatalf("second conversion reported %s: %v", code, err)
			}
		})
	}
}

func TestStoreActivateRefusesALiveBrokerAndAnUnconvertedStore(t *testing.T) {
	t.Run("live broker", func(t *testing.T) {
		resolved := legacyProject(t)
		if _, code, err := clearCensus().convert(t.Context()); err != nil {
			t.Fatal(code, err)
		}
		owner, err := brokerstate.Acquire(t.Context(), config.NewOwnershipConfig(resolved.DB, config.OpenStore), brokerstate.OSProcessInspector{})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			owner.BeginShutdown(nil)
			if err := owner.Wait(context.Background()); err != nil {
				t.Error(err)
			}
		}()

		result, code, err := refusedCensus().activate(t.Context())
		if err == nil || result != nil {
			t.Fatalf("activation against a live owner returned %v (%v)", result, err)
		}
		if code != "BROKER_RUNNING" || !errors.Is(err, brokerstate.ErrOwnerAlive) {
			t.Fatalf("activation against a live owner reported %s: %v", code, err)
		}
	})

	t.Run("unconverted store", func(t *testing.T) {
		legacyProject(t)
		result, code, err := refusedCensus().activate(t.Context())
		if err == nil || result != nil {
			t.Fatalf("activation of a legacy store returned %v (%v)", result, err)
		}
		if code != "ACTIVATION_FAILED" || !errors.Is(err, brokerstate.ErrSchemaVersion) {
			t.Fatalf("activation of a legacy store reported %s: %v", code, err)
		}
	})

	t.Run("no store", func(t *testing.T) {
		storeProject(t)
		result, code, err := refusedCensus().activate(t.Context())
		if err == nil || result != nil {
			t.Fatalf("activation without a store returned %v (%v)", result, err)
		}
		if code != "ACTIVATION_FAILED" {
			t.Fatalf("activation without a store reported %s: %v", code, err)
		}
	})
}

// Every store command works offline, so none of them may be routed through the
// broker connection the root command opens for service commands.
func TestStoreCommandsNeverReachForTheBroker(t *testing.T) {
	resolved := storeProject(t)

	registered := (*cobra.Command)(nil)
	for _, command := range rootCmd.Commands() {
		if command.Name() == "store" {
			registered = command
		}
	}
	if registered == nil {
		t.Fatal("store is not registered on the root command")
	}
	names := []string{}
	for _, command := range append([]*cobra.Command{registered}, registered.Commands()...) {
		if !isBrokerIndependentCommand(command) {
			t.Fatalf("%s waits for a broker", command.Name())
		}
		names = append(names, command.Name())
	}
	for _, want := range []string{"store", "inspect", "convert", "activate", "rollback"} {
		if !contains(names, want) {
			t.Fatalf("store commands = %v, missing %s", names, want)
		}
	}

	// The registered command, run through the root, with no broker anywhere.
	stdout, stderr := executeRootCommandForTest(t, "store", "inspect")
	if stderr != "" {
		t.Fatalf("store inspect stderr = %q", stderr)
	}
	view := decode[inspectionView](t, stdout)
	if !view.OK || view.Database != resolved.DB {
		t.Fatalf("store inspect through the root command = %+v", view)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// A mistyped subcommand must be refused, not answered with help and a success
// status: an operator part-way through a conversion has to be able to tell that
// nothing happened.
func TestStoreRefusesAnUnknownSubcommand(t *testing.T) {
	storeProject(t)
	var output bytes.Buffer
	command := newStoreCommand(refusedCensus())
	command.SetOut(&output)
	command.SetErr(&output)
	command.SilenceErrors, command.SilenceUsage = true, true
	command.SetArgs([]string{"convertt"})
	err := command.ExecuteContext(t.Context())
	if err == nil {
		t.Fatalf("mistyped subcommand accepted: %s", output.String())
	}
	if !strings.Contains(err.Error(), "convertt") {
		t.Fatalf("refusal does not name the mistyped subcommand: %v", err)
	}
}

// Help must answer from an empty machine: no HOME, no project, no broker.
func TestStoreHelpNeedsNothing(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("WAGGLE_PROJECT_ID", "")
	t.Setenv("WAGGLE_ROOT", "")
	t.Chdir(t.TempDir())
	for _, args := range [][]string{
		{"store", "--help"},
		{"store", "inspect", "--help"},
		{"store", "convert", "--help"},
		{"store", "activate", "--help"},
		{"store", "rollback", "--help"},
	} {
		stdout, stderr, err := executeRootCommandForTestWithError(t, args...)
		if err != nil || stderr != "" {
			t.Fatalf("waggle %s: %v (%s)", strings.Join(args, " "), err, stderr)
		}
		if !strings.Contains(stdout, "waggle store") {
			t.Fatalf("waggle %s printed %q", strings.Join(args, " "), stdout)
		}
	}
}
