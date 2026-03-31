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
	if err := os.WriteFile(filepath.Join(waggleDir, "shell-hook.sh"), hookData, 0o644); err != nil {
		return fmt.Errorf("write hook: %w", err)
	}
	for _, rc := range []string{".zshenv", ".bashrc"} {
		if err := upsertShellHookBlock(filepath.Join(homeDir, rc)); err != nil {
			return fmt.Errorf("update %s: %w", rc, err)
		}
	}
	return nil
}

func uninstallShellHook(homeDir string) error {
	for _, rc := range []string{".zshenv", ".bashrc"} {
		removeShellHookBlock(filepath.Join(homeDir, rc))
	}
	os.Remove(filepath.Join(homeDir, ".waggle", "shell-hook.sh"))
	return nil
}

func upsertShellHookBlock(path string) error {
	existing, _ := os.ReadFile(path)
	content := string(existing)
	if strings.Contains(content, shellHookBegin) {
		return nil // already present
	}
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += shellHookBlock + "\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

func removeShellHookBlock(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
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
	os.WriteFile(path, []byte(strings.Join(filtered, "\n")), 0o644)
}
