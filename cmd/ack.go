package cmd

import (
	"strconv"

	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

var (
	ackName string
)

func init() {
	ackCmd.Flags().StringVar(&ackName, "name", "", "Agent name (defaults to WAGGLE_AGENT_NAME)")
	rootCmd.AddCommand(ackCmd)
}

var ackCmd = &cobra.Command{
	Use:   "ack <message_id>",
	Short: "Acknowledge a message",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Parse message ID
		messageID, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			printErr("INVALID_REQUEST", "invalid message_id: must be an integer")
			return nil
		}

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

		// Send ack command
		resp, err := c.Send(protocol.Request{
			Cmd:       protocol.CmdAck,
			MessageID: messageID,
		})
		if err != nil {
			printErr("INTERNAL_ERROR", err.Error())
			return nil
		}

		if !resp.OK {
			printErr(resp.Code, resp.Error)
			return nil
		}

		printJSON(map[string]any{"ok": true})
		return nil
	},
}

