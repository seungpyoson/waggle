package cmd

import (
	"encoding/json"
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
	Use:   "install [platform]",
	Short: "Install waggle integration for a platform",
	Args:  installArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			if installUninstall {
				printErr("INVALID_REQUEST", "install --uninstall requires a platform")
				return nil
			}
			results, err := installDetected()
			if err != nil {
				printInstallError(cmd, "INSTALL_ERROR", err.Error(), map[string]any{
					"ok":                 false,
					"installed_adapters": installResultPlatforms(results),
				})
				cmd.SilenceErrors = true
				cmd.SilenceUsage = true
				return err
			}
			if len(results) == 0 {
				printJSON(map[string]any{"ok": true, "message": "no supported adapters detected"})
				return nil
			}
			printJSON(map[string]any{
				"ok":                 true,
				"message":            "detected adapters installed",
				"installed_adapters": installResultPlatforms(results),
				"results":            results,
			})
			return nil
		}

		platform := args[0]
		if !installPlatform(platform) {
			return nil
		}
		return nil
	},
}

func installArgs(cmd *cobra.Command, args []string) error {
	if len(args) <= 1 {
		return nil
	}
	err := fmt.Errorf("accepts at most 1 arg(s), received %d", len(args))
	printInstallError(cmd, "INVALID_REQUEST", err.Error(), nil)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	return err
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

func printInstallError(cmd *cobra.Command, code, message string, fields map[string]any) {
	resp := map[string]any{
		"ok":    false,
		"code":  code,
		"error": message,
	}
	for key, value := range fields {
		resp[key] = value
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Fprintln(cmd.ErrOrStderr(), string(data))
}
