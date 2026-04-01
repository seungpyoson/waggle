package install

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

const (
	codexBlockBegin = "<!-- WAGGLE-CODEX-BEGIN -->"
	codexBlockEnd   = "<!-- WAGGLE-CODEX-END -->"
)

// The canonical Codex integration assets live in integrations/codex/.
// This mirrored copy exists in-package so go:embed can bundle them for install.
//
//go:embed all:codex
var codexFiles embed.FS

func InstallCodex() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}
	return installCodex(home)
}

func UninstallCodex() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}
	return uninstallCodex(home)
}

func installCodex(homeDir string) error {
	codexDir := filepath.Join(homeDir, ".codex")
	skillDir := filepath.Join(codexDir, "skills", "waggle-runtime")
	if err := safeMkdirAll(skillDir, homeDir, 0o755); err != nil {
		return fmt.Errorf("creating Codex skill dir: %w", err)
	}

	skillData, err := codexFiles.ReadFile("codex/skills/waggle-runtime/SKILL.md")
	if err != nil {
		return fmt.Errorf("reading embedded Codex skill: %w", err)
	}
	if err := safeWriteFile(filepath.Join(skillDir, "SKILL.md"), skillData, 0o644, homeDir); err != nil {
		return fmt.Errorf("writing Codex skill: %w", err)
	}

	blockData, err := codexFiles.ReadFile("codex/AGENTS-block.md")
	if err != nil {
		return fmt.Errorf("reading embedded Codex AGENTS block: %w", err)
	}
	if err := upsertManagedBlock(filepath.Join(codexDir, "AGENTS.md"), codexBlockBegin, codexBlockEnd, string(blockData), homeDir); err != nil {
		return fmt.Errorf("updating Codex AGENTS.md: %w", err)
	}

	if err := installShellHook(homeDir); err != nil {
		return fmt.Errorf("installing shell hook: %w", err)
	}

	return nil
}

func uninstallCodex(homeDir string) error {
	codexDir := filepath.Join(homeDir, ".codex")

	if err := safeRemoveAll(filepath.Join(codexDir, "skills", "waggle-runtime"), homeDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing Codex skill directory: %w", err)
	}

	if err := removeManagedBlock(filepath.Join(codexDir, "AGENTS.md"), codexBlockBegin, codexBlockEnd, homeDir); err != nil {
		return fmt.Errorf("updating Codex AGENTS.md: %w", err)
	}

	return nil
}
