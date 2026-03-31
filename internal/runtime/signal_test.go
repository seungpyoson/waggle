package runtime

import (
	"fmt"
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
	data, err := os.ReadFile(filepath.Join(dir, "proj-test", "agent-1"))
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
	data, _ := os.ReadFile(filepath.Join(dir, "proj-test", "a"))
	if c := strings.Count(string(data), "\n"); c != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", c, data)
	}
}

func TestWriteSignal_DropsWhenOverCap(t *testing.T) {
	dir := t.TempDir()
	// First write fits (short message ~38 bytes, cap 100)
	WriteSignal(dir, "proj-test", "a", "alice", "hello", 100)
	data, _ := os.ReadFile(filepath.Join(dir, "proj-test", "a"))
	if !strings.Contains(string(data), "alice") {
		t.Fatalf("first write should succeed, got: %q", data)
	}
	// Second write pushes past cap
	WriteSignal(dir, "proj-test", "a", "bob", strings.Repeat("x", 100), 100)
	data, _ = os.ReadFile(filepath.Join(dir, "proj-test", "a"))
	if strings.Contains(string(data), "bob") {
		t.Fatalf("second write should be dropped, got: %q", data)
	}
}

func TestWriteSignal_DropsOversizedFirstWrite(t *testing.T) {
	dir := t.TempDir()
	// Even first write rejected if message exceeds cap
	WriteSignal(dir, "proj-test", "a", "alice", strings.Repeat("x", 100), 50)
	if _, err := os.Stat(filepath.Join(dir, "proj-test", "a")); err == nil {
		t.Fatal("oversized first write should be dropped")
	}
}

func TestWriteSignal_DirPermissions(t *testing.T) {
	sigDir := filepath.Join(t.TempDir(), "signals")
	WriteSignal(sigDir, "proj-test", "a", "alice", "test", 65536)
	info, err := os.Stat(filepath.Join(sigDir, "proj-test"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("dir perm = %o, want 700", perm)
	}
}

func TestConsumeSignal_AtomicReadAndDelete(t *testing.T) {
	dir := t.TempDir()
	WriteSignal(dir, "proj-test", "a", "alice", "hello", 65536)
	projDir := filepath.Join(dir, "proj-test")
	content, err := ConsumeSignal(dir, "proj-test", "a")
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
	content, err := ConsumeSignal(t.TempDir(), "no-proj", "nope")
	if err != nil || content != "" {
		t.Fatalf("expected empty, got %q err=%v", content, err)
	}
}

func TestConsumeSignal_NewWriteDuringConsume(t *testing.T) {
	dir := t.TempDir()
	WriteSignal(dir, "proj-test", "a", "alice", "first", 65536)
	projDir := filepath.Join(dir, "proj-test")
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

func TestSanitizeProjectIDForPath_GitHash(t *testing.T) {
	got := SanitizeProjectIDForPath("abcdef0123456789")
	if got != "abcdef0123456789" {
		t.Fatalf("got %q, want unchanged hex hash", got)
	}
}

func TestSanitizeProjectIDForPath_PathTraversal(t *testing.T) {
	got := SanitizeProjectIDForPath("../../etc/passwd")
	if strings.Contains(got, "/") || strings.Contains(got, "..") {
		t.Fatalf("path traversal not sanitized: %q", got)
	}
}

func TestSanitizeProjectIDForPath_PathPrefix(t *testing.T) {
	got := SanitizeProjectIDForPath("path:/Users/test/project")
	if strings.Contains(got, "/") {
		t.Fatalf("slashes not removed: %q", got)
	}
	if got == "" {
		t.Fatal("should not be empty")
	}
}

func TestSanitizeProjectIDForPath_Empty(t *testing.T) {
	got := SanitizeProjectIDForPath("")
	if got != "" {
		t.Fatalf("got %q, want empty", got)
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
			data, _ := os.ReadFile(filepath.Join(d, "abcdef01", "a"))
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

func TestWriteSignal_StripsANSIFromSender(t *testing.T) {
	dir := t.TempDir()
	ansiFrom := "\x1b[31mevil\x1b[0m"
	if err := WriteSignal(dir, "proj-test", "a", ansiFrom, "msg", 65536); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "proj-test", "a"))
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
	safeProjectID := SanitizeProjectIDForPath(projectID)

	// Step 1: Simulate bootstrap — generate nonce, write session mapping
	nonce := "12345-1711843200000000000"
	sessionPath := filepath.Join(runtimeDir, "agent-session-"+nonce)
	os.WriteFile(sessionPath, []byte(agentName+"\n"+safeProjectID+"\n"), 0o600)
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
	if discoveredProject != safeProjectID {
		t.Fatalf("project = %q, want %q", discoveredProject, safeProjectID)
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
