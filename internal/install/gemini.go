package install

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

const (
	geminiBlockBegin = "<!-- WAGGLE-GEMINI-BEGIN -->"
	geminiBlockEnd   = "<!-- WAGGLE-GEMINI-END -->"
)

// The canonical Gemini integration assets live in integrations/gemini/.
// This mirrored copy exists in-package so go:embed can bundle them for install.
//
//go:embed all:gemini
var geminiFiles embed.FS

func InstallGemini() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}
	return installGemini(home)
}

func UninstallGemini() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}
	return uninstallGemini(home)
}

func installGemini(homeDir string) error {
	geminiDir := filepath.Join(homeDir, ".gemini")
	if err := safeMkdirAll(geminiDir, homeDir, 0o755); err != nil {
		return fmt.Errorf("creating Gemini dir: %w", err)
	}

	blockData, err := geminiFiles.ReadFile("gemini/GEMINI-block.md")
	if err != nil {
		return fmt.Errorf("reading embedded Gemini block: %w", err)
	}
	if err := upsertManagedBlock(filepath.Join(geminiDir, "GEMINI.md"), geminiBlockBegin, geminiBlockEnd, string(blockData), homeDir); err != nil {
		return fmt.Errorf("updating Gemini GEMINI.md: %w", err)
	}

	if err := installShellHook(homeDir); err != nil {
		return fmt.Errorf("installing shell hook: %w", err)
	}

	return nil
}

func uninstallGemini(homeDir string) error {
	geminiDir := filepath.Join(homeDir, ".gemini")

	if err := removeManagedBlock(filepath.Join(geminiDir, "GEMINI.md"), geminiBlockBegin, geminiBlockEnd, homeDir); err != nil {
		return fmt.Errorf("updating Gemini GEMINI.md: %w", err)
	}

	return nil
}
