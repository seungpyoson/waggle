package cmd

import (
	"fmt"
	"strings"

	"github.com/seungpyoson/waggle/internal/install"
	"github.com/spf13/cobra"
)

var (
	installUninstall bool
	installDetected  = install.InstallDetected
)

func init() {
	installCmd.Flags().BoolVar(&installUninstall, "uninstall", false, "Remove integration")
	rootCmd.AddCommand(installCmd)
}

var installCmd = &cobra.Command{
	Use:           "install [platform]",
	Short:         "Install waggle integration for a platform",
	Args:          cobra.MaximumNArgs(1),
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			if installUninstall {
				printErr("INVALID_REQUEST", "install --uninstall requires a platform")
				return nil
			}
			results, err := installDetected()
			if err != nil {
				printJSON(map[string]any{
					"ok":                 false,
					"code":               "INSTALL_ERROR",
					"error":              err.Error(),
					"installed_adapters": installResultPlatforms(results),
				})
				return err
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
		if !installPlatform(platform) {
			return nil
		}
		return nil
	},
}

func installPlatform(platform string) bool {
	switch platform {
	case install.PlatformClaudeCode:
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
	case install.PlatformCodex:
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
	case install.PlatformGemini:
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
	case install.PlatformAuggie:
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
	case install.PlatformAugment:
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
		printErr("INVALID_REQUEST", fmt.Sprintf("unknown platform: %s (supported: %s)", platform, strings.Join(install.SupportedPlatforms(), ", ")))
		return false
	}
	return true
}

func installResultPlatforms(results []install.InstallResult) []string {
	platforms := make([]string, 0, len(results))
	for _, result := range results {
		platforms = append(platforms, result.Platform)
	}
	return platforms
}
