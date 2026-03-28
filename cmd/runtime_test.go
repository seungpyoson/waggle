package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
	rt "github.com/seungpyoson/waggle/internal/runtime"
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

func TestRuntimeStatusReportsStoppedWhenStateMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	stdout, stderr := executeRootCommandForTest(t, "runtime", "status")
	if stderr != "" {
		t.Fatalf("runtime status stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, `"running": false`) {
		t.Fatalf("runtime status = %q, want running false", stdout)
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

func executeRootCommandForTest(t *testing.T, args ...string) (string, string) {
	t.Helper()

	originalNoAutoStart := noAutoStart
	originalPaths := paths
	originalInstallUninstall := installUninstall
	originalAdapterBootstrapTool := adapterBootstrapTool
	originalAdapterBootstrapAgent := adapterBootstrapAgent
	originalAdapterBootstrapProjectID := adapterBootstrapProjectID
	originalAdapterBootstrapSource := adapterBootstrapSource
	originalAdapterBootstrapFormat := adapterBootstrapFormat
	defer func() {
		noAutoStart = originalNoAutoStart
		paths = originalPaths
		installUninstall = originalInstallUninstall
		adapterBootstrapTool = originalAdapterBootstrapTool
		adapterBootstrapAgent = originalAdapterBootstrapAgent
		adapterBootstrapProjectID = originalAdapterBootstrapProjectID
		adapterBootstrapSource = originalAdapterBootstrapSource
		adapterBootstrapFormat = originalAdapterBootstrapFormat
		rootCmd.SetOut(os.Stdout)
		rootCmd.SetErr(os.Stderr)
		rootCmd.SetArgs(nil)
	}()

	noAutoStart = false
	paths = config.Paths{}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs(args)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute %v: %v", args, err)
	}

	return stdout.String(), stderr.String()
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
