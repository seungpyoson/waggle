package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testBegin = "<!-- TEST-BEGIN -->"
	testEnd   = "<!-- TEST-END -->"
	testBody  = "test body content"
)

// --- Class 1: Topology validation ---

func TestUpsertRejectsOrphanedEndMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")

	content := "some content\n" + testEnd + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	err := upsertManagedBlock(path, testBegin, testEnd, testBody)
	if err == nil {
		t.Fatal("expected error for orphaned end marker, got nil")
	}
	if !strings.Contains(err.Error(), "orphaned end marker") {
		t.Fatalf("unexpected error: %v", err)
	}

	after, _ := os.ReadFile(path)
	if string(after) != content {
		t.Fatalf("file changed despite error:\nwant: %q\ngot:  %q", content, string(after))
	}
}

func TestUpsertRejectsDuplicateMarkers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")

	content := testBegin + "\nbody1\n" + testEnd + "\n" + testBegin + "\nbody2\n" + testEnd + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	err := upsertManagedBlock(path, testBegin, testEnd, testBody)
	if err == nil {
		t.Fatal("expected error for duplicate markers, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("unexpected error: %v", err)
	}

	after, _ := os.ReadFile(path)
	if string(after) != content {
		t.Fatalf("file changed despite error:\nwant: %q\ngot:  %q", content, string(after))
	}
}

func TestRemoveRejectsOrphanedEndMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")

	content := "some content\n" + testEnd + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	err := removeManagedBlock(path, testBegin, testEnd)
	if err == nil {
		t.Fatal("expected error for orphaned end marker, got nil")
	}
	if !strings.Contains(err.Error(), "orphaned end marker") {
		t.Fatalf("unexpected error: %v", err)
	}

	after, _ := os.ReadFile(path)
	if string(after) != content {
		t.Fatalf("file changed despite error:\nwant: %q\ngot:  %q", content, string(after))
	}
}

func TestRemoveRejectsDuplicateMarkers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")

	content := testBegin + "\nbody1\n" + testEnd + "\n" + testBegin + "\nbody2\n" + testEnd + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	err := removeManagedBlock(path, testBegin, testEnd)
	if err == nil {
		t.Fatal("expected error for duplicate markers, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("unexpected error: %v", err)
	}

	after, _ := os.ReadFile(path)
	if string(after) != content {
		t.Fatalf("file changed despite error:\nwant: %q\ngot:  %q", content, string(after))
	}
}

func TestUpsertRejectsGluedBeginMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")

	// Begin marker is glued to preceding text (no newline before it)
	content := "User Content" + testBegin + "\nbody\n" + testEnd + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	err := upsertManagedBlock(path, testBegin, testEnd, testBody)
	if err == nil {
		t.Fatal("expected error for glued begin marker, got nil")
	}
	if !strings.Contains(err.Error(), "begin marker not at start of line") {
		t.Fatalf("unexpected error: %v", err)
	}

	after, _ := os.ReadFile(path)
	if string(after) != content {
		t.Fatalf("file changed despite error:\nwant: %q\ngot:  %q", content, string(after))
	}
}

func TestUpsertRejectsGluedEndMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")

	// End marker is followed by non-newline content
	content := testBegin + "\nbody\n" + testEnd + "trailing"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	err := upsertManagedBlock(path, testBegin, testEnd, testBody)
	if err == nil {
		t.Fatal("expected error for glued end marker, got nil")
	}
	if !strings.Contains(err.Error(), "end marker not at end of line") {
		t.Fatalf("unexpected error: %v", err)
	}

	after, _ := os.ReadFile(path)
	if string(after) != content {
		t.Fatalf("file changed despite error:\nwant: %q\ngot:  %q", content, string(after))
	}
}

// --- Class 1: Valid topologies that should succeed ---

func TestUpsertAcceptsBeginAtFileStart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")

	// Begin marker at very start of file (idx == 0) is valid
	content := testBegin + "\nbody\n" + testEnd + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	err := upsertManagedBlock(path, testBegin, testEnd, testBody)
	if err != nil {
		t.Fatalf("unexpected error for valid topology: %v", err)
	}
}

