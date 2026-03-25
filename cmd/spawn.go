package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/seungpyoson/waggle/internal/spawn"
	"github.com/spf13/cobra"
)

var (
	spawnName string
	spawnType string
)

func init() {
	spawnCmd.Flags().StringVar(&spawnName, "name", "", "Agent name (required)")
	spawnCmd.Flags().StringVar(&spawnType, "type", "", "Agent type (default: from config)")
	spawnCmd.MarkFlagRequired("name")
	rootCmd.AddCommand(spawnCmd)
}

var spawnCmd = &cobra.Command{
	Use:   "spawn",
	Short: "Launch an agent in a new terminal tab",
	RunE: func(cmd *cobra.Command, args []string) error {
		// 1. Load agent config
		home, err := os.UserHomeDir()
		if err != nil {
			printErr("CONFIG_ERROR", "failed to get home directory")
			return nil
		}
		configDir := filepath.Join(home, config.Defaults.DirName)
		agentCfg, err := spawn.LoadAgentConfig(configDir)
		if err != nil {
			printErr("CONFIG_ERROR", err.Error())
			return nil
		}

		// 2. Resolve agent type
		agent, err := agentCfg.GetAgent(spawnType)
		if err != nil {
			printErr("AGENT_ERROR", err.Error())
			return nil
		}

		// 3. Detect terminal
		term := spawn.Detect()
		if term == spawn.Unknown {
			printErr("TERMINAL_ERROR", "cannot detect terminal emulator")
			return nil
		}

		// 4. Build env
		env := map[string]string{
			"WAGGLE_AGENT_NAME": spawnName,
		}
		// Add project ID if available
		projectID, err := config.ResolveProjectID()
		if err == nil {
			env["WAGGLE_PROJECT_ID"] = projectID
		}

		// 5. Build command
		agentCmd := agent.Cmd
		if len(agent.Args) > 0 {
			for _, arg := range agent.Args {
				agentCmd += " " + arg
			}
		}

		// 6. Open tab
		pid, err := spawn.OpenTab(term, spawnName, agentCmd, env)
		if err != nil {
			printErr("SPAWN_ERROR", err.Error())
			return nil
		}

		// 7. Determine actual agent type for output
		agentType := spawnType
		if agentType == "" {
			agentType = agentCfg.Default
		}

		// 8. Register spawn with broker
		c, err := connectToBroker("")
		if err != nil {
			// Non-fatal — tab is already open, just warn
			fmt.Fprintf(os.Stderr, "warning: could not register spawn with broker: %v\n", err)
		} else {
			spawnData, _ := json.Marshal(map[string]any{
				"pid":  pid,
				"type": agentType,
			})
			resp, err := c.Send(protocol.Request{
				Cmd:  protocol.CmdSpawnRegister,
				Name: spawnName,
				Data: spawnData,
			})
			if err != nil || !resp.OK {
				fmt.Fprintf(os.Stderr, "warning: could not register spawn with broker\n")
			}
			disconnectAndClose(c)
		}

		// 9. Print success
		printJSON(map[string]any{
			"ok":      true,
			"message": fmt.Sprintf("spawned %s (%s) in new tab — PID %d", spawnName, agentType, pid),
			"name":    spawnName,
			"type":    agentType,
			"pid":     pid,
		})
		return nil
	},
}

