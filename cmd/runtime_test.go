package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
	rt "github.com/seungpyoson/waggle/internal/runtime"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestRuntimeSubcommandsSkipBrokerAutoStart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WAGGLE_PROJECT_ID", "proj-runtime")

	originalNoAutoStart := noAutoStart
	originalPaths := paths
	t.Cleanup(func() {
		noAutoStart = originalNoAutoStart
		paths = originalPaths
	})

	noAutoStart = false
	paths = config.NewPaths("stale-project")

	cmd := rootCmd
	cmd.SetArgs([]string{"runtime", "watches"})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute runtime watches: %v", err)
	}

	if paths.ProjectID != "stale-project" {
		t.Fatalf("paths.ProjectID = %q, want broker paths to remain untouched", paths.ProjectID)
	}
}

func TestRuntimeWatchCommandsUseSharedMachineStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Setenv("WAGGLE_PROJECT_ID", "proj-one")
	stdout, stderr := executeRootCommandForTest(t, "runtime", "watch", "agent-a")
	if stderr != "" || !strings.Contains(stdout, `"ok": true`) {
		t.Fatalf("runtime watch proj-one stdout=%q stderr=%q", stdout, stderr)
	}

	t.Setenv("WAGGLE_PROJECT_ID", "proj-two")
	stdout, stderr = executeRootCommandForTest(t, "runtime", "watch", "agent-b")
	if stderr != "" || !strings.Contains(stdout, `"ok": true`) {
		t.Fatalf("runtime watch proj-two stdout=%q stderr=%q", stdout, stderr)
	}

	store := openRuntimeStoreForTest(t)
	watches, err := store.ListWatches()
	if err != nil {
		t.Fatalf("list watches: %v", err)
	}
	if len(watches) != 2 {
		t.Fatalf("watch count = %d, want 2", len(watches))
	}

	stdout, stderr = executeRootCommandForTest(t, "runtime", "watches")
	if stderr != "" {
		t.Fatalf("runtime watches stderr = %q, want empty", stderr)
	}
	var watchesResp struct {
		OK      bool       `json:"ok"`
		Watches []rt.Watch `json:"watches"`
	}
	if err := json.Unmarshal([]byte(stdout), &watchesResp); err != nil {
		t.Fatalf("unmarshal watches response: %v", err)
	}
	if !watchesResp.OK || len(watchesResp.Watches) != 2 {
		t.Fatalf("watches response = %+v, want ok with two watches", watchesResp)
	}
}

