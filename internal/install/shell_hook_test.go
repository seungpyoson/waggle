package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seungpyoson/waggle/internal/fsutil"
)

func TestAtomicWriteFile_BasicRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := []byte("hello world\n")
	if err := atomicWriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("got %q, want %q", got, content)
	}
	info, _ := os.Stat(path)
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Fatalf("perm = %o, want 644", perm)
	}
}

func TestAtomicWriteFile_NoTempLeftBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	atomicWriteFile(path, []byte("data"), 0o644)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".waggle-tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestHasAncestorSymlink_DetectsSymlinkedParent(t *testing.T) {
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	os.MkdirAll(realDir, 0o700)
	linkDir := filepath.Join(base, "link")
	os.Symlink(realDir, linkDir)

	if !fsutil.HasAncestorSymlink(filepath.Join(linkDir, "file.txt"), base) {
		t.Fatal("should detect symlinked parent")
	}
}

func TestHasAncestorSymlink_NoSymlink(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "subdir")
	os.MkdirAll(dir, 0o700)
	if fsutil.HasAncestorSymlink(filepath.Join(dir, "file.txt"), base) {
		t.Fatal("should not detect symlink in real dir")
	}
}

func TestHasAncestorSymlink_AbsolutePathsNeverRelError(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "subdir")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "file.txt")

	rel, err := filepath.Rel(base, path)
	if err != nil {
		t.Fatalf("filepath.Rel returned error for absolute paths: %v", err)
	}
	if rel != filepath.Join("subdir", "file.txt") {
		t.Fatalf("rel = %q, want %q", rel, filepath.Join("subdir", "file.txt"))
	}
	if fsutil.HasAncestorSymlink(path, base) {
		t.Fatal("absolute path under root should not be treated as a symlink escape")
	}
}

func TestHasAncestorSymlink_RejectsPathEscapingRoot(t *testing.T) {
	base := t.TempDir()
	escaped := filepath.Join(base, "..", "etc", "passwd")
	if !fsutil.HasAncestorSymlink(escaped, base) {
		t.Fatal("should reject path escaping root via ..")
	}
}

func TestHasAncestorSymlink_DeeplyNestedSymlink(t *testing.T) {
	base := t.TempDir()
	realDir := filepath.Join(base, "real", "deep")
	os.MkdirAll(realDir, 0o700)
	os.MkdirAll(filepath.Join(base, "a"), 0o700)
	os.Symlink(realDir, filepath.Join(base, "a", "link"))
	if !fsutil.HasAncestorSymlink(filepath.Join(base, "a", "link", "file.txt"), base) {
		t.Fatal("should detect deeply nested symlink")
	}
}

func TestAtomicWriteFile_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("old"), 0o644)
	if err := atomicWriteFile(path, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Fatalf("got %q, want %q", got, "new")
	}
}

func TestSafeWriteFile_RejectsLeafSymlink(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target.txt")
	if err := os.WriteFile(target, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	err := safeWriteFile(link, []byte("mutated"), 0o644, base)
	if err == nil {
		t.Fatal("expected symlink rejection")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Fatalf("target content = %q, want %q", got, "original")
	}
}

func TestSafeRemoveAll_RejectsLeafSymlink(t *testing.T) {
	base := t.TempDir()
	targetDir := filepath.Join(base, "target")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(targetDir, link); err != nil {
		t.Fatal(err)
	}

	err := safeRemoveAll(link, base)
	if err == nil {
		t.Fatal("expected symlink rejection")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}

	if _, err := os.Stat(targetDir); err != nil {
		t.Fatalf("target dir should remain: %v", err)
	}
}
