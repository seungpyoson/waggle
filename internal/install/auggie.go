package install

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed all:auggie
var auggieFiles embed.FS

// auggieHeader marks the file as managed by waggle.
const auggieHeader = "<!-- Managed by waggle. Do not edit. Custom rules go in a separate file. -->\n"

func InstallAuggie() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}
	return installAuggie(home)
}

func UninstallAuggie() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}
	return uninstallAuggie(home)
}

// rejectNonRegularFile returns an error if path exists and is not a regular file
// (e.g., a symlink, directory, or device). This protects the owned-file model
// from symlink attacks that could write to or delete unrelated files.
func rejectNonRegularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // does not exist yet — will be created as regular file
		}
		return err
	}
	if info.Mode()&os.ModeType != 0 {
		return fmt.Errorf("%s is not a regular file (mode: %s); remove it manually before installing", path, info.Mode().Type())
	}
	return nil
}

func installAuggie(homeDir string) error {
	rulesDir := filepath.Join(homeDir, ".augment", "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		return fmt.Errorf("creating Auggie rules dir: %w", err)
	}
	rulesPath := filepath.Join(rulesDir, "waggle.md")

	if err := rejectNonRegularFile(rulesPath); err != nil {
		return err
	}

	content, err := canonicalAuggieFile()
	if err != nil {
		return err
	}

	return os.WriteFile(rulesPath, []byte(content), 0644)
}

func uninstallAuggie(homeDir string) error {
	rulesPath := filepath.Join(homeDir, ".augment", "rules", "waggle.md")
	if err := rejectNonRegularFile(rulesPath); err != nil {
		return err
	}
	err := os.Remove(rulesPath)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// canonicalAuggieFile returns the exact content waggle writes to waggle.md.
func canonicalAuggieFile() (string, error) {
	blockData, err := auggieFiles.ReadFile("auggie/RULE-block.md")
	if err != nil {
		return "", fmt.Errorf("reading embedded Auggie rule block: %w", err)
	}
	return auggieHeader + strings.TrimSpace(string(blockData)) + "\n", nil
}
