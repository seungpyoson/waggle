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

	if err := InstallGemini(tmpHome); err != nil {
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
}

func TestInstall_GeminiIdempotent(t *testing.T) {
	tmpHome := t.TempDir()

	if err := InstallGemini(tmpHome); err != nil {
		t.Fatalf("first install failed: %v", err)
	}
	if err := InstallGemini(tmpHome); err != nil {
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

	if err := InstallGemini(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if err := UninstallGemini(tmpHome); err != nil {
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

	if err := InstallGemini(tmpHome); err != nil {
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

	if err := InstallGemini(tmpHome); err != nil {
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

func TestCheckGemini_Broken(t *testing.T) {
	tmpHome := t.TempDir()

	if err := InstallGemini(tmpHome); err != nil {
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
		t.Fatalf("expected at least one issue for broken state, got %d", len(issues))
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
