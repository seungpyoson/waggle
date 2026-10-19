package cmd

import (
	"errors"
	"fmt"

	"github.com/seungpyoson/waggle/internal/install"
	"github.com/spf13/cobra"
)

var (
	uninstallAll    bool
	uninstallDryRun bool

	uninstallTargets = []struct {
		name string
		fn   func() error
	}{
		{"claude-code", install.UninstallClaudeCode},
		{"codex", install.UninstallCodex},
		{"gemini", install.UninstallGemini},
		{"auggie", install.UninstallAuggie},
		{"augment", install.UninstallAugment},
		{"shell-hook", install.UninstallShellHook},
	}
)

func init() {
	uninstallCmd.Flags().BoolVar(&uninstallAll, "all", false, "Remove all supported integrations")
	uninstallCmd.Flags().BoolVar(&uninstallDryRun, "dry-run", false, "Report planned removals without changing files")
	rootCmd.AddCommand(uninstallCmd)
}

var uninstallCmd = &cobra.Command{
	Use:           "uninstall",
	Short:         "Remove waggle integrations, preserving local state",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) != 0 {
			return fmt.Errorf("uninstall accepts flags only")
		}
		if !uninstallAll {
			printErr("INVALID_REQUEST", "pass --all to remove integrations")
			return fmt.Errorf("pass --all to remove integrations")
		}
		actions, err := runUninstall(uninstallDryRun)
		result := map[string]any{
			"ok":      err == nil,
			"dry_run": uninstallDryRun,
			"actions": actions,
		}
		if err != nil {
			result["code"] = "UNINSTALL_ERROR"
			result["error"] = err.Error()
			printJSON(result)
			return err
		}
		printJSON(result)
		return nil
	},
}

func runUninstall(dryRun bool) ([]map[string]any, error) {
	var actions []map[string]any
	var uninstallErrs []error
	for _, item := range uninstallTargets {
		actions = append(actions, map[string]any{"target": item.name, "action": plannedAction(dryRun, "remove integration")})
		if !dryRun {
			if err := item.fn(); err != nil {
				uninstallErrs = append(uninstallErrs, fmt.Errorf("uninstall %s: %w", item.name, err))
			}
		}
	}
	return actions, errors.Join(uninstallErrs...)
}

func plannedAction(dryRun bool, action string) string {
	if dryRun {
		return "would " + action
	}
	return action
}
