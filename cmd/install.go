package cmd

import (
	"fmt"

	"github.com/seungpyoson/waggle/internal/install"
	"github.com/spf13/cobra"
)

var installUninstall bool

func init() {
	installCmd.Flags().BoolVar(&installUninstall, "uninstall", false, "Remove integration")
	rootCmd.AddCommand(installCmd)
}

var installCmd = &cobra.Command{
	Use:   "install <platform>",
	Short: "Install waggle integration for a platform",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		platform := args[0]
		switch platform {
		case "claude-code":
			if installUninstall {
				if err := install.UninstallClaudeCode(); err != nil {
					printErr("INSTALL_ERROR", err.Error())
					return nil
				}
				printJSON(map[string]any{"ok": true, "message": "Claude Code integration removed"})
			} else {
				if err := install.InstallClaudeCode(); err != nil {
					printErr("INSTALL_ERROR", err.Error())
					return nil
				}
				printJSON(map[string]any{"ok": true, "message": "Claude Code integration installed. Restart Claude Code to activate."})
			}
		default:
			printErr("INVALID_REQUEST", fmt.Sprintf("unknown platform: %s (supported: claude-code)", platform))
		}
		return nil
	},
}

