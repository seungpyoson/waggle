package install

import (
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/seungpyoson/waggle/internal/fsutil"
)

//go:embed shell-hook/shell-hook.sh
var shellHookFS embed.FS

const (
	shellHookBegin = "# <!-- WAGGLE-SHELL-HOOK-BEGIN -->"
	shellHookEnd   = "# <!-- WAGGLE-SHELL-HOOK-END -->"
)

var shellHookBlock = strings.Join([]string{
	shellHookBegin,
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
	if err := safeMkdirAll(waggleDir, homeDir, 0o700); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	hookData, err := shellHookFS.ReadFile("shell-hook/shell-hook.sh")
	if err != nil {
		return fmt.Errorf("read embedded hook: %w", err)
	}
	hookPath := filepath.Join(waggleDir, "shell-hook.sh")
	if err := safeWriteFile(hookPath, hookData, 0o644, homeDir); err != nil {
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
		path := filepath.Join(homeDir, rc)
		if err := removeShellHookBlock(path, homeDir); err != nil {
			log.Printf("warning: remove shell hook block from %q: %v", path, err)
		}
	}
	if err := safeRemove(filepath.Join(homeDir, ".waggle", "shell-hook.sh"), homeDir); err != nil && !os.IsNotExist(err) {
		log.Printf("warning: remove shell hook script: %v", err)
	}
	return nil
}

func upsertShellHookBlock(path, homeDir string) error {
	// Refuse to follow symlinks — prevents write-through attacks.
	if linfo, err := os.Lstat(path); err == nil && linfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to modify symlink: %s", path)
	}
	if fsutil.HasAncestorSymlink(path, homeDir) {
		return fmt.Errorf("refusing to modify path with ancestor symlink: %s", path)
	}

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}
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
	return safeWriteFile(path, []byte(content), perm, homeDir)
}

func removeShellHookBlock(path, homeDir string) error {
	// Refuse to follow symlinks.
	if linfo, err := os.Lstat(path); err == nil && linfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to modify symlink: %s", path)
	}
	if fsutil.HasAncestorSymlink(path, homeDir) {
		return fmt.Errorf("refusing to modify path with ancestor symlink: %s", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}

	// Preserve original file permissions.
	perm := os.FileMode(0o644)
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	perm = info.Mode().Perm()

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
	// Uninstall stays best-effort: if rewriting the filtered file fails, leave the
	// user's rc file untouched and continue removing the rest of the hook artifacts.
	return safeWriteFile(path, []byte(strings.Join(filtered, "\n")), perm, homeDir)
}