func TestUpsertAcceptsEndAtEOF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")

	// End marker at EOF (no trailing newline or content) is valid
	content := testBegin + "\nbody\n" + testEnd
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	err := upsertManagedBlock(path, testBegin, testEnd, testBody)
	if err != nil {
		t.Fatalf("unexpected error for valid topology: %v", err)
	}
}

func TestUpsertAcceptsBeginOnlyWithLineStart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")

	// Begin marker alone (no end) at start of line is valid (repair handles it)
	content := "existing\n" + testBegin + "\npartial body"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// This will fail at the "start found without end" check inside upsert,
	// not at topology validation. Topology allows begin-without-end.
	err := upsertManagedBlock(path, testBegin, testEnd, testBody)
	if err != nil && strings.Contains(err.Error(), "topology") {
		t.Fatalf("topology validation should not reject begin-without-end: %v", err)
	}
}

func TestUpsertRejectsReversedMarkers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")

	// End marker before begin marker — each appearing exactly once
	content := testEnd + "\nother\n" + testBegin + "\nbody\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	err := upsertManagedBlock(path, testBegin, testEnd, testBody)
	if err == nil {
		t.Fatal("expected error for reversed markers, got nil")
	}
	if !strings.Contains(err.Error(), "end marker appears before begin") {
		t.Fatalf("unexpected error: %v", err)
	}

	after, _ := os.ReadFile(path)
	if string(after) != content {
		t.Fatalf("file changed despite error:\nwant: %q\ngot:  %q", content, string(after))
	}
}

func TestRemoveRejectsReversedMarkers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")

	content := testEnd + "\nother\n" + testBegin + "\nbody\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	err := removeManagedBlock(path, testBegin, testEnd)
	if err == nil {
		t.Fatal("expected error for reversed markers, got nil")
	}
	if !strings.Contains(err.Error(), "end marker appears before begin") {
		t.Fatalf("unexpected error: %v", err)
	}

	after, _ := os.ReadFile(path)
	if string(after) != content {
		t.Fatalf("file changed despite error:\nwant: %q\ngot:  %q", content, string(after))
	}
}

// --- Class 2: Round-trip newline behavior ---

func TestRoundTripNewlineBehavior(t *testing.T) {
	t.Run("file_with_trailing_newline_is_byte_exact", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.md")

		original := "# Header\nContent\n"
		if err := os.WriteFile(path, []byte(original), 0644); err != nil {
			t.Fatal(err)
		}

		if err := upsertManagedBlock(path, testBegin, testEnd, testBody); err != nil {
			t.Fatalf("install failed: %v", err)
		}
		if err := removeManagedBlock(path, testBegin, testEnd); err != nil {
			t.Fatalf("uninstall failed: %v", err)
		}

		after, _ := os.ReadFile(path)
		if string(after) != original {
			t.Fatalf("round-trip changed file:\nwant: %q\ngot:  %q", original, string(after))
		}
	})

	t.Run("file_without_trailing_newline_gains_one_byte", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.md")

		original := "# Header\nContent"
		if err := os.WriteFile(path, []byte(original), 0644); err != nil {
			t.Fatal(err)
		}

		if err := upsertManagedBlock(path, testBegin, testEnd, testBody); err != nil {
			t.Fatalf("install failed: %v", err)
		}
		if err := removeManagedBlock(path, testBegin, testEnd); err != nil {
			t.Fatalf("uninstall failed: %v", err)
		}

		after, _ := os.ReadFile(path)
		// Non-POSIX file gains exactly one byte: the trailing \n
		if len(after) != len(original)+1 {
			t.Fatalf("expected exactly 1 byte added, got %d byte difference:\nwant: %q\ngot:  %q",
				len(after)-len(original), original, string(after))
		}
		if string(after) != original+"\n" {
			t.Fatalf("expected POSIX-normalized output:\nwant: %q\ngot:  %q", original+"\n", string(after))
		}
	})
}
