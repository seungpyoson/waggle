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

		// Self-healing hint for broker failure states. Status is read-only —
		// it reports but does not mutate. Other commands auto-recover via PersistentPreRunE.
		const brokerHint = "run any waggle command (e.g. 'waggle sessions') to auto-recover, or 'waggle stop && waggle start' to restart manually"

		c, err := client.Connect(localPaths.Socket, config.Defaults.ConnectTimeout)
		if err != nil {
			// Broker not running or not reachable — show adapter health but report error
			printJSON(map[string]any{
				"ok":       false,
				"code":     "BROKER_NOT_RUNNING",
				"error":    err.Error(),
				"hint":     brokerHint,
				"broker":   map[string]any{"running": false},
				"adapters": adapters,
			})
			os.Exit(1)
			return nil
		}
		defer c.Close()

		// Connect handshake
		if err := c.SetDeadline(config.Defaults.ConnectTimeout); err != nil {
			// Socket opened but can't set deadline — broker process exists but connection is broken
			printJSON(map[string]any{
				"ok":       false,
				"code":     "BROKER_DEGRADED",
				"error":    err.Error(),
				"hint":     brokerHint,
				"broker":   map[string]any{"running": false},
				"adapters": adapters,
			})
			os.Exit(1)
			return nil
		}
		resp, err := c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "waggle-status"})
		if err != nil || !resp.OK {
			// Socket opened but handshake failed — zombie broker or explicit rejection
			errMsg := "handshake failed"
			if err != nil {
				errMsg = err.Error()
			} else if resp.Code != "" || resp.Error != "" {
				errMsg = resp.Code + ": " + resp.Error
			}
			printJSON(map[string]any{
				"ok":       false,
				"code":     "BROKER_UNRESPONSIVE",
				"error":    errMsg,
				"hint":     brokerHint,
				"broker":   map[string]any{"running": false},
				"adapters": adapters,
			})
			os.Exit(1)
			return nil
		}
		if err := c.ClearDeadline(); err != nil {
			return err
		}

		// 4. Get broker status
		resp, err = c.Send(protocol.Request{Cmd: protocol.CmdStatus})
		if err != nil || !resp.OK {
			// Connected and handshake OK but status RPC failed — broker running but degraded
			printJSON(map[string]any{
				"ok":       true,
				"broker":   map[string]any{"running": true, "status": "degraded"},
				"hint":     brokerHint,
				"adapters": adapters,
			})
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
	result["claude-code"] = formatAdapterState(ccState, ccIssues, "waggle install claude-code")

	// Check Codex
	cxIssues, cxState := install.CheckCodex(homeDir)
	result["codex"] = formatAdapterState(cxState, cxIssues, "waggle install codex")

	return result
}

func formatAdapterState(state install.AdapterState, issues []install.HealthIssue, repairCmd string) map[string]any {
	switch state {
	case install.StateNotInstalled:
		return map[string]any{"status": "not_installed"}
	case install.StateHealthy:
		return map[string]any{"status": "healthy"}
	case install.StateBroken:
		issueList := make([]map[string]any, len(issues))
		for i, iss := range issues {
			issueList[i] = map[string]any{
				"asset":   iss.Asset,
				"problem": iss.Problem,
				"repair":  iss.Repair,
			}
		}
		return map[string]any{
			"status": "broken",
			"issues": issueList,
			"repair": repairCmd,
		}
	default:
		return map[string]any{"status": string(state)}
	}
}

