package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/seungpyoson/waggle/internal/client"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/seungpyoson/waggle/internal/spawn"
	"github.com/spf13/cobra"
)

var (
	spawnName     string
	spawnType     string
	spawnTerminal string
)

func init() {
	spawnCmd.Flags().StringVar(&spawnName, "name", "", "Session label (required; identity is assigned by enrollment)")
	spawnCmd.Flags().StringVar(&spawnType, "type", "", "Agent type (default: from config)")
	spawnCmd.Flags().StringVar(&spawnTerminal, "terminal", "", "Terminal to launch (default: from agent config)")
	spawnCmd.MarkFlagRequired("name")
	rootCmd.AddCommand(spawnCmd)
}

var spawnCmd = &cobra.Command{
	Use:   "spawn",
	Short: "Request an agent launch in a new terminal tab",
	Long:  "Request an agent launch in a new terminal tab. Native enrollment establishes session identity and readiness. Launching does not reserve a name or confirm that the agent is running.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(spawnName) == "" || len(spawnName) > config.Defaults.MaxFieldLength {
			return fmt.Errorf("session label outside configured bounds")
		}
		// Require the independently started broker without creating a session
		// registration or any launch record.
		if _, err := client.Query(cmd.Context(), paths.Socket, config.Defaults.ConnectTimeout, protocol.Request{Cmd: protocol.CmdStatus}); err != nil {
			return fmt.Errorf("broker unavailable; run waggle start: %w", err)
		}

		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve agent config home: %w", err)
		}
		agentCfg, err := config.LoadAgentConfig(filepath.Join(home, config.Defaults.DirName))
		if err != nil {
			return err
		}
		agentType, agent, err := agentCfg.GetAgent(spawnType)
		if err != nil {
			return err
		}
		terminal := agentCfg.Terminal
		if cmd.Flags().Changed("terminal") {
			terminal = spawnTerminal
		}
		launcher, err := spawn.ResolveTerminal(terminal)
		if err != nil {
			return err
		}

		env := map[string]string{
			"WAGGLE_AGENT_NAME": spawnName,
			"WAGGLE_PROJECT_ID": paths.ProjectID,
		}
		if err := launcher.OpenTab(cmd.Context(), agent.Cmd, agent.Args, env); err != nil {
			return err
		}
		printJSON(map[string]any{
			"ok":    true,
			"state": "launch_requested",
			"name":  spawnName,
			"type":  agentType,
		})
		return nil
	},
}
