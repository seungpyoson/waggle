package install

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// canonicalAuggieFileForTest returns the canonical waggle.md content, failing the test on error.
func canonicalAuggieFileForTest(t *testing.T) string {
	t.Helper()
	content, err := canonicalAuggieFile()
	if err != nil {
		t.Fatalf("canonicalAuggieFile: %v", err)
	}
	return content
}

// ---------------------------------------------------------------------------
// Install tests
// ---------------------------------------------------------------------------

func TestInstallAuggie_CreatesFile(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	rulesPath := filepath.Join(tmpHome, ".augment", "rules", "waggle.md")
	if _, err := os.Stat(rulesPath); err != nil {
		t.Fatalf("waggle.md not created: %v", err)
	}
}

func TestInstallAuggie_CorrectContent(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	rulesPath := filepath.Join(tmpHome, ".augment", "rules", "waggle.md")
	data, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("read waggle.md: %v", err)
	}

	canonical := canonicalAuggieFileForTest(t)
	if string(data) != canonical {
		t.Fatalf("content mismatch:\nwant: %q\ngot:  %q", canonical, string(data))
	}

	// Verify structure: header + rule body + trailing newline
	if !strings.HasPrefix(string(data), auggieHeader) {
		t.Fatalf("file does not start with managed header:\n%s", string(data))
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Fatal("file does not end with trailing newline")
	}
	if !strings.Contains(string(data), "waggle adapter bootstrap auggie --format markdown") {
		t.Fatalf("expected waggle adapter bootstrap command in content:\n%s", string(data))
	}
}

func TestInstallAuggie_Idempotent(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("first install failed: %v", err)
	}

	first, err := os.ReadFile(filepath.Join(tmpHome, ".augment", "rules", "waggle.md"))
	if err != nil {
		t.Fatalf("read after first install: %v", err)
	}

	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("second install failed: %v", err)
	}

	second, err := os.ReadFile(filepath.Join(tmpHome, ".augment", "rules", "waggle.md"))
	if err != nil {
		t.Fatalf("read after second install: %v", err)
	}

	if !bytes.Equal(first, second) {
		t.Fatalf("second install produced different content:\nfirst:  %q\nsecond: %q", string(first), string(second))
	}
}

func TestInstallAuggie_CreatesParentDir(t *testing.T) {
	tmpHome := t.TempDir()

	// Ensure ~/.augment/rules/ does NOT exist
	rulesDir := filepath.Join(tmpHome, ".augment", "rules")
	if _, err := os.Stat(rulesDir); err == nil {
		t.Fatal("rules dir already exists before install")
	}

	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	info, err := os.Stat(rulesDir)
	if err != nil {
		t.Fatalf("rules dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("rules path is not a directory")
	}
}

func TestInstallAuggie_OverwritesStaleContent(t *testing.T) {
	tmpHome := t.TempDir()
	rulesDir := filepath.Join(tmpHome, ".augment", "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	rulesPath := filepath.Join(rulesDir, "waggle.md")
	stale := "# Old stale content from a previous version\n"
	if err := os.WriteFile(rulesPath, []byte(stale), 0644); err != nil {
		t.Fatalf("write stale: %v", err)
	}

	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	data, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	canonical := canonicalAuggieFileForTest(t)
	if string(data) != canonical {
		t.Fatalf("stale content not overwritten:\nwant: %q\ngot:  %q", canonical, string(data))
	}
}

// ---------------------------------------------------------------------------
// Uninstall tests
// ---------------------------------------------------------------------------

func TestUninstallAuggie_DeletesFile(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	if err := uninstallAuggie(tmpHome); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}

	rulesPath := filepath.Join(tmpHome, ".augment", "rules", "waggle.md")
	if _, err := os.Stat(rulesPath); !os.IsNotExist(err) {
		t.Fatalf("waggle.md still exists after uninstall (err=%v)", err)
	}
}

func TestUninstallAuggie_IdempotentOnMissing(t *testing.T) {
	tmpHome := t.TempDir()

	// Uninstall without prior install — should not error
	if err := uninstallAuggie(tmpHome); err != nil {
		t.Fatalf("uninstall on missing file failed: %v", err)
	}
}

func TestUninstallAuggie_RoundTrip(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Verify file exists after install
	rulesPath := filepath.Join(tmpHome, ".augment", "rules", "waggle.md")
	if _, err := os.Stat(rulesPath); err != nil {
		t.Fatalf("waggle.md missing after install: %v", err)
	}

	if err := uninstallAuggie(tmpHome); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}

	// File must be completely gone
	if _, err := os.Stat(rulesPath); !os.IsNotExist(err) {
		t.Fatalf("waggle.md still exists after round-trip (err=%v)", err)
	}
}

// ---------------------------------------------------------------------------
// Health tests
// ---------------------------------------------------------------------------

func TestCheckAuggie_NotInstalled(t *testing.T) {
	tmpHome := t.TempDir()

	issues, state := CheckAuggie(tmpHome)
	if state != StateNotInstalled {
		t.Errorf("expected StateNotInstalled, got %q", state)
	}
	if len(issues) != 0 {
		t.Errorf("expected no issues, got %d", len(issues))
	}
}

func TestCheckAuggie_Healthy(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	issues, state := CheckAuggie(tmpHome)
	if state != StateHealthy {
		t.Errorf("expected StateHealthy, got %q", state)
		for _, issue := range issues {
			t.Errorf("  issue: %s: %s", issue.Asset, issue.Problem)
		}
	}
}

func TestCheckAuggie_BrokenWrongContent(t *testing.T) {
	tmpHome := t.TempDir()
	rulesDir := filepath.Join(tmpHome, ".augment", "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	rulesPath := filepath.Join(rulesDir, "waggle.md")
	if err := os.WriteFile(rulesPath, []byte("wrong content\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	issues, state := CheckAuggie(tmpHome)
	if state != StateBroken {
		t.Errorf("expected StateBroken, got %q", state)
	}
	if len(issues) == 0 {
		t.Error("expected health issues for wrong content, got none")
	}
}

func TestCheckAuggie_BrokenReadError(t *testing.T) {
	tmpHome := t.TempDir()
	rulesDir := filepath.Join(tmpHome, ".augment", "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	rulesPath := filepath.Join(rulesDir, "waggle.md")
	if err := os.WriteFile(rulesPath, []byte("content"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(rulesPath, 0000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(rulesPath, 0644) })

	issues, state := CheckAuggie(tmpHome)
	if state != StateBroken {
		t.Errorf("expected StateBroken for unreadable file, got %q", state)
	}
	if len(issues) == 0 {
		t.Error("expected health issues for unreadable file, got none")
	}
}

// ---------------------------------------------------------------------------
// Asset sync test
// ---------------------------------------------------------------------------

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
