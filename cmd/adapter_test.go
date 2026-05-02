package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ia "github.com/seungpyoson/waggle/internal/adapter"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/install"
	rt "github.com/seungpyoson/waggle/internal/runtime"
)

func TestAdapterBootstrapRegistersWatchAndDerivesTTYAgentName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TTY", "/dev/ttys009")
	t.Setenv("WAGGLE_PROJECT_ID", "proj-bootstrap")
	t.Setenv("WAGGLE_ADAPTER_SKIP_RUNTIME_START", "1")
	installCodexForAdapterCommandTest(t)

	stdout, stderr := executeRootCommandForTest(t, "adapter", "bootstrap", "codex")
	if stderr != "" {
		t.Fatalf("adapter bootstrap stderr = %q, want empty", stderr)
	}

	var resp struct {
		OK             bool                `json:"ok"`
		Tool           string              `json:"tool"`
		ProjectID      string              `json:"project_id"`
		AgentName      string              `json:"agent_name"`
		Source         string              `json:"source"`
		RuntimeRunning bool                `json:"runtime_running"`
		Records        []rt.DeliveryRecord `json:"records"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal bootstrap response: %v", err)
	}

	if !resp.OK {
		t.Fatalf("bootstrap response = %+v, want ok", resp)
	}
	if resp.Tool != "codex" {
		t.Fatalf("tool = %q, want codex", resp.Tool)
	}
	if resp.ProjectID != "proj-bootstrap" {
		t.Fatalf("project_id = %q, want proj-bootstrap", resp.ProjectID)
	}
	if resp.AgentName != "codex-ttys009" {
		t.Fatalf("agent_name = %q, want codex-ttys009", resp.AgentName)
	}
	if resp.Source != "codex-adapter" {
		t.Fatalf("source = %q, want codex-adapter", resp.Source)
	}
	if len(resp.Records) != 0 {
		t.Fatalf("record count = %d, want 0", len(resp.Records))
	}
	if _, err := os.Stat(config.NewPaths("").RuntimePID); !os.IsNotExist(err) {
		t.Fatalf("runtime pid file exists despite WAGGLE_ADAPTER_SKIP_RUNTIME_START=1: err=%v", err)
	}

	store := openRuntimeStoreForTest(t)
	watches, err := store.ListWatches()
	if err != nil {
		t.Fatalf("list watches: %v", err)
	}
	if len(watches) != 1 {
		t.Fatalf("watch count = %d, want 1", len(watches))
	}
	if watches[0].ProjectID != "proj-bootstrap" || watches[0].AgentName != "codex-ttys009" || watches[0].Source != "codex-adapter" {
		t.Fatalf("watch = %+v, want project/agent/source for codex bootstrap", watches[0])
	}
}

func TestAdapterBootstrapReturnsUnreadRecordsAndMarksThemSurfaced(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WAGGLE_PROJECT_ID", "proj-bootstrap")
	t.Setenv("WAGGLE_ADAPTER_SKIP_RUNTIME_START", "1")
	installCodexForAdapterCommandTest(t)

	store := openRuntimeStoreForTest(t)
	now := time.Now().UTC().Round(time.Second)
	if err := store.AddRecord(rt.DeliveryRecord{
		ProjectID:  "proj-bootstrap",
		AgentName:  "worker-a",
		MessageID:  101,
		FromName:   "planner",
		Body:       "implement the adapter seam",
		SentAt:     now.Add(-2 * time.Minute),
		ReceivedAt: now.Add(-1 * time.Minute),
		NotifiedAt: now.Add(-1 * time.Minute),
	}); err != nil {
		t.Fatalf("add unread record: %v", err)
	}

	stdout, stderr := executeRootCommandForTest(t, "adapter", "bootstrap", "--tool", "codex", "--agent", "worker-a")
	if stderr != "" {
		t.Fatalf("adapter bootstrap stderr = %q, want empty", stderr)
	}

	var resp struct {
		OK      bool                `json:"ok"`
		Records []rt.DeliveryRecord `json:"records"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal bootstrap response: %v", err)
	}
	if !resp.OK || len(resp.Records) != 1 || resp.Records[0].MessageID != 101 {
		t.Fatalf("bootstrap response = %+v, want unread message 101", resp)
	}

	unread, err := store.Unread("proj-bootstrap", "worker-a")
	if err != nil {
		t.Fatalf("list unread after bootstrap: %v", err)
	}
	if len(unread) != 0 {
		t.Fatalf("unread count after bootstrap = %d, want 0", len(unread))
	}
}

func TestAdapterBootstrapMarkdownUsesExplicitOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WAGGLE_PROJECT_ID", "proj-default")
	t.Setenv("WAGGLE_ADAPTER_SKIP_RUNTIME_START", "1")
	installCodexForAdapterCommandTest(t)

	stdout, stderr := executeRootCommandForTest(
		t,
		"adapter", "bootstrap",
		"--tool", "Codex CLI",
		"--agent", "worker-a",
		"--project-id", "proj-explicit",
		"--source", "manual-check",
		"--format", "markdown",
	)
	if stderr != "" {
		t.Fatalf("adapter bootstrap stderr = %q, want empty", stderr)
	}

	for _, want := range []string{
		"## Waggle Runtime",
		"- Tool: `codex-cli`",
		"- Project: `proj-explicit`",
		"- Agent: `worker-a`",
		"- Source: `manual-check`",
		"- Unread: `0`",
		"No unread Waggle records.",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("markdown output missing %q:\n%s", want, stdout)
		}
	}
}

