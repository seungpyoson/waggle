package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	// First write: long body exceeds small cap
	WriteSignal(dir, "proj-test", "a", "alice", strings.Repeat("x", 100), 50)
	// File is ~133 bytes > 50 cap
	WriteSignal(dir, "proj-test", "a", "bob", "should-be-dropped", 50)
	data, _ := os.ReadFile(filepath.Join(dir, "proj-test", "a"))
	if strings.Contains(string(data), "bob") {
		t.Fatalf("second write should be dropped, got: %q", data)
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
	content, err := ConsumeSignal(projDir, "a")
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
	content, err := ConsumeSignal(t.TempDir(), "nope")
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
