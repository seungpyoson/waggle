package install

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed shell-hook/shell-hook.sh
var shellHookFS embed.FS

const (
	shellHookBegin = "# <!-- WAGGLE-SHELL-HOOK-BEGIN -->"
	shellHookEnd   = "# <!-- WAGGLE-SHELL-HOOK-END -->"
)

var shellHookBlock = strings.Join([]string{
	shellHookBegin,
	`export WAGGLE_AGENT_PPID="${WAGGLE_AGENT_PPID:-$PPID}"`,
	`[ -f "$HOME/.waggle/shell-hook.sh" ] && source "$HOME/.waggle/shell-hook.sh"`,
	`export BASH_ENV="$HOME/.waggle/shell-hook.sh"`,
	shellHookEnd,
}, "\n")

// InstallShellHook installs the universal shell hook.
func InstallShellHook() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return installShellHook(home)
}

// UninstallShellHook removes the shell hook.
func UninstallShellHook() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return uninstallShellHook(home)
}

func installShellHook(homeDir string) error {
	waggleDir := filepath.Join(homeDir, ".waggle")
	if err := os.MkdirAll(waggleDir, 0o700); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	hookData, err := shellHookFS.ReadFile("shell-hook/shell-hook.sh")
	if err != nil {
		return fmt.Errorf("read embedded hook: %w", err)
	}
	hookPath := filepath.Join(waggleDir, "shell-hook.sh")
	if info, err := os.Lstat(hookPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to overwrite symlink: %s", hookPath)
	}
	if hasAncestorSymlink(hookPath, homeDir) {
		return fmt.Errorf("refusing to write through ancestor symlink: %s", hookPath)
	}
	if err := atomicWriteFile(hookPath, hookData, 0o644); err != nil {
		return fmt.Errorf("write hook: %w", err)
	}
	for _, rc := range []string{".zshenv", ".bashrc"} {
		if err := upsertShellHookBlock(filepath.Join(homeDir, rc), homeDir); err != nil {
			return fmt.Errorf("update %s: %w", rc, err)
		}
	}
	return nil
}

func uninstallShellHook(homeDir string) error {
	for _, rc := range []string{".zshenv", ".bashrc"} {
		removeShellHookBlock(filepath.Join(homeDir, rc), homeDir)
	}
	os.Remove(filepath.Join(homeDir, ".waggle", "shell-hook.sh"))
	return nil
}

func upsertShellHookBlock(path, homeDir string) error {
	// Refuse to follow symlinks — prevents write-through attacks.
	if linfo, err := os.Lstat(path); err == nil && linfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to modify symlink: %s", path)
	}
	if hasAncestorSymlink(path, homeDir) {
		return fmt.Errorf("refusing to modify path with ancestor symlink: %s", path)
	}

	existing, _ := os.ReadFile(path)
	content := string(existing)
	if strings.Contains(content, shellHookBegin) {
		return nil // already present
	}

	// Preserve original file permissions; default 0644 for new files.
	perm := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}

	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += shellHookBlock + "\n"
	return atomicWriteFile(path, []byte(content), perm)
}

func removeShellHookBlock(path, homeDir string) {
	// Refuse to follow symlinks.
	if linfo, err := os.Lstat(path); err == nil && linfo.Mode()&os.ModeSymlink != 0 {
		return
	}
	if hasAncestorSymlink(path, homeDir) {
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	// Preserve original file permissions.
	perm := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}

	lines := strings.Split(string(data), "\n")
	var filtered []string
	inBlock := false
	for _, line := range lines {
		if strings.Contains(line, "WAGGLE-SHELL-HOOK-BEGIN") {
			inBlock = true
			continue
		}
		if strings.Contains(line, "WAGGLE-SHELL-HOOK-END") {
			inBlock = false
			continue
		}
		if !inBlock {
			filtered = append(filtered, line)
		}
	}
	_ = atomicWriteFile(path, []byte(strings.Join(filtered, "\n")), perm)
}

// atomicWriteFile writes data to path atomically via temp+rename.
// Prevents TOCTOU attacks where the target is swapped between check and write.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".waggle-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp: %w", err)
	}
	tmpName = "" // success — don't remove in defer
	return nil
}

// hasAncestorSymlink checks whether any directory component between root and path
// is a symlink. Only checks components below root to avoid false positives on
// system-level symlinks (e.g., /var → /private/var on macOS).
func hasAncestorSymlink(path, root string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	// Reject paths that escape root via ".." components.
	if strings.HasPrefix(rel, "..") {
		return true
	}
	current := root
	for _, part := range strings.Split(filepath.Dir(rel), string(filepath.Separator)) {
		if part == "." {
			continue
		}
		current = filepath.Join(current, part)
		if info, err := os.Lstat(current); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}
