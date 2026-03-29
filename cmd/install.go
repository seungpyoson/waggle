package cmd

import (
	"fmt"
	"os"

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
		case "codex":
			if installUninstall {
				if err := install.UninstallCodex(); err != nil {
					printErr("INSTALL_ERROR", err.Error())
					return nil
				}
				printJSON(map[string]any{"ok": true, "message": "Codex integration removed"})
			} else {
				if err := install.InstallCodex(); err != nil {
					printErr("INSTALL_ERROR", err.Error())
					return nil
				}
				printJSON(map[string]any{"ok": true, "message": "Codex integration installed. Restart Codex to activate."})
			}
		case "gemini":
			if installUninstall {
				if err := install.UninstallGemini(os.ExpandEnv("$HOME")); err != nil {
					printErr("INSTALL_ERROR", err.Error())
					return nil
				}
				printJSON(map[string]any{"ok": true, "message": "Gemini integration removed"})
			} else {
				if err := install.InstallGemini(os.ExpandEnv("$HOME")); err != nil {
					printErr("INSTALL_ERROR", err.Error())
					return nil
				}
				printJSON(map[string]any{"ok": true, "message": "Gemini integration installed. Restart Gemini to activate."})
			}
		default:
			printErr("INVALID_REQUEST", fmt.Sprintf("unknown platform: %s (supported: claude-code, codex, gemini)", platform))
		}
		return nil
	},
}
