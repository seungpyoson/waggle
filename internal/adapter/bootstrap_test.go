package adapter

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
	rt "github.com/seungpyoson/waggle/internal/runtime"
)

func TestResolveAgentNamePrefersExplicit(t *testing.T) {
	t.Setenv("WAGGLE_AGENT_NAME", "env-agent")

	got := ResolveAgentName("codex", "Explicit-Agent!", "/dev/ttys001", 123, 456)
	if got != "explicit-agent" {
		t.Fatalf("ResolveAgentName() = %q, want explicit-agent (sanitized)", got)
	}
}

func TestResolveAgentNameUsesEnvBeforeTTY(t *testing.T) {
	t.Setenv("WAGGLE_AGENT_NAME", "Env Agent!")

	got := ResolveAgentName("codex", "", "/dev/ttys001", 123, 456)
	if got != "env-agent" {
		t.Fatalf("ResolveAgentName() = %q, want env-agent (sanitized)", got)
	}
}

func TestResolveAgentNameUsesTTYFallback(t *testing.T) {
	got := ResolveAgentName("codex", "", "/dev/ttys001", 123, 456)
	if got != "codex-ttys001" {
		t.Fatalf("ResolveAgentName() = %q, want codex-ttys001", got)
	}
}

func TestResolveAgentNameUsesParentPIDFallback(t *testing.T) {
	got := ResolveAgentName("gemini", "", "", 123, 456)
	if got != "gemini-123" {
		t.Fatalf("ResolveAgentName() = %q, want gemini-123", got)
	}
}

func TestResolveAgentNameFallsBackToPIDWhenParentUnavailable(t *testing.T) {
	got := ResolveAgentName("gemini", "", "", 0, 456)
	if got != "gemini-456" {
		t.Fatalf("ResolveAgentName() = %q, want gemini-456", got)
	}
}

func TestResolveAgentNameSanitizesTTY(t *testing.T) {
	got := ResolveAgentName("claude-code", "", "/dev/pts/3", 0, 456)
	if got != "claude-code-3" {
		t.Fatalf("ResolveAgentName() = %q, want claude-code-3", got)
	}
}

func TestShouldSkipRuntimeStartForTestHonorsEnvUnderTestBinary(t *testing.T) {
	t.Setenv("WAGGLE_ADAPTER_SKIP_RUNTIME_START", "1")

	if !shouldSkipRuntimeStartForTest() {
		t.Fatalf("shouldSkipRuntimeStartForTest() = false, want true for test binary with env flag")
	}
}

func TestShouldSkipRuntimeStartForTestRequiresEnvFlag(t *testing.T) {
	t.Setenv("WAGGLE_ADAPTER_SKIP_RUNTIME_START", "0")

	if shouldSkipRuntimeStartForTest() {
		t.Fatalf("shouldSkipRuntimeStartForTest() = true, want false without env flag")
	}
}

func TestAdapterBootstrap_RoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WAGGLE_PROJECT_ID", "proj-roundtrip")
	t.Setenv("WAGGLE_ADAPTER_SKIP_RUNTIME_START", "1")

	// Open store for test manipulation
	store, err := rt.OpenStore(config.NewPaths(""))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	// Step 1: Bootstrap agent "codex" — registers watch, gets 0 records
	result1, err := Bootstrap(BootstrapInput{
		Tool: "codex",
	})
	if err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	if result1.Tool != "codex" {
		t.Fatalf("first bootstrap tool = %q, want codex", result1.Tool)
	}
	if len(result1.Records) != 0 {
		t.Fatalf("first bootstrap record count = %d, want 0", len(result1.Records))
	}

	// Step 2: Simulate a delivery record in the store
	now := time.Now().UTC()
	rec := rt.DeliveryRecord{
		ProjectID:  "proj-roundtrip",
		AgentName:  result1.AgentName,
		MessageID:  101,
		FromName:   "orchestrator",
		Body:       "test message",
		SentAt:     now.Add(-2 * time.Minute),
		ReceivedAt: now.Add(-1 * time.Minute),
		NotifiedAt: now,
	}
	if err := store.AddRecord(rec); err != nil {
		t.Fatalf("add record: %v", err)
	}

	// Step 3: Bootstrap agent "codex" again — gets the 1 unread record
	result2, err := Bootstrap(BootstrapInput{
		Tool:      "codex",
		AgentName: result1.AgentName,
	})
	if err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	if len(result2.Records) != 1 {
		t.Fatalf("second bootstrap record count = %d, want 1", len(result2.Records))
	}
	if result2.Records[0].MessageID != 101 {
		t.Fatalf("second bootstrap message id = %d, want 101", result2.Records[0].MessageID)
	}

	// Step 4: Third bootstrap — gets 0 records (already surfaced from prior bootstrap)
	result3, err := Bootstrap(BootstrapInput{
		Tool:      "codex",
		AgentName: result1.AgentName,
	})
	if err != nil {
		t.Fatalf("third bootstrap: %v", err)
	}
	if len(result3.Records) != 0 {
		t.Fatalf("third bootstrap record count = %d, want 0", len(result3.Records))
	}
}

