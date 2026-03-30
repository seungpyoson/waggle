package install

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstall_GeminiCreatesBlock(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installGemini(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpHome, ".gemini", "GEMINI.md"))
	if err != nil {
		t.Fatalf("read GEMINI.md: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, geminiBlockBegin) || !strings.Contains(content, geminiBlockEnd) {
		t.Fatalf("managed block markers missing:\n%s", content)
	}
	if !strings.Contains(content, "waggle adapter bootstrap gemini") {
		t.Fatalf("bootstrap command missing:\n%s", content)
	}
}

func TestInstall_GeminiIdempotent(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installGemini(tmpHome); err != nil {
		t.Fatalf("first install failed: %v", err)
	}
	if err := installGemini(tmpHome); err != nil {
		t.Fatalf("second install failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpHome, ".gemini", "GEMINI.md"))
	if err != nil {
		t.Fatalf("read GEMINI.md: %v", err)
	}

	if got := strings.Count(string(data), geminiBlockBegin); got != 1 {
		t.Fatalf("expected 1 managed block, got %d", got)
	}
}

func TestInstall_GeminiUninstall(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installGemini(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if err := uninstallGemini(tmpHome); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpHome, ".gemini", "GEMINI.md"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read GEMINI.md: %v", err)
	}
	if err == nil && strings.Contains(string(data), geminiBlockBegin) {
		t.Fatalf("managed block still present after uninstall:\n%s", string(data))
	}
}

func TestInstall_GeminiPreservesOtherContent(t *testing.T) {
	tmpHome := t.TempDir()
	geminiDir := filepath.Join(tmpHome, ".gemini")
	if err := os.MkdirAll(geminiDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	original := "# My Gemini Config\n- keep this\n"
	if err := os.WriteFile(filepath.Join(geminiDir, "GEMINI.md"), []byte(original), 0644); err != nil {
		t.Fatalf("write GEMINI.md: %v", err)
	}

	if err := installGemini(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(geminiDir, "GEMINI.md"))
	if err != nil {
		t.Fatalf("read GEMINI.md: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "# My Gemini Config") {
		t.Fatalf("existing content removed:\n%s", content)
	}
	if !strings.Contains(content, geminiBlockBegin) {
		t.Fatalf("managed block missing:\n%s", content)
	}
}

func TestUninstall_GeminiPreservesOtherContent(t *testing.T) {
	tmpHome := t.TempDir()
	geminiDir := filepath.Join(tmpHome, ".gemini")
	if err := os.MkdirAll(geminiDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	original := "# My Gemini Config\n- keep this\n"
	if err := os.WriteFile(filepath.Join(geminiDir, "GEMINI.md"), []byte(original), 0644); err != nil {
		t.Fatalf("write GEMINI.md: %v", err)
	}

	if err := installGemini(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if err := uninstallGemini(tmpHome); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(geminiDir, "GEMINI.md"))
	if err != nil {
		t.Fatalf("read GEMINI.md: %v", err)
	}
	if string(data) != original {
		t.Fatalf("GEMINI.md content changed unexpectedly:\nwant:\n%s\ngot:\n%s", original, string(data))
	}
}

func TestCheckGemini_NotInstalled(t *testing.T) {
	tmpHome := t.TempDir()

	issues, state := CheckGemini(tmpHome)
	if state != StateNotInstalled {
		t.Fatalf("expected StateNotInstalled, got %v", state)
	}
	if len(issues) != 0 {
		t.Fatalf("expected zero issues, got %d", len(issues))
	}
}

func TestCheckGemini_NotInstalledNoMarker(t *testing.T) {
	tmpHome := t.TempDir()
	geminiDir := filepath.Join(tmpHome, ".gemini")
	if err := os.MkdirAll(geminiDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// File exists but no marker
	if err := os.WriteFile(filepath.Join(geminiDir, "GEMINI.md"), []byte("# Content\nNo marker here\n"), 0644); err != nil {
		t.Fatalf("write GEMINI.md: %v", err)
	}

	issues, state := CheckGemini(tmpHome)
	if state != StateNotInstalled {
		t.Fatalf("expected StateNotInstalled, got %v", state)
	}
	if len(issues) != 0 {
		t.Fatalf("expected zero issues, got %d", len(issues))
	}
}

func TestCheckGemini_Healthy(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installGemini(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	issues, state := CheckGemini(tmpHome)
	if state != StateHealthy {
		t.Fatalf("expected StateHealthy, got %v", state)
	}
	if len(issues) != 0 {
		t.Fatalf("expected zero issues, got %d", len(issues))
	}
}

func TestCheckGemini_BrokenTruncated(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installGemini(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Corrupt the block by removing end marker
	path := filepath.Join(tmpHome, ".gemini", "GEMINI.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read GEMINI.md: %v", err)
	}

	corrupted := strings.ReplaceAll(string(data), geminiBlockEnd, "")
	if err := os.WriteFile(path, []byte(corrupted), 0644); err != nil {
		t.Fatalf("write corrupted GEMINI.md: %v", err)
	}

	issues, state := CheckGemini(tmpHome)
	if state != StateBroken {
		t.Fatalf("expected StateBroken, got %v", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected at least one issue")
	}
	if !strings.Contains(issues[0].Problem, "managed block truncated") {
		t.Fatalf("expected truncation message, got: %s", issues[0].Problem)
	}
}

func TestCheckGemini_BrokenOrphanedEnd(t *testing.T) {
	tmpHome := t.TempDir()
	geminiDir := filepath.Join(tmpHome, ".gemini")
	if err := os.MkdirAll(geminiDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// End marker without begin marker
	geminiFilePath := filepath.Join(geminiDir, "GEMINI.md")
	content := "# Config\n" + geminiBlockEnd + "\n"
	if err := os.WriteFile(geminiFilePath, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	issues, state := CheckGemini(tmpHome)
	if state != StateBroken {
		t.Fatalf("expected StateBroken for orphaned end marker, got %v", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected issues for orphaned end marker, got none")
	}
	expected := "managed block has invalid topology: orphaned end marker without begin marker; refusing to mutate"
	if issues[0].Problem != expected {
		t.Fatalf("expected %q, got %q", expected, issues[0].Problem)
	}
	if issues[0].Repair != "waggle install gemini" {
		t.Fatalf("expected repair 'waggle install gemini', got %q", issues[0].Repair)
	}
}

func TestCheckGemini_BrokenDuplicateBegin(t *testing.T) {
	tmpHome := t.TempDir()
	geminiDir := filepath.Join(tmpHome, ".gemini")
	if err := os.MkdirAll(geminiDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	geminiFilePath := filepath.Join(geminiDir, "GEMINI.md")
	content := geminiBlockBegin + "\nfirst\n" + geminiBlockEnd + "\n" + geminiBlockBegin + "\nsecond\n" + geminiBlockEnd + "\n"
	if err := os.WriteFile(geminiFilePath, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	issues, state := CheckGemini(tmpHome)
	if state != StateBroken {
		t.Fatalf("expected StateBroken for duplicate begin markers, got %v", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected issues for duplicate begin markers, got none")
	}
	expected := "managed block has invalid topology: duplicate begin markers (2 found); refusing to mutate"
	if issues[0].Problem != expected {
		t.Fatalf("expected %q, got %q", expected, issues[0].Problem)
	}
}

func TestCheckGemini_BrokenDuplicateEnd(t *testing.T) {
	tmpHome := t.TempDir()
	geminiDir := filepath.Join(tmpHome, ".gemini")
	if err := os.MkdirAll(geminiDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	geminiFilePath := filepath.Join(geminiDir, "GEMINI.md")
	content := geminiBlockBegin + "\nsome content\n" + geminiBlockEnd + "\nextra\n" + geminiBlockEnd + "\n"
	if err := os.WriteFile(geminiFilePath, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	issues, state := CheckGemini(tmpHome)
	if state != StateBroken {
		t.Fatalf("expected StateBroken for duplicate end markers, got %v", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected issues for duplicate end markers, got none")
	}
	expected := "managed block has invalid topology: duplicate end markers (2 found); refusing to mutate"
	if issues[0].Problem != expected {
		t.Fatalf("expected %q, got %q", expected, issues[0].Problem)
	}
}

func TestCheckGemini_BrokenReversedMarkers(t *testing.T) {
	tmpHome := t.TempDir()
	geminiDir := filepath.Join(tmpHome, ".gemini")
	if err := os.MkdirAll(geminiDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	geminiFilePath := filepath.Join(geminiDir, "GEMINI.md")
	content := geminiBlockEnd + "\ncontent\n" + geminiBlockBegin + "\n"
	if err := os.WriteFile(geminiFilePath, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	issues, state := CheckGemini(tmpHome)
	if state != StateBroken {
		t.Fatalf("expected StateBroken for reversed markers, got %v", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected issues for reversed markers, got none")
	}
	expected := "managed block has invalid topology: end marker appears before begin marker; refusing to mutate"
	if issues[0].Problem != expected {
		t.Fatalf("expected %q, got %q", expected, issues[0].Problem)
	}
}

func TestCheckGemini_BrokenBeginNotAtLineStart(t *testing.T) {
	tmpHome := t.TempDir()
	geminiDir := filepath.Join(tmpHome, ".gemini")
	if err := os.MkdirAll(geminiDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	geminiFilePath := filepath.Join(geminiDir, "GEMINI.md")
	content := "prefix " + geminiBlockBegin + "\nsome content\n" + geminiBlockEnd + "\n"
	if err := os.WriteFile(geminiFilePath, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	issues, state := CheckGemini(tmpHome)
	if state != StateBroken {
		t.Fatalf("expected StateBroken for begin marker not at line start, got %v", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected issues for begin marker not at line start, got none")
	}
	expected := "managed block has invalid topology: begin marker not at start of line; refusing to mutate"
	if issues[0].Problem != expected {
		t.Fatalf("expected %q, got %q", expected, issues[0].Problem)
	}
}

func TestCheckGemini_BrokenReadError(t *testing.T) {
	tmpHome := t.TempDir()
	geminiDir := filepath.Join(tmpHome, ".gemini")
	if err := os.MkdirAll(geminiDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Create GEMINI.md as a directory — ReadFile will fail
	geminiFilePath := filepath.Join(geminiDir, "GEMINI.md")
	if err := os.MkdirAll(geminiFilePath, 0755); err != nil {
		t.Fatalf("mkdir GEMINI.md: %v", err)
	}

	issues, state := CheckGemini(tmpHome)
	if state != StateBroken {
		t.Fatalf("expected StateBroken for read error, got %v", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected issues for read error, got none")
	}
	if !strings.Contains(issues[0].Problem, "failed to read GEMINI.md") {
		t.Fatalf("expected read error message, got: %s", issues[0].Problem)
	}
}

func TestEmbeddedGeminiFilesMatchSource(t *testing.T) {
	sourceDir := filepath.Join("..", "..", "integrations", "gemini")
	embedDir := filepath.Join("gemini")

	if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
		t.Skip("source directory not found (running outside repo root)")
	}

	sourceFiles := make(map[string][]byte)
	err := filepath.WalkDir(sourceDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(sourceDir, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sourceFiles[rel] = data
		return nil
	})
	if err != nil {
		t.Fatalf("walking source dir: %v", err)
	}

	embedFiles := make(map[string][]byte)
	err = filepath.WalkDir(embedDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(embedDir, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		embedFiles[rel] = data
		return nil
	})
	if err != nil {
		t.Fatalf("walking embed dir: %v", err)
	}

	for name, sourceData := range sourceFiles {
		embedData, ok := embedFiles[name]
		if !ok {
			t.Errorf("file %s exists in integrations/gemini/ but not in internal/install/gemini/", name)
			continue
		}
		if !bytes.Equal(sourceData, embedData) {
			t.Errorf("embedded copy diverged from source: %s", name)
		}
	}

	for name := range embedFiles {
		if _, ok := sourceFiles[name]; !ok {
			t.Errorf("file %s exists in internal/install/gemini/ but not in integrations/gemini/", name)
		}
	}
}
