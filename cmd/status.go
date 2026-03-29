package cmd

import (
	"os"

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
		// Always run adapter health checks (local file checks, no broker needed)
		homeDir, err := os.UserHomeDir()
		adapterStatus := map[string]any{}
		if err == nil {
			adapterStatus = buildAdapterStatus(homeDir)
		}

		// Attempt broker connection
		c, err := connectToBroker("")
		if err != nil {
			// Broker not running — show adapter health but report error
			result := map[string]any{
				"ok":      false,
				"code":    "BROKER_NOT_RUNNING",
				"error":   err.Error(),
				"broker":  map[string]any{"running": false},
				"adapters": adapterStatus,
			}
			printJSON(result)
			os.Exit(1)
			return nil
		}
		defer disconnectAndClose(c)

		resp, err := c.Send(protocol.Request{Cmd: protocol.CmdStatus})
		if err != nil {
			printErr("INTERNAL_ERROR", err.Error())
			return nil
		}

		if !resp.OK {
			printErr(resp.Code, resp.Error)
			return nil
		}

		// Merge adapter health into broker response
		result := map[string]any{
			"ok":      resp.OK,
			"broker":  resp.Data,
			"adapters": adapterStatus,
		}
		printJSON(result)
		return nil
	},
}

func buildAdapterStatus(homeDir string) map[string]any {
	result := map[string]any{}

	// Check Claude Code
	ccIssues := install.CheckClaudeCode(homeDir)
	if len(ccIssues) == 0 {
		result["claude-code"] = map[string]any{"healthy": true}
	} else {
		problems := make([]string, len(ccIssues))
		for i, iss := range ccIssues {
			problems[i] = iss.Problem
		}
		result["claude-code"] = map[string]any{
			"healthy": false,
			"issues":  problems,
			"repair":  "waggle install claude-code",
		}
	}

	// Check Codex
	cxIssues := install.CheckCodex(homeDir)
	if len(cxIssues) == 0 {
		result["codex"] = map[string]any{"healthy": true}
	} else {
		problems := make([]string, len(cxIssues))
		for i, iss := range cxIssues {
			problems[i] = iss.Problem
		}
		result["codex"] = map[string]any{
			"healthy": false,
			"issues":  problems,
			"repair":  "waggle install codex",
		}
	}

	return result
}

