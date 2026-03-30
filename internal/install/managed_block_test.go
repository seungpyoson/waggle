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

	content := "existing\n" + testBegin + "\npartial body"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Should self-heal: replace from begin to EOF with canonical block
	err := upsertManagedBlock(path, testBegin, testEnd, testBody)
	if err != nil {
		t.Fatalf("expected self-heal for begin-without-end, got error: %v", err)
	}

	after, _ := os.ReadFile(path)
	if !strings.Contains(string(after), testEnd) {
		t.Fatalf("end marker missing after self-heal: %s", string(after))
	}
}

func TestUpsertSelfHealsBeginWithoutEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")

	// File with begin marker but no end marker (truncated)
	content := "prefix\n" + testBegin + "\npartial body content"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	err := upsertManagedBlock(path, testBegin, testEnd, testBody)
	if err != nil {
		t.Fatalf("expected self-heal, got error: %v", err)
	}

	after, _ := os.ReadFile(path)
	result := string(after)

	// Prefix must be preserved
	if !strings.HasPrefix(result, "prefix\n") {
		t.Fatalf("prefix not preserved: %s", result)
	}
	// Must contain both markers now
	if !strings.Contains(result, testBegin) || !strings.Contains(result, testEnd) {
		t.Fatalf("markers missing after self-heal: %s", result)
	}
	// Must contain the new body
	if !strings.Contains(result, testBody) {
		t.Fatalf("body missing after self-heal: %s", result)
	}
}

func TestRemoveSelfHealsBeginWithoutEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")

	// File with begin marker but no end marker
	content := "prefix\n" + testBegin + "\npartial body content"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	err := removeManagedBlock(path, testBegin, testEnd)
	if err != nil {
		t.Fatalf("expected self-heal, got error: %v", err)
	}

	after, _ := os.ReadFile(path)
	result := string(after)

	// Prefix preserved, markers gone
	if result != "prefix\n" {
		t.Fatalf("expected only prefix after remove, got: %q", result)
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

// --- Class 3: CRLF line ending handling ---

func TestUpsertAcceptsCRLFLineEndings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")

	// CRLF file with valid block
	content := "header\r\n" + testBegin + "\r\nbody\r\n" + testEnd + "\r\nfooter\r\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	err := upsertManagedBlock(path, testBegin, testEnd, testBody)
	if err != nil {
		t.Fatalf("CRLF file should be accepted: %v", err)
	}

	after, _ := os.ReadFile(path)
	result := string(after)
	// Must contain both markers and the new body
	if !strings.Contains(result, testBegin) || !strings.Contains(result, testEnd) {
		t.Fatalf("markers missing after CRLF upsert: %q", result)
	}
	if !strings.Contains(result, testBody) {
		t.Fatalf("body missing after CRLF upsert: %q", result)
	}
	// Header and footer with CRLF should be preserved
	if !strings.HasPrefix(result, "header\r\n") {
		t.Fatalf("header not preserved: %q", result)
	}
	if !strings.HasSuffix(result, "footer\r\n") {
		t.Fatalf("footer not preserved: %q", result)
	}
}

func TestRemoveAcceptsCRLFLineEndings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")

	content := "header\r\n" + testBegin + "\r\nbody\r\n" + testEnd + "\r\nfooter\r\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	err := removeManagedBlock(path, testBegin, testEnd)
	if err != nil {
		t.Fatalf("CRLF file should be accepted: %v", err)
	}

	after, _ := os.ReadFile(path)
	want := "header\r\nfooter\r\n"
	if string(after) != want {
		t.Fatalf("CRLF remove produced wrong output:\nwant: %q\ngot:  %q", want, string(after))
	}
}

func TestTopologyRejectsGluedBeginEvenWithCR(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")

	// \r before begin is a line break — should be accepted
	content := "header\r" + testBegin + "\nbody\n" + testEnd + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	err := upsertManagedBlock(path, testBegin, testEnd, testBody)
	if err != nil {
		t.Fatalf("bare CR before begin should be accepted as line break: %v", err)
	}
}
