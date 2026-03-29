package cmd

import (
	"os"

	"github.com/seungpyoson/waggle/internal/client"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/install"
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check broker and adapter status",
	RunE: func(cmd *cobra.Command, args []string) error {
		// 1. Always run adapter health (file-based, no broker needed)
		homeDir, _ := os.UserHomeDir()
		adapters := map[string]any{}
		if homeDir != "" {
			adapters = buildAdapterStatus(homeDir)
		}

		// 2. Resolve paths locally (cannot rely on package-level paths var
		//    because status is broker-independent — PersistentPreRunE skips path setup)
		projectID, err := config.ResolveProjectID()
		if err != nil {
			// Cannot determine project ID — no git repo, WAGGLE_PROJECT_ID, or WAGGLE_ROOT
			printJSON(map[string]any{
				"ok":      false,
				"code":    "NO_PROJECT_CONTEXT",
				"error":   err.Error(),
				"broker":  map[string]any{"running": false, "reason": "no project context"},
				"adapters": adapters,
			})
			os.Exit(1)
			return nil
		}

		localPaths := config.NewPaths(projectID)
		if localPaths.Socket == "" {
			// Cannot determine socket path — HOME not set
			printJSON(map[string]any{
				"ok":      false,
				"code":    "NO_HOME",
				"error":   "cannot determine socket path: HOME not set",
				"broker":  map[string]any{"running": false, "reason": "HOME not set"},
				"adapters": adapters,
			})
			os.Exit(1)
			return nil
		}

		c, err := client.Connect(localPaths.Socket, config.Defaults.ConnectTimeout)
		if err != nil {
			// Broker not running or not reachable — show adapter health but report error
			printJSON(map[string]any{
				"ok":       false,
				"code":     "BROKER_NOT_RUNNING",
				"error":    err.Error(),
				"broker":   map[string]any{"running": false},
				"adapters": adapters,
			})
			os.Exit(1)
			return nil
		}
		defer c.Close()

		// Connect handshake
		if err := c.SetDeadline(config.Defaults.ConnectTimeout); err != nil {
			printJSON(map[string]any{"ok": true, "broker": map[string]any{"running": false}, "adapters": adapters})
			return nil
		}
		resp, err := c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "waggle-status"})
		if err != nil || !resp.OK {
			printJSON(map[string]any{"ok": true, "broker": map[string]any{"running": false}, "adapters": adapters})
			return nil
		}
		if err := c.ClearDeadline(); err != nil {
			return err
		}

		// 4. Get broker status
		resp, err = c.Send(protocol.Request{Cmd: protocol.CmdStatus})
		if err != nil || !resp.OK {
			printJSON(map[string]any{"ok": true, "broker": map[string]any{"running": true}, "adapters": adapters})
			return nil
		}

		// Disconnect cleanly
		if err := c.SetDeadline(config.Defaults.DisconnectTimeout); err == nil {
			_, _ = c.Send(protocol.Request{Cmd: protocol.CmdDisconnect})
		}

		printJSON(map[string]any{
			"ok":      resp.OK,
			"broker":  resp.Data,
			"adapters": adapters,
		})
		return nil
	},
}

func buildAdapterStatus(homeDir string) map[string]any {
	result := map[string]any{}

	// Check Claude Code
	ccIssues, ccState := install.CheckClaudeCode(homeDir)
	switch ccState {
	case install.StateNotInstalled:
		result["claude-code"] = map[string]any{"status": "not_installed"}
	case install.StateHealthy:
		result["claude-code"] = map[string]any{"status": "healthy"}
	case install.StateBroken:
		problems := make([]string, len(ccIssues))
		for i, iss := range ccIssues {
			problems[i] = iss.Problem
		}
		result["claude-code"] = map[string]any{
			"status": "broken",
			"issues": problems,
			"repair": "waggle install claude-code",
		}
	}

	// Check Codex
	cxIssues, cxState := install.CheckCodex(homeDir)
	switch cxState {
	case install.StateNotInstalled:
		result["codex"] = map[string]any{"status": "not_installed"}
	case install.StateHealthy:
		result["codex"] = map[string]any{"status": "healthy"}
	case install.StateBroken:
		problems := make([]string, len(cxIssues))
		for i, iss := range cxIssues {
			problems[i] = iss.Problem
		}
		result["codex"] = map[string]any{
			"status": "broken",
			"issues": problems,
			"repair": "waggle install codex",
		}
	}

	return result
}

