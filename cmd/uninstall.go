package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/seungpyoson/waggle/internal/install"
	"github.com/spf13/cobra"
)

var (
	uninstallAll    bool
	uninstallPurge  bool
	uninstallDryRun bool
)

func init() {
	uninstallCmd.Flags().BoolVar(&uninstallAll, "all", false, "Remove all supported integrations")
	uninstallCmd.Flags().BoolVar(&uninstallPurge, "purge", false, "Remove Waggle runtime and broker state")
	uninstallCmd.Flags().BoolVar(&uninstallDryRun, "dry-run", false, "Report planned removals without changing files")
	rootCmd.AddCommand(uninstallCmd)
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove waggle integrations and optionally purge local state",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) != 0 {
			return fmt.Errorf("uninstall accepts flags only")
		}
		if !uninstallAll && !uninstallPurge {
			printErr("INVALID_REQUEST", "pass --all and/or --purge")
			return nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("getting home dir: %w", err)
		}
		actions, err := runUninstall(home, uninstallAll, uninstallPurge, uninstallDryRun)
		if err != nil {
			return err
		}
		printJSON(map[string]any{
			"ok":      true,
			"dry_run": uninstallDryRun,
			"actions": actions,
		})
		return nil
	},
}

func runUninstall(home string, all, purge, dryRun bool) ([]map[string]any, error) {
	var actions []map[string]any
	record := func(target, action string) {
		actions = append(actions, map[string]any{"target": target, "action": action})
	}

	if all {
		for _, item := range []struct {
			name string
			fn   func() error
		}{
			{"claude-code", install.UninstallClaudeCode},
			{"codex", install.UninstallCodex},
			{"gemini", install.UninstallGemini},
			{"auggie", install.UninstallAuggie},
			{"augment", install.UninstallAugment},
			{"shell-hook", install.UninstallShellHook},
		} {
			record(item.name, plannedAction(dryRun, "remove integration"))
			if !dryRun {
				if err := item.fn(); err != nil {
					return actions, fmt.Errorf("uninstall %s: %w", item.name, err)
				}
			}
		}
	}

	if purge {
		waggleDir := filepath.Join(home, ".waggle")
		record(waggleDir, plannedAction(dryRun, "remove state"))
		if !dryRun {
			if err := removeOwnedTree(waggleDir, home); err != nil {
				return actions, err
			}
		}
	}

	return actions, nil
}

func plannedAction(dryRun bool, action string) string {
	if dryRun {
		return "would " + action
	}
	return action
}

func removeOwnedTree(path, root string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || rel == "" || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("refusing to remove path outside home: %s", path)
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to remove symlink: %s", path)
		}
	} else if os.IsNotExist(err) {
		return nil
	} else {
		return fmt.Errorf("lstat %s: %w", path, err)
	}
	return os.RemoveAll(path)
}
