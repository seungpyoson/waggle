package install

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDetectPlatformsUsesHomeAndPathEvidence(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, ".gemini"), 0o755); err != nil {
		t.Fatal(err)
	}

	lookPath := func(name string) (string, error) {
		if name == "claude" {
			return "/usr/local/bin/claude", nil
		}
		return "", os.ErrNotExist
	}

	detections := DetectPlatforms(home, lookPath)
	var got []string
	for _, detection := range detections {
		if detection.Found {
			got = append(got, detection.Name)
		}
	}
	want := []string{PlatformClaudeCode, PlatformCodex, PlatformGemini}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("detected platforms = %v, want %v", got, want)
	}
}

func TestPathExistsReturnsFalseOnPermissionError(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "parent")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(parent, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(parent, 0o755)
	})

	if pathExists(child) {
		t.Fatal("pathExists should return false when stat fails with permission denied")
	}
}

func TestInstallDetectedInHomeInstallsOnlyDetectedPlatforms(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}

	results, err := InstallDetectedInHome(home, func(string) (string, error) {
		return "", os.ErrNotExist
	})
	if err != nil {
		t.Fatalf("InstallDetectedInHome: %v", err)
	}
	if len(results) != 1 || results[0].Platform != PlatformCodex {
		t.Fatalf("results = %+v, want only Codex", results)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "skills", "waggle-runtime", "SKILL.md")); err != nil {
		t.Fatalf("Codex skill not installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini", "GEMINI.md")); !os.IsNotExist(err) {
		t.Fatalf("Gemini should not have been installed, stat err = %v", err)
	}
}
