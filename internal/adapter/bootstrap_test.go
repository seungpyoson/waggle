package adapter

import "testing"

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
