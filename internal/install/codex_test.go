package install

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallCodex_SkillCreated(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installCodex(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	skillPath := filepath.Join(tmpHome, ".codex", "skills", "waggle-runtime", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("skill not created: %v", err)
	}
}

func TestInstallCodex_AGENTSManagedBlockCreated(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installCodex(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpHome, ".codex", "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, codexBlockBegin) || !strings.Contains(content, codexBlockEnd) {
		t.Fatalf("managed block markers missing:\n%s", content)
	}
	if !strings.Contains(content, "$waggle-runtime") {
		t.Fatalf("expected waggle-runtime skill reference in AGENTS block:\n%s", content)
	}
}

func TestInstallCodex_Idempotent(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installCodex(tmpHome); err != nil {
		t.Fatalf("first install failed: %v", err)
	}
	if err := installCodex(tmpHome); err != nil {
		t.Fatalf("second install failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpHome, ".codex", "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}

	if got := strings.Count(string(data), codexBlockBegin); got != 1 {
		t.Fatalf("expected 1 managed block, got %d", got)
	}
}

func TestInstallCodex_PreservesExistingAGENTS(t *testing.T) {
	tmpHome := t.TempDir()
	codexDir := filepath.Join(tmpHome, ".codex")
	if err := os.MkdirAll(codexDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	original := "# Existing Rules\n- keep this\n"
	if err := os.WriteFile(filepath.Join(codexDir, "AGENTS.md"), []byte(original), 0644); err != nil {
		t.Fatalf("write AGENTS: %v", err)
	}

	if err := installCodex(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(codexDir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "# Existing Rules") {
		t.Fatalf("existing content removed:\n%s", content)
	}
	if !strings.Contains(content, codexBlockBegin) {
		t.Fatalf("managed block missing:\n%s", content)
	}
}

func TestUninstallCodex_RemovesManagedBlockAndSkill(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installCodex(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if err := uninstallCodex(tmpHome); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(tmpHome, ".codex", "skills", "waggle-runtime")); !os.IsNotExist(err) {
		t.Fatalf("skill directory still exists after uninstall")
	}

	data, err := os.ReadFile(filepath.Join(tmpHome, ".codex", "AGENTS.md"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if err == nil && strings.Contains(string(data), codexBlockBegin) {
		t.Fatalf("managed block still present after uninstall:\n%s", string(data))
	}
}

func TestUninstallCodex_PreservesOtherAGENTSContent(t *testing.T) {
	tmpHome := t.TempDir()
	codexDir := filepath.Join(tmpHome, ".codex")
	if err := os.MkdirAll(codexDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	original := "# Existing Rules\n- keep this\n"
	if err := os.WriteFile(filepath.Join(codexDir, "AGENTS.md"), []byte(original), 0644); err != nil {
		t.Fatalf("write AGENTS: %v", err)
	}

	if err := installCodex(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if err := uninstallCodex(tmpHome); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(codexDir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if string(data) != original {
		t.Fatalf("AGENTS content changed unexpectedly:\nwant:\n%s\ngot:\n%s", original, string(data))
	}
}

func TestEmbeddedCodexFilesMatchSource(t *testing.T) {
	sourceDir := filepath.Join("..", "..", "integrations", "codex")
	embedDir := filepath.Join("codex")

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
			t.Errorf("file %s exists in integrations/codex/ but not in internal/install/codex/", name)
			continue
		}
		if !bytes.Equal(sourceData, embedData) {
			t.Errorf("embedded copy diverged from source: %s", name)
		}
	}

	for name := range embedFiles {
		if _, ok := sourceFiles[name]; !ok {
			t.Errorf("file %s exists in internal/install/codex/ but not in integrations/codex/", name)
		}
	}
}
