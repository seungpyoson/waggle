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
	if !strings.Contains(content, "waggle adapter bootstrap augment") {
		t.Errorf("expected waggle adapter bootstrap command in skill block:\n%s", content)
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

func TestInstall_AugmentUninstallIdempotent(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installAugment(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if err := uninstallAugment(tmpHome); err != nil {
		t.Fatalf("first uninstall failed: %v", err)
	}
	if err := uninstallAugment(tmpHome); err != nil {
		t.Fatalf("second uninstall failed: %v", err)
	}
}

func TestInstall_AugmentUninstallMissingFile(t *testing.T) {
	tmpHome := t.TempDir()

	if err := uninstallAugment(tmpHome); err != nil {
		t.Fatalf("uninstall on missing file failed: %v", err)
	}
}

func TestInstall_AugmentUninstallTruncatedBlock(t *testing.T) {
	tmpHome := t.TempDir()
	augmentDir := filepath.Join(tmpHome, ".augment", "skills")
	os.MkdirAll(augmentDir, 0755)

	skillPath := filepath.Join(augmentDir, "waggle.md")
	os.WriteFile(skillPath, []byte(augmentBlockBegin+"\ntruncated block\n"), 0644)

	// removeManagedBlock self-heals truncated blocks (begin without end)
	// by removing everything from begin marker to EOF
	if err := uninstallAugment(tmpHome); err != nil {
		t.Fatalf("expected self-healing uninstall, got error: %v", err)
	}

	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if strings.Contains(string(data), augmentBlockBegin) {
		t.Errorf("begin marker still present after self-healing uninstall")
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

func TestCheckAugment_BrokenStaleCanonicalContent(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installAugment(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	skillPath := filepath.Join(tmpHome, ".augment", "skills", "waggle.md")
	staleBlock := canonicalManagedBlock(augmentBlockBegin, augmentBlockEnd, "old bootstrap instructions")
	if err := os.WriteFile(skillPath, managedBlockBytes(staleBlock, true), 0644); err != nil {
		t.Fatalf("write stale Augment skill: %v", err)
	}

	issues, state := CheckAugment(tmpHome)
	if state != StateBroken {
		t.Fatalf("expected StateBroken for stale Augment block content, got %q", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected issues for stale Augment block content, got none")
	}
	if !hasHealthIssueContaining(issues, "managed block content does not match expected") {
		t.Fatalf("expected stale managed block content issue, got %+v", issues)
	}
}

func TestCheckAugment_Broken(t *testing.T) {
	tmpHome := t.TempDir()
	augmentDir := filepath.Join(tmpHome, ".augment", "skills")
	os.MkdirAll(augmentDir, 0755)

	// Create skill file with begin marker but missing end marker
	skillPath := filepath.Join(augmentDir, "waggle.md")
	os.WriteFile(skillPath, []byte(augmentBlockBegin+"\nBroken block\n"), 0644)

	issues, state := CheckAugment(tmpHome)

	if state != StateBroken {
		t.Errorf("expected StateBroken, got %q", state)
	}
	if len(issues) == 0 {
		t.Errorf("expected issues for broken block, got none")
	}
}

func TestCheckAugment_ReadError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test not meaningful when running as root")
	}
	tmpHome := t.TempDir()
	augmentDir := filepath.Join(tmpHome, ".augment", "skills")
	os.MkdirAll(augmentDir, 0755)

	// Create skill file then remove read permission
	skillPath := filepath.Join(augmentDir, "waggle.md")
	os.WriteFile(skillPath, []byte(augmentBlockBegin+"\ncontent\n"+augmentBlockEnd+"\n"), 0644)
	os.Chmod(skillPath, 0000)
	t.Cleanup(func() { os.Chmod(skillPath, 0644) })

	issues, state := CheckAugment(tmpHome)

	if state != StateBroken {
		t.Errorf("expected StateBroken for unreadable file, got %q", state)
	}
	if len(issues) == 0 {
		t.Errorf("expected issues for unreadable file, got none")
	}
}

func TestCheckAugment_OrphanedEndMarker(t *testing.T) {
	tmpHome := t.TempDir()
	augmentDir := filepath.Join(tmpHome, ".augment", "skills")
	os.MkdirAll(augmentDir, 0755)

	skillPath := filepath.Join(augmentDir, "waggle.md")
	os.WriteFile(skillPath, []byte(augmentBlockEnd+"\norphaned end\n"), 0644)

	issues, state := CheckAugment(tmpHome)

	if state != StateBroken {
		t.Errorf("expected StateBroken for orphaned end marker, got %q", state)
	}
	if len(issues) == 0 {
		t.Errorf("expected issues for orphaned end marker, got none")
	}
}

func TestCheckAugment_DuplicateBeginMarkers(t *testing.T) {
	tmpHome := t.TempDir()
	augmentDir := filepath.Join(tmpHome, ".augment", "skills")
	os.MkdirAll(augmentDir, 0755)

	skillPath := filepath.Join(augmentDir, "waggle.md")
	content := augmentBlockBegin + "\nfirst\n" + augmentBlockEnd + "\n" + augmentBlockBegin + "\nsecond\n" + augmentBlockEnd + "\n"
	os.WriteFile(skillPath, []byte(content), 0644)

	issues, state := CheckAugment(tmpHome)

	if state != StateBroken {
		t.Errorf("expected StateBroken for duplicate markers, got %q", state)
	}
	if len(issues) == 0 {
		t.Errorf("expected issues for duplicate markers, got none")
	}
}

func TestCheckAugment_InvertedMarkers(t *testing.T) {
	tmpHome := t.TempDir()
	augmentDir := filepath.Join(tmpHome, ".augment", "skills")
	os.MkdirAll(augmentDir, 0755)

	skillPath := filepath.Join(augmentDir, "waggle.md")
	os.WriteFile(skillPath, []byte(augmentBlockEnd+"\ncontent\n"+augmentBlockBegin+"\n"), 0644)

	issues, state := CheckAugment(tmpHome)

	if state != StateBroken {
		t.Errorf("expected StateBroken for inverted markers, got %q", state)
	}
	if len(issues) == 0 {
		t.Errorf("expected issues for inverted markers, got none")
	}
}

func TestCheckAugment_DuplicateEndMarkers(t *testing.T) {
	tmpHome := t.TempDir()
	augmentDir := filepath.Join(tmpHome, ".augment", "skills")
	os.MkdirAll(augmentDir, 0755)

	skillPath := filepath.Join(augmentDir, "waggle.md")
	content := augmentBlockBegin + "\ncontent\n" + augmentBlockEnd + "\nextra\n" + augmentBlockEnd + "\n"
	os.WriteFile(skillPath, []byte(content), 0644)

	issues, state := CheckAugment(tmpHome)

	if state != StateBroken {
		t.Errorf("expected StateBroken for duplicate end markers, got %q", state)
	}
	if len(issues) == 0 {
		t.Errorf("expected issues for duplicate end markers, got none")
	}
}

func TestCheckAugment_BeginNotAtStartOfLine(t *testing.T) {
	tmpHome := t.TempDir()
	augmentDir := filepath.Join(tmpHome, ".augment", "skills")
	os.MkdirAll(augmentDir, 0755)

	skillPath := filepath.Join(augmentDir, "waggle.md")
	content := "prefix " + augmentBlockBegin + "\ncontent\n" + augmentBlockEnd + "\n"
	os.WriteFile(skillPath, []byte(content), 0644)

	issues, state := CheckAugment(tmpHome)

	if state != StateBroken {
		t.Errorf("expected StateBroken for begin not at start of line, got %q", state)
	}
	if len(issues) == 0 {
		t.Errorf("expected issues for begin not at start of line, got none")
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
