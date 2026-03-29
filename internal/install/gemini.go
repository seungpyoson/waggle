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

func InstallGemini(homeDir string) error {
	geminiDir := filepath.Join(homeDir, ".gemini")
	if err := os.MkdirAll(geminiDir, 0755); err != nil {
		return fmt.Errorf("creating Gemini dir: %w", err)
	}

	blockData, err := geminiFiles.ReadFile("gemini/GEMINI-block.md")
	if err != nil {
		return fmt.Errorf("reading embedded Gemini block: %w", err)
	}
	if err := upsertManagedBlock(filepath.Join(geminiDir, "GEMINI.md"), geminiBlockBegin, geminiBlockEnd, string(blockData)); err != nil {
		return fmt.Errorf("updating Gemini GEMINI.md: %w", err)
	}

	return nil
}

func UninstallGemini(homeDir string) error {
	geminiDir := filepath.Join(homeDir, ".gemini")

	if err := removeManagedBlock(filepath.Join(geminiDir, "GEMINI.md"), geminiBlockBegin, geminiBlockEnd); err != nil {
		return fmt.Errorf("updating Gemini GEMINI.md: %w", err)
	}

	return nil
}