func TestRuntimePullReturnsUnreadRecordsAndMarksThemSurfaced(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WAGGLE_PROJECT_ID", "proj-pull")

	store := openRuntimeStoreForTest(t)
	now := time.Now().UTC().Round(time.Second)
	if err := store.AddRecord(rt.DeliveryRecord{
		ProjectID:  "proj-pull",
		AgentName:  "agent-a",
		MessageID:  101,
		FromName:   "sender-1",
		Body:       "first message",
		SentAt:     now.Add(-2 * time.Minute),
		ReceivedAt: now.Add(-time.Minute),
		NotifiedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("add unread record: %v", err)
	}
	if err := store.AddRecord(rt.DeliveryRecord{
		ProjectID:  "proj-pull",
		AgentName:  "agent-a",
		MessageID:  102,
		FromName:   "sender-2",
		Body:       "already surfaced",
		SentAt:     now.Add(-4 * time.Minute),
		ReceivedAt: now.Add(-3 * time.Minute),
		NotifiedAt: now.Add(-3 * time.Minute),
		SurfacedAt: now.Add(-2 * time.Minute),
	}); err != nil {
		t.Fatalf("add surfaced record: %v", err)
	}

	stdout, stderr := executeRootCommandForTest(t, "runtime", "pull", "agent-a")
	if stderr != "" {
		t.Fatalf("runtime pull stderr = %q, want empty", stderr)
	}

	var pullResp struct {
		OK      bool                `json:"ok"`
		Records []rt.DeliveryRecord `json:"records"`
	}
	if err := json.Unmarshal([]byte(stdout), &pullResp); err != nil {
		t.Fatalf("unmarshal pull response: %v", err)
	}
	if !pullResp.OK || len(pullResp.Records) != 1 || pullResp.Records[0].MessageID != 101 {
		t.Fatalf("pull response = %+v, want unread record 101 only", pullResp)
	}

	unread, err := store.Unread("proj-pull", "agent-a")
	if err != nil {
		t.Fatalf("list unread after pull: %v", err)
	}
	if len(unread) != 0 {
		t.Fatalf("unread count after pull = %d, want 0", len(unread))
	}
}

func TestExecuteRootCommandForTestDoesNotLeakRuntimePullProjectID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	store := openRuntimeStoreForTest(t)
	now := time.Now().UTC().Round(time.Second)
	for _, rec := range []rt.DeliveryRecord{
		{
			ProjectID:  "proj-flag",
			AgentName:  "agent-a",
			MessageID:  101,
			FromName:   "sender-flag",
			Body:       "from explicit project",
			SentAt:     now.Add(-2 * time.Minute),
			ReceivedAt: now.Add(-1 * time.Minute),
			NotifiedAt: now.Add(-1 * time.Minute),
		},
		{
			ProjectID:  "proj-env",
			AgentName:  "agent-a",
			MessageID:  202,
			FromName:   "sender-env",
			Body:       "from env project",
			SentAt:     now.Add(-4 * time.Minute),
			ReceivedAt: now.Add(-3 * time.Minute),
			NotifiedAt: now.Add(-3 * time.Minute),
		},
	} {
		if err := store.AddRecord(rec); err != nil {
			t.Fatalf("add unread record: %v", err)
		}
	}

	stdout, stderr := executeRootCommandForTest(t, "runtime", "pull", "agent-a", "--project-id", "proj-flag")
	if stderr != "" {
		t.Fatalf("runtime pull with explicit project stderr = %q, want empty", stderr)
	}

	var firstPull struct {
		OK      bool                `json:"ok"`
		Records []rt.DeliveryRecord `json:"records"`
	}
	if err := json.Unmarshal([]byte(stdout), &firstPull); err != nil {
		t.Fatalf("unmarshal first pull response: %v", err)
	}
	if !firstPull.OK || len(firstPull.Records) != 1 || firstPull.Records[0].MessageID != 101 {
		t.Fatalf("first pull response = %+v, want unread record 101 only", firstPull)
	}

	t.Setenv("WAGGLE_PROJECT_ID", "proj-env")
	stdout, stderr = executeRootCommandForTest(t, "runtime", "pull", "agent-a")
	if stderr != "" {
		t.Fatalf("runtime pull with env project stderr = %q, want empty", stderr)
	}

	var secondPull struct {
		OK      bool                `json:"ok"`
		Records []rt.DeliveryRecord `json:"records"`
	}
	if err := json.Unmarshal([]byte(stdout), &secondPull); err != nil {
		t.Fatalf("unmarshal second pull response: %v", err)
	}
	if !secondPull.OK || len(secondPull.Records) != 1 || secondPull.Records[0].MessageID != 202 {
		t.Fatalf("second pull response = %+v, want unread record 202 only", secondPull)
	}
}

func TestRuntimeStatusReportsStoppedWhenStateMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	stdout, stderr := executeRootCommandForTest(t, "runtime", "status")
	if stderr != "" {
		t.Fatalf("runtime status stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, `"running": false`) {
		t.Fatalf("runtime status = %q, want running false", stdout)
	}
	if !strings.Contains(stdout, `"recent_errors": []`) {
		t.Fatalf("runtime status = %q, want empty recent_errors array", stdout)
	}
}

func TestRuntimeStatusIncludesRecentErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	paths := config.NewPaths("")
	wantRecentErrors := []rt.ErrorEntry{{
		Timestamp: time.Now().UTC().Round(time.Second),
		WatchKey:  "watch-1",
		Error:     "boom",
	}}
	if err := rt.SaveState(paths, rt.State{
		PID:          123,
		Running:      true,
		StartedAt:    time.Now().UTC().Round(time.Second),
		WatchCount:   1,
		LastError:    "watch-1: boom",
		RecentErrors: wantRecentErrors,
	}); err != nil {
		t.Fatalf("save runtime state: %v", err)
	}

	stdout, stderr := executeRootCommandForTest(t, "runtime", "status")
	if stderr != "" {
		t.Fatalf("runtime status stderr = %q, want empty", stderr)
	}

	var resp map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal runtime status response: %v", err)
	}
	rawRecentErrors, ok := resp["recent_errors"]
	if !ok {
		t.Fatalf("runtime status response missing recent_errors: %s", stdout)
	}

	var gotRecentErrors []rt.ErrorEntry
	if err := json.Unmarshal(rawRecentErrors, &gotRecentErrors); err != nil {
		t.Fatalf("unmarshal recent_errors: %v", err)
	}
	if len(gotRecentErrors) != 1 {
		t.Fatalf("recent_errors length = %d, want 1", len(gotRecentErrors))
	}
	if gotRecentErrors[0].WatchKey != wantRecentErrors[0].WatchKey || gotRecentErrors[0].Error != wantRecentErrors[0].Error || !gotRecentErrors[0].Timestamp.Equal(wantRecentErrors[0].Timestamp) {
		t.Fatalf("recent_errors[0] = %+v, want %+v", gotRecentErrors[0], wantRecentErrors[0])
	}
}

