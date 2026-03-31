package runtime

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteSignal_CreatesFileWithContent(t *testing.T) {
	dir := t.TempDir()
	if err := WriteSignal(dir, "proj-test", "agent-1", "alice", "hello", 65536); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ProjectPathKey("proj-test"), "agent-1"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "alice") || !strings.Contains(s, "hello") {
		t.Fatalf("content = %q", s)
	}
}

func TestWriteSignal_Appends(t *testing.T) {
	dir := t.TempDir()
	WriteSignal(dir, "proj-test", "a", "alice", "one", 65536)
	WriteSignal(dir, "proj-test", "a", "bob", "two", 65536)
	data, _ := os.ReadFile(filepath.Join(dir, ProjectPathKey("proj-test"), "a"))
	if c := strings.Count(string(data), "\n"); c != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", c, data)
	}
}

func TestWriteSignal_DropsWhenOverCap(t *testing.T) {
	dir := t.TempDir()
	// First write fits (short message ~38 bytes, cap 100)
	WriteSignal(dir, "proj-test", "a", "alice", "hello", 100)
	data, _ := os.ReadFile(filepath.Join(dir, ProjectPathKey("proj-test"), "a"))
	if !strings.Contains(string(data), "alice") {
		t.Fatalf("first write should succeed, got: %q", data)
	}
	// Second write pushes past cap
	WriteSignal(dir, "proj-test", "a", "bob", strings.Repeat("x", 100), 100)
	data, _ = os.ReadFile(filepath.Join(dir, ProjectPathKey("proj-test"), "a"))
	if strings.Contains(string(data), "bob") {
		t.Fatalf("second write should be dropped, got: %q", data)
	}
}

