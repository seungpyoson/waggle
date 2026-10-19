package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	shellHookBegin = "# <!-- WAGGLE-SHELL-HOOK-BEGIN -->"
	shellHookEnd   = "# <!-- WAGGLE-SHELL-HOOK-END -->"
)

// Only retirement remains. There is no shell delivery installer or script.
func UninstallShellHook() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return uninstallShellHook(home)
}

func uninstallShellHook(home string) error {
	var failures []error
	for _, name := range []string{".zshenv", ".bashrc"} {
		if err := removeManagedBlock(filepath.Join(home, name), shellHookBegin, shellHookEnd, home); err != nil {
			failures = append(failures, fmt.Errorf("retire %s hook: %w", name, err))
		}
	}
	if err := safeRemove(filepath.Join(home, ".waggle", "shell-hook.sh"), home); err != nil && !os.IsNotExist(err) {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}
