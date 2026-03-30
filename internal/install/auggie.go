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

func installAuggie(homeDir string) error {
	rulesDir := filepath.Join(homeDir, ".augment", "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		return fmt.Errorf("creating Auggie rules dir: %w", err)
	}
	rulesPath := filepath.Join(rulesDir, "waggle.md")

	content, err := canonicalAuggieFile()
	if err != nil {
		return err
	}

	return os.WriteFile(rulesPath, []byte(content), 0644)
}

func uninstallAuggie(homeDir string) error {
	rulesPath := filepath.Join(homeDir, ".augment", "rules", "waggle.md")
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