func TestAdapterBootstrapDoesNotLeakFlagStateAcrossExecutions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WAGGLE_PROJECT_ID", "proj-bootstrap")
	t.Setenv("TTY", "/dev/ttys009")
	t.Setenv("WAGGLE_ADAPTER_SKIP_RUNTIME_START", "1")
	installCodexForAdapterCommandTest(t)

	stdout, stderr := executeRootCommandForTest(
		t,
		"adapter", "bootstrap",
		"--tool", "codex",
		"--agent", "worker-a",
	)
	if stderr != "" {
		t.Fatalf("first adapter bootstrap stderr = %q, want empty", stderr)
	}

	var firstResp struct {
		OK        bool   `json:"ok"`
		AgentName string `json:"agent_name"`
	}
	if err := json.Unmarshal([]byte(stdout), &firstResp); err != nil {
		t.Fatalf("unmarshal first bootstrap response: %v", err)
	}
	if !firstResp.OK || firstResp.AgentName != "worker-a" {
		t.Fatalf("first bootstrap response = %+v, want explicit worker-a", firstResp)
	}

	stdout, stderr = executeRootCommandForTest(t, "adapter", "bootstrap", "codex")
	if stderr != "" {
		t.Fatalf("second adapter bootstrap stderr = %q, want empty", stderr)
	}

	var secondResp struct {
		OK        bool   `json:"ok"`
		AgentName string `json:"agent_name"`
	}
	if err := json.Unmarshal([]byte(stdout), &secondResp); err != nil {
		t.Fatalf("unmarshal second bootstrap response: %v", err)
	}
	if !secondResp.OK {
		t.Fatalf("second bootstrap response = %+v, want ok", secondResp)
	}
	if secondResp.AgentName != "codex-ttys009" {
		t.Fatalf("agent_name after prior flagged execution = %q, want codex-ttys009", secondResp.AgentName)
	}
}

func TestAdapterBootstrapMarkdownSilentWhenRuntimeStoreUnavailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WAGGLE_PROJECT_ID", "proj-runtime-db-unavailable")
	t.Setenv("WAGGLE_ADAPTER_SKIP_RUNTIME_START", "1")
	installCodexForAdapterCommandTest(t)
	blockRuntimeDirForTest(t, home)

	stdout, stderr := executeRootCommandForTest(t, "adapter", "bootstrap", "codex", "--format", "markdown")
	if stdout != "" {
		t.Fatalf("adapter bootstrap stdout = %q, want empty for degraded markdown startup", stdout)
	}
	if stderr != "" {
		t.Fatalf("adapter bootstrap stderr = %q, want empty for degraded markdown startup", stderr)
	}
}

func TestAdapterBootstrapJSONReportsSkippedWhenRuntimeStoreUnavailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WAGGLE_PROJECT_ID", "proj-runtime-db-unavailable")
	t.Setenv("WAGGLE_ADAPTER_SKIP_RUNTIME_START", "1")
	installCodexForAdapterCommandTest(t)
	blockRuntimeDirForTest(t, home)

	stdout, stderr := executeRootCommandForTest(t, "adapter", "bootstrap", "codex")
	if stderr != "" {
		t.Fatalf("adapter bootstrap stderr = %q, want empty", stderr)
	}

	var resp struct {
		OK         bool   `json:"ok"`
		Skipped    bool   `json:"skipped"`
		SkipReason string `json:"skip_reason"`
		Tool       string `json:"tool"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal skipped bootstrap response: %v", err)
	}
	if resp.OK {
		t.Fatalf("bootstrap response OK = true, want false for skipped runtime store")
	}
	if !resp.Skipped {
		t.Fatalf("bootstrap response skipped = false, want true")
	}
	if !strings.Contains(resp.SkipReason, "runtime store unavailable") {
		t.Fatalf("skip_reason = %q, want runtime store unavailable", resp.SkipReason)
	}
	if resp.Tool != "codex" {
		t.Fatalf("tool = %q, want codex", resp.Tool)
	}
}

func TestWhoamiReturnsMappedAgentIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WAGGLE_AGENT_PPID", "12345")

	runtimeDir := config.NewPaths("").RuntimeDir
	nonce := "12345-1711843200000000002"
	if err := ia.WriteSessionMapping(runtimeDir, 12345, nonce, "codex-ttys001", "proj-whoami"); err != nil {
		t.Fatalf("write session mapping: %v", err)
	}

	stdout, stderr := executeRootCommandForTest(t, "whoami")
	if stderr != "" {
		t.Fatalf("whoami stderr = %q, want empty", stderr)
	}

	var resp struct {
		OK         bool   `json:"ok"`
		AgentName  string `json:"agent_name"`
		ProjectKey string `json:"project_key"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal whoami response: %v", err)
	}
	if !resp.OK || resp.AgentName != "codex-ttys001" || resp.ProjectKey != rt.ProjectPathKey("proj-whoami") {
		t.Fatalf("whoami response = %+v", resp)
	}
}

