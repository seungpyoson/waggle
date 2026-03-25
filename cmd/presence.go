package cmd

import (
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

var (
	presenceName string
)

func init() {
	presenceCmd.Flags().StringVar(&presenceName, "name", "", "Agent name (defaults to WAGGLE_AGENT_NAME)")
	rootCmd.AddCommand(presenceCmd)
}

var presenceCmd = &cobra.Command{
	Use:   "presence",
	Short: "List connected agents",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Resolve agent name
		agentName, err := resolveAgentName(cmd)
		if err != nil {
			printErr("INVALID_REQUEST", err.Error())
			return nil
		}

		// Connect as agent
		c, err := connectToBroker(agentName)
		if err != nil {
			printErr("BROKER_NOT_RUNNING", err.Error())
			return nil
		}
		defer disconnectAndClose(c)

		// Send presence command
		resp, err := c.Send(protocol.Request{
			Cmd: protocol.CmdPresence,
		})
		if err != nil {
			printErr("INTERNAL_ERROR", err.Error())
			return nil
		}

		if !resp.OK {
			printErr(resp.Code, resp.Error)
			return nil
		}

		printJSON(map[string]any{"ok": true, "data": resp.Data})
		return nil
	},
}

