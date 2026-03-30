package install

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// ===========================================================================
// Adversarial tests for the Auggie owned-file model.
// waggle owns ~/.augment/rules/waggle.md entirely. Users put custom rules
// in separate files in the same directory.
// ===========================================================================

// ---------------------------------------------------------------------------
// InstallOverwritesUserEdits — user edits waggle.md, install overwrites, healthy
// ---------------------------------------------------------------------------
func TestAdversarial_OwnedFile_InstallOverwritesUserEdits(t *testing.T) {
	tmpHome := t.TempDir()

	// Install canonical file
	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("initial install failed: %v", err)
	}

	// User manually edits waggle.md
	rulesPath := filepath.Join(tmpHome, ".augment", "rules", "waggle.md")
	if err := os.WriteFile(rulesPath, []byte("# User edited this file\nCustom content here.\n"), 0644); err != nil {
		t.Fatalf("write user edits: %v", err)
	}

	// Verify health detects the edit as broken
	issues, state := CheckAuggie(tmpHome)
	if state != StateBroken {
		t.Errorf("expected StateBroken after user edits, got %q", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected health issues for user-edited content, got none")
	}

	// Reinstall — must overwrite user edits
	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("reinstall failed: %v", err)
	}

	// Must be healthy now
	issues, state = CheckAuggie(tmpHome)
	if state != StateHealthy {
		t.Errorf("expected StateHealthy after reinstall, got %q", state)
		for _, issue := range issues {
			t.Errorf("  issue: %s: %s", issue.Asset, issue.Problem)
		}
	}

	// Content must match canonical
	data, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	canonical := canonicalAuggieFileForTest(t)
	if string(data) != canonical {
		t.Fatalf("content not canonical after reinstall:\nwant: %q\ngot:  %q", canonical, string(data))
	}
}

// ---------------------------------------------------------------------------
// UninstallLeavesOtherFiles — other files in ~/.augment/rules/ untouched
// ---------------------------------------------------------------------------
func TestAdversarial_OwnedFile_UninstallLeavesOtherFiles(t *testing.T) {
	tmpHome := t.TempDir()
	rulesDir := filepath.Join(tmpHome, ".augment", "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Create user's own rule file
	userFile := filepath.Join(rulesDir, "my-rules.md")
	userContent := "# My personal rules\n- Be kind\n- Write tests\n"
	if err := os.WriteFile(userFile, []byte(userContent), 0644); err != nil {
		t.Fatalf("write user file: %v", err)
	}

	// Install waggle
	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Verify both files exist
	if _, err := os.Stat(filepath.Join(rulesDir, "waggle.md")); err != nil {
		t.Fatalf("waggle.md missing after install: %v", err)
	}
	if _, err := os.Stat(userFile); err != nil {
		t.Fatalf("user file missing after install: %v", err)
	}

	// Uninstall waggle
	if err := uninstallAuggie(tmpHome); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}

	// waggle.md must be gone
	if _, err := os.Stat(filepath.Join(rulesDir, "waggle.md")); !os.IsNotExist(err) {
		t.Fatalf("waggle.md still exists after uninstall (err=%v)", err)
	}

	// User's file must be untouched
	data, err := os.ReadFile(userFile)
	if err != nil {
		t.Fatalf("read user file: %v", err)
	}
	if string(data) != userContent {
		t.Fatalf("user file modified by uninstall:\nwant: %q\ngot:  %q", userContent, string(data))
	}

	// Rules directory itself must still exist
	if _, err := os.Stat(rulesDir); err != nil {
		t.Fatalf("rules directory removed by uninstall: %v", err)
	}
}

// ---------------------------------------------------------------------------
// PerfectRoundTrip — install then uninstall leaves no trace
// ---------------------------------------------------------------------------
func TestAdversarial_OwnedFile_PerfectRoundTrip(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	rulesPath := filepath.Join(tmpHome, ".augment", "rules", "waggle.md")
	if _, err := os.Stat(rulesPath); err != nil {
		t.Fatalf("waggle.md missing after install: %v", err)
	}

	if err := uninstallAuggie(tmpHome); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}

	// File must be completely gone — no orphaned bytes
	if _, err := os.Stat(rulesPath); !os.IsNotExist(err) {
		t.Fatalf("waggle.md still exists after round-trip (err=%v)", err)
	}
}

// ---------------------------------------------------------------------------
// HealthRejectsPartialContent — partial content is detected as broken
// ---------------------------------------------------------------------------
func TestAdversarial_OwnedFile_HealthRejectsPartialContent(t *testing.T) {
	tmpHome := t.TempDir()
	rulesDir := filepath.Join(tmpHome, ".augment", "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Write only the header (partial canonical content)
	rulesPath := filepath.Join(rulesDir, "waggle.md")
	if err := os.WriteFile(rulesPath, []byte(auggieHeader), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	issues, state := CheckAuggie(tmpHome)
	if state != StateBroken {
		t.Errorf("expected StateBroken for partial content, got %q", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected health issues for partial content, got none")
	}
}

// ---------------------------------------------------------------------------
// HealthRejectsEmptyFile — empty waggle.md is detected as broken
// ---------------------------------------------------------------------------
func TestAdversarial_OwnedFile_HealthRejectsEmptyFile(t *testing.T) {
	tmpHome := t.TempDir()
	rulesDir := filepath.Join(tmpHome, ".augment", "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	rulesPath := filepath.Join(rulesDir, "waggle.md")
	if err := os.WriteFile(rulesPath, []byte(""), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	issues, state := CheckAuggie(tmpHome)
	if state != StateBroken {
		t.Errorf("expected StateBroken for empty file, got %q", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected health issues for empty file, got none")
	}
}

// ---------------------------------------------------------------------------
// ConcurrentInstall — two concurrent installs produce canonical file
// ---------------------------------------------------------------------------
func TestAdversarial_OwnedFile_ConcurrentInstall(t *testing.T) {
	tmpHome := t.TempDir()

	var wg sync.WaitGroup
	errs := make([]error, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = installAuggie(tmpHome)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent install %d failed: %v", i, err)
		}
	}

	// File must be canonical regardless of race
	rulesPath := filepath.Join(tmpHome, ".augment", "rules", "waggle.md")
	data, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	canonical := canonicalAuggieFileForTest(t)
	if string(data) != canonical {
		t.Fatalf("content not canonical after concurrent install:\nwant: %q\ngot:  %q", canonical, string(data))
	}

	issues, state := CheckAuggie(tmpHome)
	if state != StateHealthy {
		t.Errorf("expected StateHealthy after concurrent install, got %q", state)
		for _, issue := range issues {
			t.Errorf("  issue: %s: %s", issue.Asset, issue.Problem)
		}
	}
}