func TestWhoamiParsesCRLFSessionMapping(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WAGGLE_AGENT_PPID", "12345")

	runtimeDir := config.NewPaths("").RuntimeDir
	nonce := "12345-1711843200000000002"
	if err := ia.WriteSessionMapping(runtimeDir, 12345, nonce, "codex-ttys001", "proj-whoami"); err != nil {
		t.Fatalf("write session mapping: %v", err)
	}
	sessionPath := filepath.Join(runtimeDir, "agent-session-"+nonce)
	if err := os.WriteFile(sessionPath, []byte("codex-ttys001\r\n"+rt.ProjectPathKey("proj-whoami")+"\r\n"), 0o600); err != nil {
		t.Fatalf("rewrite CRLF session mapping: %v", err)
	}

	stdout, stderr := executeRootCommandForTest(t, "whoami")
	if stderr != "" {
		t.Fatalf("whoami stderr = %q, want empty", stderr)
	}

	var resp struct {
		OK         bool   `json:"ok"`
		AgentName  string `json:"agent_name"`
		ProjectKey string `json:"project_key"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal whoami response: %v", err)
	}
	if !resp.OK || resp.AgentName != "codex-ttys001" || resp.ProjectKey != rt.ProjectPathKey("proj-whoami") {
		t.Fatalf("whoami response = %+v", resp)
	}
}

func TestWhoamiFallsBackToTTYMapping(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WAGGLE_AGENT_PPID", "")
	t.Setenv("TTY", "/dev/tty_U0")

	runtimeDir := config.NewPaths("").RuntimeDir
	nonce := "12345-1711843200000003"
	if err := ia.WriteSessionMapping(runtimeDir, 12345, nonce, "codex-tty_u0", "proj-whoami"); err != nil {
		t.Fatalf("write session mapping: %v", err)
	}
	if err := ia.WriteTTYMapping(runtimeDir, "tty_U0", nonce); err != nil {
		t.Fatalf("write tty mapping: %v", err)
	}

	stdout, stderr := executeRootCommandForTest(t, "whoami")
	if stderr != "" {
		t.Fatalf("whoami stderr = %q, want empty", stderr)
	}

	var resp struct {
		OK        bool   `json:"ok"`
		AgentName string `json:"agent_name"`
		Source    string `json:"source"`
		PPID      string `json:"ppid"`
		TTY       string `json:"tty"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal whoami response: %v", err)
	}
	if !resp.OK || resp.AgentName != "codex-tty_u0" || resp.Source != "tty" {
		t.Fatalf("whoami response = %+v", resp)
	}
	if resp.PPID != "" {
		t.Fatalf("whoami ppid = %q, want empty for tty-sourced lookup", resp.PPID)
	}
	if resp.TTY != "tty_u0" {
		t.Fatalf("whoami tty = %q, want tty_u0", resp.TTY)
	}
}

func TestWhoamiRejectsArgs(t *testing.T) {
	err := whoamiCmd.Args(whoamiCmd, []string{"extra"})
	if err == nil {
		t.Fatal("whoami Args accepted unexpected positional argument")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("whoami Args error = %v, want no-args error", err)
	}
}

func TestAdapterBootstrapSkipsUninstalledIntegrationWithoutCreatingState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WAGGLE_PROJECT_ID", "proj-bootstrap")
	t.Setenv("WAGGLE_ADAPTER_SKIP_RUNTIME_START", "1")

	stdout, stderr := executeRootCommandForTest(t, "adapter", "bootstrap", "codex")
	if stderr != "" {
		t.Fatalf("adapter bootstrap stderr = %q, want empty", stderr)
	}

	var resp struct {
		OK         bool   `json:"ok"`
		Skipped    bool   `json:"skipped"`
		SkipReason string `json:"skip_reason"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal skipped bootstrap response: %v", err)
	}
	if resp.OK || !resp.Skipped {
		t.Fatalf("bootstrap response = %+v, want skipped", resp)
	}
	if !strings.Contains(resp.SkipReason, "integration is not installed") {
		t.Fatalf("skip_reason = %q, want not installed", resp.SkipReason)
	}
	if _, err := os.Stat(filepath.Join(home, ".waggle")); !os.IsNotExist(err) {
		t.Fatalf(".waggle should not be recreated for stale bootstrap, stat err = %v", err)
	}
}

func installCodexForAdapterCommandTest(t *testing.T) {
	t.Helper()
	if err := install.InstallCodex(); err != nil {
		t.Fatalf("install Codex integration: %v", err)
	}
}

func blockRuntimeDirForTest(t *testing.T, home string) {
	t.Helper()
	waggleDir := filepath.Join(home, ".waggle")
	if err := os.MkdirAll(waggleDir, 0o755); err != nil {
		t.Fatalf("create .waggle: %v", err)
	}
	if err := os.WriteFile(filepath.Join(waggleDir, "runtime"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("create runtime path blocker: %v", err)
	}
}