func TestWriteSignal_ReturnsCapExceededError(t *testing.T) {
	dir := t.TempDir()

	if err := WriteSignal(dir, "proj-test", "a", "alice", "hello", 100); err != nil {
		t.Fatal(err)
	}

	err := WriteSignal(dir, "proj-test", "a", "bob", strings.Repeat("x", 100), 100)
	if !errors.Is(err, ErrSignalCapExceeded) {
		t.Fatalf("WriteSignal() error = %v, want %v", err, ErrSignalCapExceeded)
	}

	data, readErr := os.ReadFile(filepath.Join(dir, ProjectPathKey("proj-test"), "a"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(data), "bob") {
		t.Fatalf("cap-exceeded write should not append content, got: %q", data)
	}
}

func TestWriteSignal_DropsOversizedFirstWrite(t *testing.T) {
	dir := t.TempDir()
	// Even first write rejected if message exceeds cap
	WriteSignal(dir, "proj-test", "a", "alice", strings.Repeat("x", 100), 50)
	if _, err := os.Stat(filepath.Join(dir, ProjectPathKey("proj-test"), "a")); err == nil {
		t.Fatal("oversized first write should be dropped")
	}
}

func TestWriteSignal_LogsWhenDroppingOverCap(t *testing.T) {
	dir := t.TempDir()
	if err := WriteSignal(dir, "proj-test", "a", "alice", "hello", 100); err != nil {
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

	if err := WriteSignal(dir, "proj-test", "a", "bob", strings.Repeat("x", 100), 100); !errors.Is(err, ErrSignalCapExceeded) {
		t.Fatalf("WriteSignal() error = %v, want %v", err, ErrSignalCapExceeded)
	}

	logged := buf.String()
	if !strings.Contains(logged, "dropping signal for") {
		t.Fatalf("expected cap-drop log, got %q", logged)
	}
}

func TestWriteSignal_DirPermissions(t *testing.T) {
	sigDir := filepath.Join(t.TempDir(), "signals")
	WriteSignal(sigDir, "proj-test", "a", "alice", "test", 65536)
	info, err := os.Stat(filepath.Join(sigDir, ProjectPathKey("proj-test")))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("dir perm = %o, want 700", perm)
	}
}

func TestWriteSignal_RejectsAncestorSymlink(t *testing.T) {
	runtimeRoot := t.TempDir()
	realSignals := filepath.Join(runtimeRoot, "signals-real")
	if err := os.MkdirAll(realSignals, 0o700); err != nil {
		t.Fatal(err)
	}

	signalDir := filepath.Join(runtimeRoot, "signals")
	if err := os.Symlink(realSignals, signalDir); err != nil {
		t.Fatal(err)
	}

	err := WriteSignal(signalDir, "proj-test", "agent-1", "alice", "hello", 65536)
	if err == nil {
		t.Fatal("expected error for ancestor symlink")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(realSignals, ProjectPathKey("proj-test"), "agent-1")); statErr == nil {
		t.Fatal("signal should not be written through symlinked path")
	}
}

// ConsumeSignal is test-only because production delivery uses shell/JS hooks for consume.
func ConsumeSignal(signalDir, projectKey, agentName string) (string, error) {
	if projectKey == "" {
		return "", fmt.Errorf("empty project key")
	}
	path := filepath.Join(signalDir, projectKey, agentName)
	tmp := fmt.Sprintf("%s.consuming-%d", path, time.Now().UnixNano())
	if err := os.Rename(path, tmp); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	data, err := os.ReadFile(tmp)
	if removeErr := os.Remove(tmp); removeErr != nil && !os.IsNotExist(removeErr) && err == nil {
		err = removeErr
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func TestConsumeSignal_AtomicReadAndDelete(t *testing.T) {
	dir := t.TempDir()
	WriteSignal(dir, "proj-test", "a", "alice", "hello", 65536)
	projDir := filepath.Join(dir, ProjectPathKey("proj-test"))
	content, err := ConsumeSignal(dir, ProjectPathKey("proj-test"), "a")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "alice") {
		t.Fatalf("content = %q", content)
	}
	if _, err := os.Stat(filepath.Join(projDir, "a")); !os.IsNotExist(err) {
		t.Fatal("original should be deleted")
	}
	entries, _ := os.ReadDir(projDir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "consuming") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestConsumeSignal_NoFile_Empty(t *testing.T) {
	content, err := ConsumeSignal(t.TempDir(), ProjectPathKey("no-proj"), "nope")
	if err != nil || content != "" {
		t.Fatalf("expected empty, got %q err=%v", content, err)
	}
}

func TestConsumeSignal_NewWriteDuringConsume(t *testing.T) {
	dir := t.TempDir()
	WriteSignal(dir, "proj-test", "a", "alice", "first", 65536)
	projDir := filepath.Join(dir, ProjectPathKey("proj-test"))
	// Simulate atomic rename (what ConsumeSignal does internally)
	orig := filepath.Join(projDir, "a")
	tmp := orig + ".consuming-test"
	os.Rename(orig, tmp)
	// Daemon writes new message to original path AFTER rename
	WriteSignal(dir, "proj-test", "a", "bob", "second", 65536)
	// Consumed content has first message only
	data, _ := os.ReadFile(tmp)
	os.Remove(tmp)
	if !strings.Contains(string(data), "alice") || strings.Contains(string(data), "bob") {
		t.Fatalf("consumed = %q", data)
	}
	// New message survives at original path
	data2, _ := os.ReadFile(orig)
	if !strings.Contains(string(data2), "bob") {
		t.Fatalf("new message lost, orig = %q", data2)
	}
}

func TestProjectPathKey_Deterministic(t *testing.T) {
	got := ProjectPathKey("abcdef0123456789")
	if got == "" {
		t.Fatal("ProjectPathKey() returned empty string")
	}
	if len(got) != 12 {
		t.Fatalf("len(ProjectPathKey()) = %d, want 12", len(got))
	}
	if got != ProjectPathKey("abcdef0123456789") {
		t.Fatalf("ProjectPathKey() not deterministic: %q vs %q", got, ProjectPathKey("abcdef0123456789"))
	}
}

func TestProjectPathKey_NoCollision(t *testing.T) {
	inputs := []string{
		"abcdef0123456789",
		"abcdef012345678a",
		"path:/Users/test/project",
		"path:/Users/test/project-2",
		"../../etc/passwd",
		"proj-abc",
		"proj abd",
	}

	seen := make(map[string]string, len(inputs))
	for _, input := range inputs {
		key := ProjectPathKey(input)
		if key == "" {
			t.Fatalf("ProjectPathKey(%q) returned empty string", input)
		}
		if prior, exists := seen[key]; exists {
			t.Fatalf("ProjectPathKey collision: %q and %q both mapped to %q", prior, input, key)
		}
		seen[key] = input
	}
}

func TestWriteSignal_StripsANSI(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"OSC52 clipboard", "hello \x1b]52;c;base64data\x07 world"},
		{"CSI color", "hello \x1b[31mred\x1b[0m world"},
		{"CSI private mode", "hello \x1b[?1049h world"},
		{"CSI DEC private", "hello \x1b[?25l world"},
		{"DCS sequence", "hello \x1bP1;2;3q#1PAYLOAD\x1b\\ world"},
		{"raw CR overwrite", "safe\rOVERRIDE"},
		{"raw BEL", "hello \x07 world"},
		{"raw backspace", "hello \b world"},
		{"C1 CSI equiv", "hello \x9b31m world"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := t.TempDir()
			if err := WriteSignal(d, "abcdef01", "a", "alice", tt.input, 65536); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(filepath.Join(d, ProjectPathKey("abcdef01"), "a"))
			s := string(data)
			for _, b := range s {
				if b < 0x20 && b != '\n' && b != '\t' {
					t.Fatalf("control char %U not stripped in %q", b, s)
				}
				if b >= 0x7f && b <= 0x9f {
					t.Fatalf("C1 char %U not stripped in %q", b, s)
				}
				if b == 0x1b {
					t.Fatalf("ESC not stripped in %q", s)
				}
			}
		})
	}
}

