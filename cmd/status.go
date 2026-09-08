package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

var statusCmd = newStatusCommand(func(ctx context.Context, socket string) (json.RawMessage, error) {
	return client.Query(ctx, socket, config.Defaults.ConnectTimeout, protocol.Request{Cmd: protocol.CmdStatus})
})

// The request dependency permits deterministic transport faults through the
// same command and result rendering used by the CLI.
func newStatusCommand(request func(context.Context, string) (json.RawMessage, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check broker and adapter status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, _ := os.UserHomeDir()
			adapters := map[string]any{}
			if home != "" {
				adapters = buildAdapterStatus(home)
			}
			report := func(err error) error {
				output := map[string]any{"ok": false, "code": "BROKER_STATUS_UNCONFIRMED", "error": err.Error(),
					"broker": map[string]any{"status": "unknown"}, "adapters": adapters}
				return errors.Join(err, json.NewEncoder(cmd.OutOrStdout()).Encode(output))
			}
			projectID, err := config.ResolveProjectID(cmd.Context())
			if err != nil {
				return report(err)
			}
			localPaths := config.NewPaths(projectID)
			if localPaths.Socket == "" {
				return report(fmt.Errorf("cannot determine socket path: HOME not set"))
			}
			data, err := request(cmd.Context(), localPaths.Socket)
			if err != nil {
				return report(err)
			}

			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"ok": true, "broker": data, "adapters": adapters})
		},
	}
}

func buildAdapterStatus(homeDir string) map[string]any {
	result := map[string]any{}

	// Check Claude Code
	ccIssues, ccState := install.CheckClaudeCode(homeDir)
	result["claude-code"] = formatAdapterState(ccState, ccIssues, "waggle install claude-code")

	// Check Codex
	cxIssues, cxState := install.CheckCodex(homeDir)
	result["codex"] = formatAdapterState(cxState, cxIssues, "waggle install codex")

	// Check Gemini
	gmIssues, gmState := install.CheckGemini(homeDir)
	result["gemini"] = formatAdapterState(gmState, gmIssues, "waggle install gemini")

	// Check Auggie
	agIssues, agState := install.CheckAuggie(homeDir)
	result["auggie"] = formatAdapterState(agState, agIssues, "waggle install auggie")

	// Check Augment
	augIssues, augState := install.CheckAugment(homeDir)
	result["augment"] = formatAdapterState(augState, augIssues, "waggle install augment")

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
