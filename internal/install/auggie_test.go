package install

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallAuggie_RuleCreated(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	rulesPath := filepath.Join(tmpHome, ".augment", "rules", "waggle.md")
	if _, err := os.Stat(rulesPath); err != nil {
		t.Fatalf("waggle.md not created: %v", err)
	}
}

func TestInstallAuggie_ManagedBlockCreated(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpHome, ".augment", "rules", "waggle.md"))
	if err != nil {
		t.Fatalf("read waggle.md: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, auggieBlockBegin) || !strings.Contains(content, auggieBlockEnd) {
		t.Fatalf("managed block markers missing:\n%s", content)
	}
	if !strings.Contains(content, "waggle adapter bootstrap auggie --format markdown") {
		t.Fatalf("expected waggle adapter bootstrap command in rule block:\n%s", content)
	}
}

func TestInstallAuggie_Idempotent(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("first install failed: %v", err)
	}
	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("second install failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpHome, ".augment", "rules", "waggle.md"))
	if err != nil {
		t.Fatalf("read waggle.md: %v", err)
	}

	if got := strings.Count(string(data), auggieBlockBegin); got != 1 {
		t.Fatalf("expected 1 managed block, got %d", got)
	}
}

func TestInstallAuggie_PreservesExistingRules(t *testing.T) {
	tmpHome := t.TempDir()
	rulesDir := filepath.Join(tmpHome, ".augment", "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	original := "# Existing Rules\n- keep this\n"
	if err := os.WriteFile(filepath.Join(rulesDir, "waggle.md"), []byte(original), 0644); err != nil {
		t.Fatalf("write waggle.md: %v", err)
	}

	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(rulesDir, "waggle.md"))
	if err != nil {
		t.Fatalf("read waggle.md: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "# Existing Rules") {
		t.Fatalf("existing content removed:\n%s", content)
	}
	if !strings.Contains(content, auggieBlockBegin) {
		t.Fatalf("managed block missing:\n%s", content)
	}
}

func TestInstallAuggie_RepairTruncatedBlockPreservesTrailingRules(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	rulesPath := filepath.Join(tmpHome, ".augment", "rules", "waggle.md")
	data, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("read waggle.md: %v", err)
	}

	broken := strings.Replace(string(data), auggieBlockEnd, "", 1) + "\n# Personal Rules\n- keep this\n"
	if err := os.WriteFile(rulesPath, []byte(broken), 0644); err != nil {
		t.Fatalf("write broken waggle.md: %v", err)
	}

	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("repair install failed: %v", err)
	}

	after, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("read waggle.md after repair: %v", err)
	}

	canonicalBlock := canonicalAuggieManagedBlock(t)
	brokenSuffix := broken[strings.Index(broken, auggieBlockBegin):]
	want := string(normalizeManagedBlockWhitespace(canonicalBlock + brokenSuffix[longestMatchingPrefix(canonicalBlock, brokenSuffix):]))
	if string(after) != want {
		t.Fatalf("reinstall did not preserve trailing rules as expected:\nwant:\n%s\ngot:\n%s", want, string(after))
	}
	if !strings.Contains(string(after), "# Personal Rules\n- keep this") {
		t.Fatalf("trailing personal rules missing after repair:\n%s", string(after))
	}
}

func TestInstallAuggie_RepairTruncatedBlockAtEOF(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	rulesPath := filepath.Join(tmpHome, ".augment", "rules", "waggle.md")
	original, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("read waggle.md: %v", err)
	}

	broken := strings.TrimSuffix(string(original), auggieBlockEnd+"\n")
	if err := os.WriteFile(rulesPath, []byte(broken), 0644); err != nil {
		t.Fatalf("write broken waggle.md: %v", err)
	}

	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("repair install failed: %v", err)
	}

	after, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("read waggle.md after repair: %v", err)
	}
	if string(after) != string(original) {
		t.Fatalf("EOF truncation repair changed content unexpectedly:\nwant:\n%s\ngot:\n%s", string(original), string(after))
	}
}

func TestUninstallAuggie_RemovesManagedBlock(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if err := uninstallAuggie(tmpHome); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpHome, ".augment", "rules", "waggle.md"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read waggle.md: %v", err)
	}
	if err == nil && strings.Contains(string(data), auggieBlockBegin) {
		t.Fatalf("managed block still present after uninstall:\n%s", string(data))
	}
}

func TestUninstallAuggie_PreservesOtherRulesContent(t *testing.T) {
	tmpHome := t.TempDir()
	rulesDir := filepath.Join(tmpHome, ".augment", "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	original := "# Existing Rules\n- keep this\n"
	if err := os.WriteFile(filepath.Join(rulesDir, "waggle.md"), []byte(original), 0644); err != nil {
		t.Fatalf("write waggle.md: %v", err)
	}

	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if err := uninstallAuggie(tmpHome); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(rulesDir, "waggle.md"))
	if err != nil {
		t.Fatalf("read waggle.md: %v", err)
	}
	if string(data) != original {
		t.Fatalf("waggle.md content changed unexpectedly:\nwant:\n%s\ngot:\n%s", original, string(data))
	}
}

func TestEmbeddedAuggieFilesMatchSource(t *testing.T) {
	sourceDir := filepath.Join("..", "..", "integrations", "auggie")
	embedDir := filepath.Join("auggie")

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
			t.Errorf("file %s exists in integrations/auggie/ but not in internal/install/auggie/", name)
			continue
		}
		if !bytes.Equal(sourceData, embedData) {
			t.Errorf("embedded copy diverged from source: %s", name)
		}
	}

	for name := range embedFiles {
		if _, ok := sourceFiles[name]; !ok {
			t.Errorf("file %s exists in internal/install/auggie/ but not in integrations/auggie/", name)
		}
	}
}

func canonicalAuggieManagedBlock(t *testing.T) string {
	t.Helper()

	blockData, err := auggieFiles.ReadFile("auggie/RULE-block.md")
	if err != nil {
		t.Fatalf("read embedded Auggie block: %v", err)
	}

	return strings.TrimSpace(strings.Join([]string{auggieBlockBegin, strings.TrimSpace(string(blockData)), auggieBlockEnd}, "\n"))
}
