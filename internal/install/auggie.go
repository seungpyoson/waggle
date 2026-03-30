package install

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	auggieBlockBegin = "<!-- WAGGLE-AUGGIE-BEGIN -->"
	auggieBlockEnd   = "<!-- WAGGLE-AUGGIE-END -->"
)

// The canonical Auggie integration assets live in integrations/auggie/.
// This mirrored copy exists in-package so go:embed can bundle them for install.
//
//go:embed all:auggie
var auggieFiles embed.FS

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

	blockData, err := auggieFiles.ReadFile("auggie/RULE-block.md")
	if err != nil {
		return fmt.Errorf("reading embedded Auggie rule block: %w", err)
	}

	if err := repairTruncatedAuggieBlock(rulesPath, string(blockData)); err != nil {
		return fmt.Errorf("repairing truncated Auggie waggle rule: %w", err)
	}

	if err := upsertManagedBlock(rulesPath, auggieBlockBegin, auggieBlockEnd, string(blockData)); err != nil {
		return fmt.Errorf("updating Auggie waggle rule: %w", err)
	}

	return nil
}

func repairTruncatedAuggieBlock(path, blockBody string) error {
	current, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	content := string(current)
	idx := strings.Index(content, auggieBlockBegin)
	if idx < 0 {
		return nil
	}
	if strings.Contains(content[idx:], auggieBlockEnd) {
		return nil
	}

	canonicalBlock := canonicalManagedBlock(auggieBlockBegin, auggieBlockEnd, blockBody)
	truncatedSuffix := content[idx:]
	matchLen := longestMatchingPrefix(canonicalBlock, truncatedSuffix)
	if matchLen < len(auggieBlockBegin) {
		return fmt.Errorf("managed block start found without end marker in %s; refusing to repair non-canonical Auggie block", path)
	}

	repaired := content[:idx] + canonicalBlock + truncatedSuffix[matchLen:]
	return os.WriteFile(path, managedBlockBytes(repaired, truncatedSuffix[matchLen:] == ""), 0644)
}

func longestMatchingPrefix(want, got string) int {
	max := len(want)
	if len(got) < max {
		max = len(got)
	}

	for i := 0; i < max; i++ {
		if want[i] != got[i] {
			return i
		}
	}

	return max
}

func uninstallAuggie(homeDir string) error {
	rulesPath := filepath.Join(homeDir, ".augment", "rules", "waggle.md")

	blockData, err := auggieFiles.ReadFile("auggie/RULE-block.md")
	if err != nil {
		return fmt.Errorf("reading embedded Auggie rule block: %w", err)
	}
	if err := repairTruncatedAuggieBlock(rulesPath, string(blockData)); err != nil {
		return fmt.Errorf("repairing truncated Auggie waggle rule: %w", err)
	}

	if err := removeManagedBlock(rulesPath, auggieBlockBegin, auggieBlockEnd); err != nil {
		return fmt.Errorf("updating Auggie waggle rule: %w", err)
	}

	return nil
}
