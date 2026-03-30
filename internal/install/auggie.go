package install

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// safeWriteFile writes content to path atomically by writing to a temp file
// in the same directory and renaming. This prevents symlink-following writes
// and hard-link inode mutation.
func safeWriteFile(path string, content []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".waggle-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()

	// Clean up temp file on any failure
	defer func() {
		if tmpPath != "" {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("setting permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}
	tmpPath = "" // prevent deferred cleanup
	return nil
}

// validateAncestorPath checks that the directory path resolves to the expected
// location under homeDir. This prevents ancestor-symlink attacks where
// ~/.augment/ or ~/.augment/rules/ is a symlink to an attacker-controlled
// directory.
func validateAncestorPath(dir, homeDir string) error {
	resolvedHome, err := filepath.EvalSymlinks(homeDir)
	if err != nil {
		return fmt.Errorf("resolving home dir: %w", err)
	}

	// Build expected resolved path using the resolved homeDir
	expectedResolved := filepath.Join(resolvedHome, ".augment", "rules")

	// Find the longest existing prefix of dir to resolve
	resolved, err := resolveExistingPrefix(dir)
	if err != nil {
		return fmt.Errorf("resolving path %s: %w", dir, err)
	}

	// The resolved existing prefix must be a prefix of expectedResolved
	// (or equal to it). This ensures no symlink diverts the path.
	if resolved != expectedResolved && !strings.HasPrefix(expectedResolved, resolved+string(filepath.Separator)) {
		return fmt.Errorf("directory %s resolves to %s (expected under %s); possible symlink in ancestor path", dir, resolved, expectedResolved)
	}
	return nil
}

// resolveExistingPrefix walks up the directory tree from path until it finds
// an existing directory, resolves symlinks on that prefix, then appends the
// remaining unresolved components. This lets us validate paths where some
// trailing components don't exist yet.
func resolveExistingPrefix(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}

	parent := filepath.Dir(path)
	if parent == path {
		return path, nil // reached root
	}

	resolvedParent, err := resolveExistingPrefix(parent)
	if err != nil {
		return "", err
	}

	return filepath.Join(resolvedParent, filepath.Base(path)), nil
}

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

	// Validate ancestor path before creating directories
	if err := validateAncestorPath(rulesDir, homeDir); err != nil {
		return err
	}

	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		return fmt.Errorf("creating Auggie rules dir: %w", err)
	}
	rulesPath := filepath.Join(rulesDir, "waggle.md")

	content, err := canonicalAuggieFile()
	if err != nil {
		return err
	}

	// Atomic write: detaches hard links, replaces leaf symlinks
	return safeWriteFile(rulesPath, []byte(content), 0644)
}

func uninstallAuggie(homeDir string) error {
	rulesPath := filepath.Join(homeDir, ".augment", "rules", "waggle.md")
	rulesDir := filepath.Dir(rulesPath)

	// Validate ancestor path
	if err := validateAncestorPath(rulesDir, homeDir); err != nil {
		return err
	}

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
