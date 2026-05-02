package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/seungpyoson/waggle/internal/broker"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/install"
	rt "github.com/seungpyoson/waggle/internal/runtime"
	"github.com/spf13/cobra"
)

var (
	uninstallAll    bool
	uninstallPurge  bool
	uninstallDryRun bool

	uninstallStopRuntime = stopRuntimeForUninstall
	uninstallTargets     = []struct {
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
			return fmt.Errorf("pass --all and/or --purge")
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
		var uninstallErrs []error
		for _, item := range uninstallTargets {
			record(item.name, plannedAction(dryRun, "remove integration"))
			if !dryRun {
				if err := item.fn(); err != nil {
					uninstallErrs = append(uninstallErrs, fmt.Errorf("uninstall %s: %w", item.name, err))
				}
			}
		}
		if err := errors.Join(uninstallErrs...); err != nil {
			return actions, err
		}
	}

	if purge {
		record("runtime-daemon", plannedAction(dryRun, "stop if running"))
		if !dryRun {
			if err := uninstallStopRuntime(); err != nil {
				return actions, err
			}
		}

		waggleDir := filepath.Join(home, config.Defaults.DirName)
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

func stopRuntimeForUninstall() error {
	runtimePaths, err := resolveRuntimePaths()
	if err != nil {
		return err
	}
	if !rt.IsRunning(runtimePaths) {
		return nil
	}

	pid, err := broker.ReadPID(runtimePaths.RuntimePID)
	if err != nil {
		return fmt.Errorf("read runtime pid: %w", err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find runtime process: %w", err)
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		if isAlreadyExitedProcessError(err) {
			return nil
		}
		return fmt.Errorf("signal runtime process: %w", err)
	}

	deadline := time.Now().Add(config.Defaults.ShutdownTimeout)
	for rt.IsRunning(runtimePaths) {
		if time.Now().After(deadline) {
			return fmt.Errorf("runtime still running after SIGTERM")
		}
		time.Sleep(config.Defaults.ShutdownPollInterval)
	}
	return nil
}

func isAlreadyExitedProcessError(err error) bool {
	return errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH)
}
