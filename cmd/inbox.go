package cmd

import (
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

var (
	inboxName string
)

func init() {
	inboxCmd.Flags().StringVar(&inboxName, "name", "", "Agent name (defaults to WAGGLE_AGENT_NAME)")
	rootCmd.AddCommand(inboxCmd)
}

var inboxCmd = &cobra.Command{
	Use:   "inbox",
	Short: "Check inbox for messages",
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

		// Check inbox
		resp, err := c.Send(protocol.Request{
			Cmd: protocol.CmdInbox,
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