func TestAdapterBootstrap_SkipsGracefullyWithoutProjectContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WAGGLE_PROJECT_ID", "")
	t.Setenv("WAGGLE_ROOT", "")
	t.Setenv("WAGGLE_ADAPTER_SKIP_RUNTIME_START", "1")

	// Change to a non-git directory so project resolution fails.
	origDir, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	result, err := Bootstrap(BootstrapInput{
		Tool: "codex",
	})
	if err != nil {
		t.Fatalf("Bootstrap should not return error in degraded context, got: %v", err)
	}
	if !result.Skipped {
		t.Fatalf("Bootstrap should set Skipped=true in degraded context")
	}
	if result.SkipReason == "" {
		t.Fatalf("Bootstrap should set SkipReason in degraded context")
	}
	if result.Tool != "codex" {
		t.Fatalf("Bootstrap should still set Tool even when skipped, got %q", result.Tool)
	}
}

func TestAdapterBootstrap_SkipsGracefullyWhenRuntimeStoreUnavailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WAGGLE_PROJECT_ID", "proj-runtime-db-unavailable")
	t.Setenv("WAGGLE_ADAPTER_SKIP_RUNTIME_START", "1")

	waggleDir := filepath.Join(home, ".waggle")
	if err := os.MkdirAll(waggleDir, 0o755); err != nil {
		t.Fatalf("create .waggle: %v", err)
	}
	if err := os.WriteFile(filepath.Join(waggleDir, "runtime"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("create runtime path blocker: %v", err)
	}

	result, err := Bootstrap(BootstrapInput{
		Tool:      "codex",
		AgentName: "codex-test",
	})
	if err != nil {
		t.Fatalf("Bootstrap should not return error when runtime store is unavailable, got: %v", err)
	}
	if !result.Skipped {
		t.Fatalf("Bootstrap should set Skipped=true when runtime store is unavailable")
	}
	if result.SkipReason == "" || !strings.Contains(result.SkipReason, "runtime store unavailable") {
		t.Fatalf("Bootstrap skip reason = %q, want runtime store unavailable", result.SkipReason)
	}
	if result.RuntimeError == "" {
		t.Fatalf("Bootstrap should expose runtime error for diagnostics")
	}
}

func TestWriteSessionMapping(t *testing.T) {
	dir := t.TempDir()
	nonce := "12345-1711843200000000000"
	if err := WriteSessionMapping(dir, 12345, nonce, "claude-99", "proj-abc"); err != nil {
		t.Fatal(err)
	}
	sessionData, err := os.ReadFile(filepath.Join(dir, "agent-session-"+nonce))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(sessionData), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), sessionData)
	}
	if lines[0] != "claude-99" {
		t.Fatalf("agent = %q, want claude-99", lines[0])
	}
	if lines[1] != rt.ProjectPathKey("proj-abc") {
		t.Fatalf("project = %q, want %q", lines[1], rt.ProjectPathKey("proj-abc"))
	}

	ppidData, err := os.ReadFile(filepath.Join(dir, "agent-ppid-12345"))
	if err != nil {
		t.Fatal(err)
	}
	if string(ppidData) != nonce+"\n" {
		t.Fatalf("ppid mapping = %q, want %q", string(ppidData), nonce+"\\n")
	}
}

func TestWriteTTYMapping(t *testing.T) {
	dir := t.TempDir()
	nonce := "12345-1711843200000000003"
	if err := WriteTTYMapping(dir, "ttys009", nonce); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "agent-tty-ttys009"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != nonce+"\n" {
		t.Fatalf("tty mapping = %q, want %q", string(data), nonce+"\\n")
	}
}

func TestBootstrapWritesTTYMappingFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TTY", "/dev/ttys009")
	t.Setenv("WAGGLE_PROJECT_ID", "proj-tty")
	t.Setenv("WAGGLE_ADAPTER_SKIP_RUNTIME_START", "1")

	result, err := Bootstrap(BootstrapInput{Tool: "codex"})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if result.AgentName != "codex-ttys009" {
		t.Fatalf("AgentName = %q, want codex-ttys009", result.AgentName)
	}

	runtimeDir := config.NewPaths("").RuntimeDir
	data, err := os.ReadFile(filepath.Join(runtimeDir, "agent-tty-ttys009"))
	if err != nil {
		t.Fatalf("read tty mapping: %v", err)
	}
	nonce := strings.TrimSpace(string(data))
	sessionData, err := os.ReadFile(filepath.Join(runtimeDir, "agent-session-"+nonce))
	if err != nil {
		t.Fatalf("read session mapping: %v", err)
	}
	if !strings.Contains(string(sessionData), "codex-ttys009\n") {
		t.Fatalf("session mapping = %q, want agent name", sessionData)
	}
}

func TestWriteSessionMapping_NoncesAreDifferent(t *testing.T) {
	dir := t.TempDir()
	ppid := 12345
	nonce1 := fmt.Sprintf("%d-%d", ppid, time.Now().UnixNano())
	if err := WriteSessionMapping(dir, ppid, nonce1, "claude-99", "proj-abc"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(time.Nanosecond)

	nonce2 := fmt.Sprintf("%d-%d", ppid, time.Now().UnixNano())
	if err := WriteSessionMapping(dir, ppid, nonce2, "claude-99", "proj-abc"); err != nil {
		t.Fatal(err)
	}

	if nonce1 == nonce2 {
		t.Fatalf("nonces should differ, both were %q", nonce1)
	}
	if _, err := os.Stat(filepath.Join(dir, "agent-session-"+nonce1)); err != nil {
		t.Fatalf("first session file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agent-session-"+nonce2)); err != nil {
		t.Fatalf("second session file missing: %v", err)
	}
}

func TestWriteSessionMapping_StoresOpaqueProjectKey(t *testing.T) {
	dir := t.TempDir()
	nonce := "12345-1711843200000000001"
	projectID := "path:/Users/test/project"

	if err := WriteSessionMapping(dir, 12345, nonce, "claude-99", projectID); err != nil {
		t.Fatal(err)
	}

	sessionData, err := os.ReadFile(filepath.Join(dir, "agent-session-"+nonce))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(sessionData), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), sessionData)
	}
	if lines[1] != rt.ProjectPathKey(projectID) {
		t.Fatalf("project key = %q, want %q", lines[1], rt.ProjectPathKey(projectID))
	}
	if strings.Contains(lines[1], "Users") || strings.Contains(lines[1], "project") {
		t.Fatalf("project key leaked raw project path: %q", lines[1])
	}
}

func TestBootstrap_LogsWhenSessionMappingWriteFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WAGGLE_PROJECT_ID", "proj-log-warning")
	t.Setenv("WAGGLE_ADAPTER_SKIP_RUNTIME_START", "1")
	t.Setenv("WAGGLE_AGENT_PPID", "4242")

	runtimeDir := config.NewPaths("").RuntimeDir
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(runtimeDir, "agent-ppid-4242"), 0o700); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	oldWriter := log.Writer()
	oldFlags := log.Flags()
	oldPrefix := log.Prefix()
	log.SetOutput(&buf)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
		log.SetPrefix(oldPrefix)
	})

	result, err := Bootstrap(BootstrapInput{Tool: "codex"})
	if err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}
	if result.ProjectID != "proj-log-warning" {
		t.Fatalf("ProjectID = %q, want proj-log-warning", result.ProjectID)
	}
	if !strings.Contains(buf.String(), "warning: write session mapping failed") {
		t.Fatalf("expected degraded push delivery warning, got %q", buf.String())
	}
	if !strings.Contains(buf.String(), "push delivery degraded") {
		t.Fatalf("expected push delivery degraded detail, got %q", buf.String())
	}
}

func TestResolveAgentPPID_PrefersEnvVar(t *testing.T) {
	t.Setenv("WAGGLE_AGENT_PPID", "99999")
	got := resolveAgentPPID()
	if got != 99999 {
		t.Fatalf("got %d, want 99999", got)
	}
}

func TestResolveAgentPPID_FallsBackToGetppid(t *testing.T) {
	t.Setenv("WAGGLE_AGENT_PPID", "")
	got := resolveAgentPPID()
	if got != os.Getppid() {
		t.Fatalf("got %d, want %d", got, os.Getppid())
	}
}
