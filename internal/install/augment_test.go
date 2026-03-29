package install

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstall_AugmentCreatesBlock(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installAugment(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	skillPath := filepath.Join(tmpHome, ".augment", "skills", "waggle.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("skill file not created: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, augmentBlockBegin) {
		t.Errorf("begin marker not found in waggle.md")
	}
	if !strings.Contains(content, augmentBlockEnd) {
		t.Errorf("end marker not found in waggle.md")
	}
}

func TestInstall_AugmentIdempotent(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installAugment(tmpHome); err != nil {
		t.Fatalf("first install failed: %v", err)
	}
	if err := installAugment(tmpHome); err != nil {
		t.Fatalf("second install failed: %v", err)
	}

	skillPath := filepath.Join(tmpHome, ".augment", "skills", "waggle.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	count := strings.Count(string(data), augmentBlockBegin)
	if count != 1 {
		t.Errorf("expected 1 block, got %d", count)
	}
}

func TestInstall_AugmentUninstall(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installAugment(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if err := uninstallAugment(tmpHome); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}

	skillPath := filepath.Join(tmpHome, ".augment", "skills", "waggle.md")
	data, err := os.ReadFile(skillPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read failed: %v", err)
	}
	if err == nil {
		content := string(data)
		if strings.Contains(content, augmentBlockBegin) {
			t.Errorf("block still present after uninstall")
		}
	}
}

func TestInstall_AugmentPreservesOtherContent(t *testing.T) {
	tmpHome := t.TempDir()
	augmentDir := filepath.Join(tmpHome, ".augment", "skills")
	os.MkdirAll(augmentDir, 0755)

	skillPath := filepath.Join(augmentDir, "waggle.md")
	original := "# Existing content\n\nKeep this.\n"
	os.WriteFile(skillPath, []byte(original), 0644)

	if err := installAugment(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "# Existing content") {
		t.Errorf("existing content was removed")
	}
	if !strings.Contains(content, augmentBlockBegin) {
		t.Errorf("managed block not added")
	}
}

func TestCheckAugment_NotInstalled(t *testing.T) {
	tmpHome := t.TempDir()
	issues, state := CheckAugment(tmpHome)

	if state != StateNotInstalled {
		t.Errorf("expected StateNotInstalled, got %q", state)
	}
	if len(issues) != 0 {
		t.Errorf("expected no issues, got %d", len(issues))
	}
}

func TestCheckAugment_NotInstalledNoMarker(t *testing.T) {
	tmpHome := t.TempDir()
	augmentDir := filepath.Join(tmpHome, ".augment", "skills")
	os.MkdirAll(augmentDir, 0755)

	// Create skill file without marker
	skillPath := filepath.Join(augmentDir, "waggle.md")
	os.WriteFile(skillPath, []byte("# No marker here\n"), 0644)

	issues, state := CheckAugment(tmpHome)

	if state != StateNotInstalled {
		t.Errorf("expected StateNotInstalled, got %q", state)
	}
	if len(issues) != 0 {
		t.Errorf("expected no issues, got %d", len(issues))
	}
}

func TestCheckAugment_Healthy(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installAugment(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	issues, state := CheckAugment(tmpHome)

	if state != StateHealthy {
		t.Errorf("expected StateHealthy, got %q", state)
	}
	if len(issues) != 0 {
		t.Errorf("expected no issues, got %d: %v", len(issues), issues)
	}
}

func TestCheckAugment_Broken(t *testing.T) {
	tmpHome := t.TempDir()
	augmentDir := filepath.Join(tmpHome, ".augment", "skills")
	os.MkdirAll(augmentDir, 0755)

	// Create skill file with begin marker but missing end marker
	skillPath := filepath.Join(augmentDir, "waggle.md")
	os.WriteFile(skillPath, []byte("<!-- WAGGLE-AUGMENT-BEGIN -->\nBroken block\n"), 0644)

	issues, state := CheckAugment(tmpHome)

	if state != StateBroken {
		t.Errorf("expected StateBroken, got %q", state)
	}
	if len(issues) == 0 {
		t.Errorf("expected issues for broken block, got none")
	}
}

func TestEmbeddedAugmentFilesMatch(t *testing.T) {
	sourceDir := filepath.Join("..", "..", "integrations", "augment")
	embedDir := filepath.Join("augment")

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
			t.Errorf("file %s exists in integrations/augment/ but not in internal/install/augment/", name)
			continue
		}
		if !bytes.Equal(sourceData, embedData) {
			t.Errorf("embedded copy diverged from source: %s", name)
		}
	}

	for name := range embedFiles {
		if _, ok := sourceFiles[name]; !ok {
			t.Errorf("file %s exists in internal/install/augment/ but not in integrations/augment/", name)
		}
	}
}
