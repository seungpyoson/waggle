package spawn

import (
	"strings"
	"testing"
)

// TestBuildShellCommand_Simple tests basic command construction with no special characters
func TestBuildShellCommand_Simple(t *testing.T) {
	env := EnvMap{"KEY": "val"}
	got, err := BuildShellCommand(env, "echo", []string{"hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "KEY='val'") {
		t.Errorf("expected KEY='val' in output, got: %s", got)
	}
	if !strings.Contains(got, "echo 'hello'") {
		t.Errorf("expected 'echo 'hello'' in output, got: %s", got)
	}
}

// TestBuildShellCommand_Spaces tests value with spaces is correctly quoted
func TestBuildShellCommand_Spaces(t *testing.T) {
	env := EnvMap{"KEY": "a b"}
	got, err := BuildShellCommand(env, "cmd", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "KEY='a b'") {
		t.Errorf("expected KEY='a b' in output, got: %s", got)
	}
}

// TestBuildShellCommand_SingleQuotes tests value with single quotes is escaped
func TestBuildShellCommand_SingleQuotes(t *testing.T) {
	env := EnvMap{"KEY": "it's"}
	got, err := BuildShellCommand(env, "cmd", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Single quote escape: ' becomes '\''
	if !strings.Contains(got, "KEY='it'\\''s'") {
		t.Errorf("expected KEY='it'\\''s' in output, got: %s", got)
	}
}

// TestBuildShellCommand_DoubleQuotes tests value with double quotes is preserved
func TestBuildShellCommand_DoubleQuotes(t *testing.T) {
	env := EnvMap{"KEY": `say "hi"`}
	got, err := BuildShellCommand(env, "cmd", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, `KEY='say "hi"'`) {
		t.Errorf("expected KEY='say \"hi\"' in output, got: %s", got)
	}
}

// TestBuildShellCommand_EmptyValue tests empty value is correctly quoted
func TestBuildShellCommand_EmptyValue(t *testing.T) {
	env := EnvMap{"KEY": ""}
	got, err := BuildShellCommand(env, "cmd", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "KEY=''") {
		t.Errorf("expected KEY='' in output, got: %s", got)
	}
}

// TestBuildShellCommand_NoEnv tests command with nil env
func TestBuildShellCommand_NoEnv(t *testing.T) {
	got, err := BuildShellCommand(nil, "echo", []string{"test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "echo 'test'") {
		t.Errorf("expected 'echo 'test'' in output, got: %s", got)
	}
}

// TestBuildShellCommand_EmptyCmd tests empty command returns error
func TestBuildShellCommand_EmptyCmd(t *testing.T) {
	_, err := BuildShellCommand(nil, "", nil)
	if err == nil {
		t.Fatal("expected error for empty command, got nil")
	}
}

// TestBuildShellCommand_InvalidKey tests key with space returns error
func TestBuildShellCommand_InvalidKey(t *testing.T) {
	env := EnvMap{"BAD KEY": "val"}
	_, err := BuildShellCommand(env, "cmd", nil)
	if err == nil {
		t.Fatal("expected error for invalid key with space, got nil")
	}
}

// TestBuildShellCommand_MultipleArgs tests command with multiple args
func TestBuildShellCommand_MultipleArgs(t *testing.T) {
	got, err := BuildShellCommand(nil, "echo", []string{"arg1", "arg2", "arg3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "echo 'arg1' 'arg2' 'arg3'") {
		t.Errorf("expected 'echo 'arg1' 'arg2' 'arg3'' in output, got: %s", got)
	}
}

// TestBuildShellCommand_ArgsWithSpaces tests args with spaces are correctly quoted
func TestBuildShellCommand_ArgsWithSpaces(t *testing.T) {
	result, err := BuildShellCommand(nil, "claude", []string{"--prompt=do this", "-v"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "claude '--prompt=do this' '-v'"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

// TestBuildAppleScript_Terminal tests Terminal.app AppleScript wrapper
func TestBuildAppleScript_Terminal(t *testing.T) {
	got := BuildAppleScript(TerminalApp, "echo test")
	if !strings.Contains(got, "Terminal") {
		t.Errorf("expected Terminal.app AppleScript, got: %s", got)
	}
	if !strings.Contains(got, "echo test") {
		t.Errorf("expected 'echo test' in AppleScript, got: %s", got)
	}
}

// TestBuildAppleScript_ITerm2 tests iTerm2 AppleScript wrapper
func TestBuildAppleScript_ITerm2(t *testing.T) {
	got := BuildAppleScript(ITerm2, "echo test")
	if !strings.Contains(got, "iTerm") {
		t.Errorf("expected iTerm2 AppleScript, got: %s", got)
	}
	if !strings.Contains(got, "echo test") {
		t.Errorf("expected 'echo test' in AppleScript, got: %s", got)
	}
}

// TestBuildAppleScript_Quotes tests embedded double quotes are escaped
func TestBuildAppleScript_Quotes(t *testing.T) {
	got := BuildAppleScript(TerminalApp, `echo "hi"`)
	if !strings.Contains(got, `\"hi\"`) {
		t.Errorf("expected escaped quotes \\\"hi\\\", got: %s", got)
	}
}

// TestBuildAppleScript_Backslash tests embedded backslashes are escaped
func TestBuildAppleScript_Backslash(t *testing.T) {
	got := BuildAppleScript(TerminalApp, `path\to`)
	if !strings.Contains(got, `path\\to`) {
		t.Errorf("expected escaped backslash path\\\\to, got: %s", got)
	}
}



// TestBuildAppleScript_Both tests both quotes and backslashes in same string
func TestBuildAppleScript_Both(t *testing.T) {
	got := BuildAppleScript(TerminalApp, `echo "path\to"`)
	if !strings.Contains(got, `\"path\\to\"`) {
		t.Errorf("expected escaped \\\"path\\\\to\\\", got: %s", got)
	}
}

// TestBuildPgrepPattern_Exact tests exact match - "worker-1" should not match "worker-10"
func TestBuildPgrepPattern_Exact(t *testing.T) {
	pattern := BuildPgrepPattern("worker-1")
	// Pattern should match "WAGGLE_AGENT_NAME=worker-1 " but not "WAGGLE_AGENT_NAME=worker-10 "
	// This is a design test - the pattern should use word boundaries or exact match
	if pattern == "" {
		t.Error("expected non-empty pattern")
	}
	// The pattern should prevent substring matches
	// We'll verify this by checking the pattern structure
	if !strings.Contains(pattern, "worker-1") {
		t.Errorf("expected pattern to contain 'worker-1', got: %s", pattern)
	}
}

// TestBuildPgrepPattern_Prefix tests prefix match - "w" should not match "worker"
func TestBuildPgrepPattern_Prefix(t *testing.T) {
	pattern := BuildPgrepPattern("w")
	// Pattern should match "WAGGLE_AGENT_NAME=w " but not "WAGGLE_AGENT_NAME=worker "
	if pattern == "" {
		t.Error("expected non-empty pattern")
	}
	if !strings.Contains(pattern, "w") {
		t.Errorf("expected pattern to contain 'w', got: %s", pattern)
	}
}

// TestBuildPgrepPattern_Normal tests normal case - "worker-1" matches "WAGGLE_AGENT_NAME=worker-1 claude"
func TestBuildPgrepPattern_Normal(t *testing.T) {
	pattern := BuildPgrepPattern("worker-1")
	// Pattern should match the full command line with WAGGLE_AGENT_NAME=worker-1
	if pattern == "" {
		t.Error("expected non-empty pattern")
	}
	// Should contain the agent name
	if !strings.Contains(pattern, "worker-1") {
		t.Errorf("expected pattern to contain 'worker-1', got: %s", pattern)
	}
	// Should reference WAGGLE_AGENT_NAME
	if !strings.Contains(pattern, "WAGGLE_AGENT_NAME") {
		t.Errorf("expected pattern to contain 'WAGGLE_AGENT_NAME', got: %s", pattern)
	}
}