func TestStripANSI_StripsCSIIntermediateBytes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "delete chars with intermediate byte",
			input: "hello \x1b[#P world",
			want:  "hello  world",
		},
		{
			name:  "cursor style with space intermediate byte",
			input: "hello \x1b[4 q world",
			want:  "hello  world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripANSI(tt.input); got != tt.want {
				t.Fatalf("stripANSI(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestWriteSignal_StripsANSIFromSender(t *testing.T) {
	dir := t.TempDir()
	ansiFrom := "\x1b[31mevil\x1b[0m"
	if err := WriteSignal(dir, "proj-test", "a", ansiFrom, "msg", 65536); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ProjectPathKey("proj-test"), "a"))
	if strings.Contains(string(data), "\x1b") {
		t.Fatalf("ANSI escape not stripped from sender: %q", data)
	}
	if !strings.Contains(string(data), "evil") {
		t.Fatalf("sender name lost: %q", data)
	}
}

func TestWriteSignal_RejectsEmptyProjectID(t *testing.T) {
	dir := t.TempDir()
	err := WriteSignal(dir, "", "a", "alice", "hello", 65536)
	if err == nil {
		t.Fatal("expected error for empty project ID")
	}
}

func TestWriteSignal_SanitizesProjectIDPath(t *testing.T) {
	dir := t.TempDir()
	// Path traversal attempt — should NOT create dirs outside signal dir
	err := WriteSignal(dir, "../../etc", "a", "alice", "hello", 65536)
	if err != nil {
		t.Fatal(err) // sanitizer transforms it, doesn't reject
	}
	// Verify it created a safe directory, not ../../etc
	if _, err := os.Stat(filepath.Join(dir, "..", "..", "etc")); err == nil {
		t.Fatal("path traversal succeeded!")
	}
	if _, err := os.Stat(filepath.Join(dir, ProjectPathKey("../../etc"), "a")); err != nil {
		t.Fatalf("hashed project path missing: %v", err)
	}
}