func TestExecuteRootCommandForTestDoesNotLeakInstallUninstallFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	stdout, stderr := executeRootCommandForTest(t, "install", "codex", "--uninstall")
	if stderr != "" {
		t.Fatalf("install uninstall stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, `"Codex integration removed"`) {
		t.Fatalf("install uninstall stdout = %q, want removal message", stdout)
	}

	stdout, stderr = executeRootCommandForTest(t, "install", "codex")
	if stderr != "" {
		t.Fatalf("install stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, `"Codex integration installed. Restart Codex to activate."`) {
		t.Fatalf("install stdout = %q, want install message", stdout)
	}
}

func TestUninstallAllPurgeRemovesIntegrationsAndState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if stdout, stderr := executeRootCommandForTest(t, "install", "codex"); stderr != "" || !strings.Contains(stdout, `"ok": true`) {
		t.Fatalf("install codex stdout=%q stderr=%q", stdout, stderr)
	}
	if stdout, stderr := executeRootCommandForTest(t, "install", "gemini"); stderr != "" || !strings.Contains(stdout, `"ok": true`) {
		t.Fatalf("install gemini stdout=%q stderr=%q", stdout, stderr)
	}
	if err := os.MkdirAll(filepath.Join(home, ".waggle", "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".waggle", "runtime", "runtime.db"), []byte("state"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr := executeRootCommandForTest(t, "uninstall", "--all", "--purge")
	if stderr != "" {
		t.Fatalf("uninstall stderr = %q, want empty", stderr)
	}
	for _, want := range []string{"claude-code", "codex", "gemini", "auggie", "augment", "shell-hook", ".waggle"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("uninstall stdout = %q, want action for %q", stdout, want)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "skills", "waggle-runtime")); !os.IsNotExist(err) {
		t.Fatalf("Codex skill should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".waggle")); !os.IsNotExist(err) {
		t.Fatalf(".waggle should be removed, stat err = %v", err)
	}
}

func TestUninstallAllPurgeDryRunDoesNotMutate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if stdout, stderr := executeRootCommandForTest(t, "install", "codex"); stderr != "" || !strings.Contains(stdout, `"ok": true`) {
		t.Fatalf("install codex stdout=%q stderr=%q", stdout, stderr)
	}
	if err := os.MkdirAll(filepath.Join(home, ".waggle", "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout, stderr := executeRootCommandForTest(t, "uninstall", "--all", "--purge", "--dry-run")
	if stderr != "" {
		t.Fatalf("uninstall dry-run stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, `"dry_run": true`) || !strings.Contains(stdout, "would remove state") {
		t.Fatalf("uninstall dry-run stdout = %q, want dry-run planned actions", stdout)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "skills", "waggle-runtime", "SKILL.md")); err != nil {
		t.Fatalf("Codex skill should remain after dry-run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".waggle")); err != nil {
		t.Fatalf(".waggle should remain after dry-run: %v", err)
	}
}

func TestRunUninstallAllAttemptsEveryIntegrationBeforeReturningErrors(t *testing.T) {
	originalTargets := uninstallTargets
	t.Cleanup(func() {
		uninstallTargets = originalTargets
	})

	var called []string
	uninstallTargets = []struct {
		name string
		fn   func() error
	}{
		{
			name: "first",
			fn: func() error {
				called = append(called, "first")
				return fmt.Errorf("first failed")
			},
		},
		{
			name: "second",
			fn: func() error {
				called = append(called, "second")
				return nil
			},
		},
		{
			name: "third",
			fn: func() error {
				called = append(called, "third")
				return fmt.Errorf("third failed")
			},
		},
	}

	actions, err := runUninstall(t.TempDir(), true, false, false)
	if err == nil {
		t.Fatal("runUninstall error = nil, want joined uninstall errors")
	}
	if !strings.Contains(err.Error(), "uninstall first") || !strings.Contains(err.Error(), "uninstall third") {
		t.Fatalf("runUninstall error = %v, want both uninstall failures", err)
	}
	if strings.Join(called, ",") != "first,second,third" {
		t.Fatalf("called targets = %v, want all targets attempted", called)
	}
	if len(actions) != 3 {
		t.Fatalf("actions len = %d, want 3", len(actions))
	}
}

func TestRunUninstallAllPurgeRemovesStateAfterIntegrationErrors(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".waggle", "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}

	originalTargets := uninstallTargets
	originalStopRuntime := uninstallStopRuntime
	t.Cleanup(func() {
		uninstallTargets = originalTargets
		uninstallStopRuntime = originalStopRuntime
	})

	uninstallTargets = []struct {
		name string
		fn   func() error
	}{
		{
			name: "failing",
			fn: func() error {
				return fmt.Errorf("integration failed")
			},
		},
	}
	uninstallStopRuntime = func() error {
		return nil
	}

	actions, err := runUninstall(home, true, true, false)
	if err == nil {
		t.Fatal("runUninstall error = nil, want integration error")
	}
	if !strings.Contains(err.Error(), "uninstall failing") {
		t.Fatalf("runUninstall error = %v, want uninstall failure", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".waggle")); !os.IsNotExist(statErr) {
		t.Fatalf(".waggle stat error = %v, want removed", statErr)
	}
	if len(actions) != 3 {
		t.Fatalf("actions len = %d, want integration, runtime stop, and state removal", len(actions))
	}
}

func TestUninstallAllPurgeReportsActionsAfterIntegrationErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".waggle", "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}

	originalTargets := uninstallTargets
	originalStopRuntime := uninstallStopRuntime
	t.Cleanup(func() {
		uninstallTargets = originalTargets
		uninstallStopRuntime = originalStopRuntime
	})

	uninstallTargets = []struct {
		name string
		fn   func() error
	}{
		{
			name: "failing",
			fn: func() error {
				return fmt.Errorf("integration failed")
			},
		},
	}
	uninstallStopRuntime = func() error {
		return nil
	}

	stdout, _, err := executeRootCommandForTestWithError(t, "uninstall", "--all", "--purge")
	if err == nil {
		t.Fatal("uninstall error = nil, want integration failure")
	}

	var resp struct {
		OK      bool             `json:"ok"`
		Code    string           `json:"code"`
		Error   string           `json:"error"`
		Actions []map[string]any `json:"actions"`
	}
	if unmarshalErr := json.Unmarshal([]byte(stdout), &resp); unmarshalErr != nil {
		t.Fatalf("unmarshal uninstall response: %v\nstdout=%s", unmarshalErr, stdout)
	}
	if resp.OK || resp.Code != "UNINSTALL_ERROR" || !strings.Contains(resp.Error, "uninstall failing") {
		t.Fatalf("uninstall response = %+v, want structured uninstall error", resp)
	}
	if len(resp.Actions) != 3 {
		t.Fatalf("actions len = %d, want integration, runtime stop, and state removal", len(resp.Actions))
	}
	if _, statErr := os.Stat(filepath.Join(home, ".waggle")); !os.IsNotExist(statErr) {
		t.Fatalf(".waggle stat error = %v, want removed", statErr)
	}
}

func TestUninstallPurgeStopsRuntimeBeforeRemovingState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := os.MkdirAll(filepath.Join(home, ".waggle", "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".waggle", "runtime", "runtime.db"), []byte("state"), 0o600); err != nil {
		t.Fatal(err)
	}

	originalStopRuntime := uninstallStopRuntime
	t.Cleanup(func() {
		uninstallStopRuntime = originalStopRuntime
	})

	stoppedBeforeRemove := false
	uninstallStopRuntime = func() error {
		if _, err := os.Stat(filepath.Join(home, ".waggle")); err != nil {
			t.Fatalf("runtime stop ran after state removal: %v", err)
		}
		stoppedBeforeRemove = true
		return nil
	}

	stdout, stderr := executeRootCommandForTest(t, "uninstall", "--purge")
	if stderr != "" {
		t.Fatalf("uninstall stderr = %q, want empty", stderr)
	}
	if !stoppedBeforeRemove {
		t.Fatal("runtime stop was not called before purge")
	}
	if !strings.Contains(stdout, "runtime-daemon") || !strings.Contains(stdout, "stop if running") {
		t.Fatalf("uninstall stdout = %q, want runtime stop action", stdout)
	}
}

func TestIsAlreadyExitedProcessError(t *testing.T) {
	if !isAlreadyExitedProcessError(os.ErrProcessDone) {
		t.Fatal("os.ErrProcessDone should be treated as already exited")
	}
	if !isAlreadyExitedProcessError(fmt.Errorf("wrapped: %w", syscall.ESRCH)) {
		t.Fatal("wrapped ESRCH should be treated as already exited")
	}
	if isAlreadyExitedProcessError(syscall.EPERM) {
		t.Fatal("EPERM should not be treated as already exited")
	}
}

type commandTestState struct {
	paths config.Paths
}

func captureCommandTestState() commandTestState {
	return commandTestState{
		paths: paths,
	}
}

func (s commandTestState) restore() {
	paths = s.paths
	resetCommandFlagState(rootCmd)
}

func resetCommandFlagState(cmd *cobra.Command) {
	cmd.Flags().VisitAll(resetFlagToDefault)
	cmd.PersistentFlags().VisitAll(resetFlagToDefault)
	for _, child := range cmd.Commands() {
		resetCommandFlagState(child)
	}
}

func resetFlagToDefault(flag *pflag.Flag) {
	_ = flag.Value.Set(flag.DefValue)
	flag.Changed = false
}

func executeRootCommandForTest(t *testing.T, args ...string) (string, string) {
	t.Helper()
	stdout, stderr, err := executeRootCommandForTestWithError(t, args...)
	if err != nil {
		t.Fatalf("execute %v: %v", args, err)
	}

	return stdout, stderr
}

func executeRootCommandForTestWithError(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	originalState := captureCommandTestState()
	defer func() {
		originalState.restore()
		rootCmd.SetOut(os.Stdout)
		rootCmd.SetErr(os.Stderr)
		rootCmd.SetArgs(nil)
	}()

	resetCommandFlagState(rootCmd)
	paths = config.Paths{}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs(args)

	err := rootCmd.Execute()
	return stdout.String(), stderr.String(), err
}

func openRuntimeStoreForTest(t *testing.T) *rt.Store {
	t.Helper()

	paths := config.NewPaths("")
	store, err := rt.NewStore(paths.RuntimeDB)
	if err != nil {
		t.Fatalf("open runtime store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}
