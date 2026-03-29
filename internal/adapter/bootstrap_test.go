package adapter

import (
	"os"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
	rt "github.com/seungpyoson/waggle/internal/runtime"
)

func TestResolveAgentNamePrefersExplicit(t *testing.T) {
	t.Setenv("WAGGLE_AGENT_NAME", "env-agent")

	got := ResolveAgentName("codex", "explicit-agent", "/dev/ttys001", 123, 456)
	if got != "explicit-agent" {
		t.Fatalf("ResolveAgentName() = %q, want explicit-agent", got)
	}
}

func TestResolveAgentNameUsesEnvBeforeTTY(t *testing.T) {
	t.Setenv("WAGGLE_AGENT_NAME", "env-agent")

	got := ResolveAgentName("codex", "", "/dev/ttys001", 123, 456)
	if got != "env-agent" {
		t.Fatalf("ResolveAgentName() = %q, want env-agent", got)
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