func TestWriteSignal_SanitizesAgentNamePath(t *testing.T) {
	dir := t.TempDir()

	if err := WriteSignal(dir, "proj-test", "../Agent Name", "alice", "hello", 65536); err != nil {
		t.Fatal(err)
	}

	safePath := filepath.Join(dir, ProjectPathKey("proj-test"), "agent-name")
	if _, err := os.Stat(safePath); err != nil {
		t.Fatalf("sanitized signal path missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "evil")); err == nil {
		t.Fatal("unsanitized agent path escaped project directory")
	}
}

// TestSignalPipeline_EndToEnd exercises the full signal delivery path:
// WriteSessionMapping → WriteSignal → two-hop file discovery → ConsumeSignal.
// This is the Go equivalent of what shell-hook.sh and waggle-push.js do at runtime.
func TestSignalPipeline_EndToEnd(t *testing.T) {
	runtimeDir := t.TempDir()
	signalDir := filepath.Join(runtimeDir, "signals")
	os.MkdirAll(signalDir, 0o700)

	ppid := 12345
	agentName := "claude-code-test"
	projectID := "abcdef0123456789"
	projectKey := ProjectPathKey(projectID)

	// Step 1: Simulate bootstrap — generate nonce, write session mapping
	nonce := "12345-1711843200000000000"
	sessionPath := filepath.Join(runtimeDir, "agent-session-"+nonce)
	os.WriteFile(sessionPath, []byte(agentName+"\n"+projectKey+"\n"), 0o600)
	ppidPath := filepath.Join(runtimeDir, fmt.Sprintf("agent-ppid-%d", ppid))
	os.WriteFile(ppidPath, []byte(nonce+"\n"), 0o600)

	// Step 2: Daemon writes a signal (simulates notifyRecord → WriteSignal)
	body := "hello from orchestrator"
	if err := WriteSignal(signalDir, projectID, agentName, "orchestrator", body, 65536); err != nil {
		t.Fatalf("WriteSignal: %v", err)
	}

	// Step 3: Simulate two-hop discovery (what the shell hook does)
	// Read PPID pointer → get nonce
	ppidData, err := os.ReadFile(ppidPath)
	if err != nil {
		t.Fatalf("read ppid pointer: %v", err)
	}
	discoveredNonce := strings.TrimSpace(string(ppidData))
	if discoveredNonce != nonce {
		t.Fatalf("nonce = %q, want %q", discoveredNonce, nonce)
	}

	// Read session file → get agent name + project
	sessData, err := os.ReadFile(filepath.Join(runtimeDir, "agent-session-"+discoveredNonce))
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(sessData)), "\n")
	if len(lines) < 2 {
		t.Fatalf("session file has %d lines, want 2", len(lines))
	}
	discoveredAgent := lines[0]
	discoveredProject := lines[1]
	if discoveredAgent != agentName {
		t.Fatalf("agent = %q, want %q", discoveredAgent, agentName)
	}
	if discoveredProject != projectKey {
		t.Fatalf("project = %q, want %q", discoveredProject, projectKey)
	}

	// Step 4: Check signal file exists at expected path
	sigPath := filepath.Join(signalDir, discoveredProject, discoveredAgent)
	if _, err := os.Stat(sigPath); os.IsNotExist(err) {
		t.Fatalf("signal file not found at %s", sigPath)
	}

	// Step 5: Consume signal (what the shell hook does via mv+cat+rm)
	content, err := ConsumeSignal(signalDir, discoveredProject, discoveredAgent)
	if err != nil {
		t.Fatalf("ConsumeSignal: %v", err)
	}
	if !strings.Contains(content, "orchestrator") || !strings.Contains(content, "hello from orchestrator") {
		t.Fatalf("consumed content = %q, missing expected message", content)
	}

	// Step 6: Signal file should be gone after consume
	if _, err := os.Stat(sigPath); !os.IsNotExist(err) {
		t.Fatal("signal file should be deleted after consume")
	}

	// Step 7: Second consume returns empty (no duplicate delivery)
	content2, err := ConsumeSignal(signalDir, discoveredProject, discoveredAgent)
	if err != nil {
		t.Fatalf("second ConsumeSignal: %v", err)
	}
	if content2 != "" {
		t.Fatalf("second consume should be empty, got %q", content2)
	}
}

// TestPruneStaleSignals_RmdirCannotDeleteNonEmptyDir proves that os.Remove
// on a directory with files fails with ENOTEMPTY. This disproves the claim
// that a TOCTOU race in PruneStaleSignals could lose signal files — rmdir(2)
// checks emptiness atomically in the kernel.
func TestPruneStaleSignals_RmdirCannotDeleteNonEmptyDir(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "project")
	os.MkdirAll(dir, 0o700)

	// Write a file into the directory
	os.WriteFile(filepath.Join(dir, "signal"), []byte("data"), 0o600)

	// Attempt to remove the non-empty directory
	err := os.Remove(dir)
	if err == nil {
		t.Fatal("os.Remove succeeded on non-empty directory — signal file would be lost")
	}

	// Verify the file survives
	data, err := os.ReadFile(filepath.Join(dir, "signal"))
	if err != nil {
		t.Fatalf("signal file was lost: %v", err)
	}
	if string(data) != "data" {
		t.Fatalf("signal file corrupted: %q", data)
	}
}

// TestPruneStaleSignals_EmptyDirCleanedNextCycle proves that empty project
// directories left behind by one prune cycle are cleaned up by the next.
func TestPruneStaleSignals_EmptyDirCleanedNextCycle(t *testing.T) {
	signalDir := t.TempDir()
	projDir := filepath.Join(signalDir, "proj-abc")
	os.MkdirAll(projDir, 0o700)

	// Write a signal file with old mtime
	sigPath := filepath.Join(projDir, "agent-1")
	os.WriteFile(sigPath, []byte("old message"), 0o600)
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(sigPath, old, old)

	// First prune: removes the stale signal file, tries to remove dir
	PruneStaleSignals(signalDir, 24*time.Hour)

	// Signal file should be gone
	if _, err := os.Stat(sigPath); !os.IsNotExist(err) {
		t.Fatal("stale signal file should be removed")
	}

	// Directory should also be gone (it was empty after signal removal)
	if _, err := os.Stat(projDir); !os.IsNotExist(err) {
		t.Fatal("empty project directory should be cleaned up")
	}
}
