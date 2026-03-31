package install

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

const (
	augmentBlockBegin = "<!-- WAGGLE-AUGMENT-BEGIN -->"
	augmentBlockEnd   = "<!-- WAGGLE-AUGMENT-END -->"
)

// The canonical Augment integration assets live in integrations/augment/.
// This mirrored copy exists in-package so go:embed can bundle them for install.
//
//go:embed all:augment
var augmentFiles embed.FS

func InstallAugment() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}
	return installAugment(home)
}

func UninstallAugment() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}
	return uninstallAugment(home)
}

func installAugment(homeDir string) error {
	augmentDir := filepath.Join(homeDir, ".augment")
	skillDir := filepath.Join(augmentDir, "skills")
	if err := safeMkdirAll(skillDir, homeDir, 0o755); err != nil {
		return fmt.Errorf("creating Augment skill dir: %w", err)
	}

	blockData, err := augmentFiles.ReadFile("augment/SKILL-block.md")
	if err != nil {
		return fmt.Errorf("reading embedded Augment block: %w", err)
	}
	if err := upsertManagedBlock(filepath.Join(skillDir, "waggle.md"), augmentBlockBegin, augmentBlockEnd, string(blockData), homeDir); err != nil {
		return fmt.Errorf("updating Augment waggle.md: %w", err)
	}

	if err := installShellHook(homeDir); err != nil {
		return fmt.Errorf("installing shell hook: %w", err)
	}

	return nil
}

func uninstallAugment(homeDir string) error {
	augmentDir := filepath.Join(homeDir, ".augment")
	skillPath := filepath.Join(augmentDir, "skills", "waggle.md")

	if err := removeManagedBlock(skillPath, augmentBlockBegin, augmentBlockEnd, homeDir); err != nil {
		return fmt.Errorf("removing managed block from Augment waggle.md: %w", err)
	}

	return nil
}
