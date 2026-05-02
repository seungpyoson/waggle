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
	Use:   "install [platform]",
	Short: "Install waggle integration for a platform",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			if installUninstall {
				printErr("INVALID_REQUEST", "install --uninstall requires a platform")
				return nil
			}
			results, err := install.InstallDetected()
			if err != nil {
				printErr("INSTALL_ERROR", err.Error())
				return nil
			}
			if len(results) == 0 {
				printJSON(map[string]any{"ok": true, "message": "no supported adapters detected"})
				return nil
			}
			for _, result := range results {
				printJSON(map[string]any{"ok": true, "message": result.Message, "platform": result.Platform})
			}
			return nil
		}

		platform := args[0]
		installPlatform(platform)
		return nil
	},
}

func installPlatform(platform string) bool {
	switch platform {
	case "claude-code":
		if installUninstall {
			if err := install.UninstallClaudeCode(); err != nil {
				printErr("INSTALL_ERROR", err.Error())
				return false
			}
			printJSON(map[string]any{"ok": true, "message": "Claude Code integration removed"})
		} else {
			if err := install.InstallClaudeCode(); err != nil {
				printErr("INSTALL_ERROR", err.Error())
				return false
			}
			printJSON(map[string]any{"ok": true, "message": "Claude Code integration installed. Restart Claude Code to activate."})
		}
	case "codex":
		if installUninstall {
			if err := install.UninstallCodex(); err != nil {
				printErr("INSTALL_ERROR", err.Error())
				return false
			}
			printJSON(map[string]any{"ok": true, "message": "Codex integration removed"})
		} else {
			if err := install.InstallCodex(); err != nil {
				printErr("INSTALL_ERROR", err.Error())
				return false
			}
			printJSON(map[string]any{"ok": true, "message": "Codex integration installed. Restart Codex to activate."})
		}
	case "gemini":
		if installUninstall {
			if err := install.UninstallGemini(); err != nil {
				printErr("INSTALL_ERROR", err.Error())
				return false
			}
			printJSON(map[string]any{"ok": true, "message": "Gemini integration removed"})
		} else {
			if err := install.InstallGemini(); err != nil {
				printErr("INSTALL_ERROR", err.Error())
				return false
			}
			printJSON(map[string]any{"ok": true, "message": "Gemini integration installed. Restart Gemini to activate."})
		}
	case "auggie":
		if installUninstall {
			if err := install.UninstallAuggie(); err != nil {
				printErr("INSTALL_ERROR", err.Error())
				return false
			}
			printJSON(map[string]any{"ok": true, "message": "Auggie integration removed"})
		} else {
			if err := install.InstallAuggie(); err != nil {
				printErr("INSTALL_ERROR", err.Error())
				return false
			}
			printJSON(map[string]any{"ok": true, "message": "Auggie integration installed. Restart Auggie to activate."})
		}
	case "augment":
		if installUninstall {
			if err := install.UninstallAugment(); err != nil {
				printErr("INSTALL_ERROR", err.Error())
				return false
			}
			printJSON(map[string]any{"ok": true, "message": "Augment integration removed"})
		} else {
			if err := install.InstallAugment(); err != nil {
				printErr("INSTALL_ERROR", err.Error())
				return false
			}
			printJSON(map[string]any{"ok": true, "message": "Augment integration installed. Restart Augment to activate."})
		}
	default:
		printErr("INVALID_REQUEST", fmt.Sprintf("unknown platform: %s (supported: claude-code, codex, gemini, auggie, augment)", platform))
		return false
	}
	return true
}
