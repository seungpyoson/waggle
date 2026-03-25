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

		// 3. Connect to broker FIRST — fail if broker not running
		c, err := connectToBroker("")
		if err != nil {
			printErr("BROKER_NOT_RUNNING", fmt.Sprintf("cannot spawn: broker not running (%v)", err))
			return nil
		}
		defer disconnectAndClose(c)

		// 4. Detect terminal
		term := spawn.Detect()
		if term == spawn.Unknown {
			printErr("TERMINAL_ERROR", "cannot detect terminal emulator")
			return nil
		}

		// 5. Build env
		env := map[string]string{
			"WAGGLE_AGENT_NAME": spawnName,
		}
		// Add project ID if available
		projectID, err := config.ResolveProjectID()
		if err == nil {
			env["WAGGLE_PROJECT_ID"] = projectID
		}

		// 6. Determine actual agent type for output
		agentType := spawnType
		if agentType == "" {
			agentType = agentCfg.Default
		}

		// 7. Register with broker FIRST (PID=0, will update after tab opens)
		spawnData, _ := json.Marshal(map[string]any{
			"pid":  0,
			"type": agentType,
		})
		resp, err := c.Send(protocol.Request{
			Cmd:     protocol.CmdSpawnRegister,
			Name:    spawnName,
			Payload: spawnData,
		})
		if err != nil {
			printErr("SPAWN_ERROR", fmt.Sprintf("registration failed: %v", err))
			return nil
		}
		if !resp.OK {
			printErr(resp.Code, fmt.Sprintf("registration failed: %s", resp.Error))
			return nil
		}

		// 8. Open tab (registration succeeded, name is reserved)
		pid, err := spawn.OpenTab(term, spawnName, agent.Cmd, agent.Args, env)
		if err != nil {
			// Tab failed — deregister (best effort, ignore errors)
			// We don't have a deregister command, so the entry stays with PID=0/alive=false
			// which is harmless and will be cleaned up on broker stop
			printErr("SPAWN_ERROR", err.Error())
			return nil
		}

		// 9. Update PID with broker (if we got a real PID)
		if pid > 0 {
			pidData, _ := json.Marshal(map[string]any{"pid": pid})
			c.Send(protocol.Request{
				Cmd:     protocol.CmdSpawnUpdatePID,
				Name:    spawnName,
				Payload: pidData,
			})
			// Non-fatal if this fails — agent is still registered with PID=0
		}

		// 10. Print success
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

