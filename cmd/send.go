package cmd

import (
	"fmt"
	"os"

	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

var (
	sendName      string
	sendPriority  string
	sendTTL       int
	sendAwaitAck  bool
	sendTimeout   int
)

func init() {
	sendCmd.Flags().StringVar(&sendName, "name", "", "Sender name (defaults to WAGGLE_AGENT_NAME)")
	sendCmd.Flags().StringVar(&sendPriority, "priority", "", "Message priority: critical, normal, bulk")
	sendCmd.Flags().IntVar(&sendTTL, "ttl", 0, "Message TTL in seconds (0 = no expiry)")
	sendCmd.Flags().BoolVar(&sendAwaitAck, "await-ack", false, "Block until receiver acks the message")
	sendCmd.Flags().IntVar(&sendTimeout, "timeout", 0, "Timeout in seconds for --await-ack (default: 30)")
	rootCmd.AddCommand(sendCmd)
}

var sendCmd = &cobra.Command{
	Use:   "send <recipient> <message>",
	Short: "Send a message to another agent",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		recipient := args[0]
		message := args[1]

		// Resolve sender name
		senderName, err := resolveAgentName(cmd)
		if err != nil {
			printErr("INVALID_REQUEST", err.Error())
			return nil
		}

		// Connect as sender
		c, err := connectToBroker(senderName)
		if err != nil {
			printErr("BROKER_NOT_RUNNING", err.Error())
			return nil
		}
		defer disconnectAndClose(c)

		// Send message
		resp, err := c.Send(protocol.Request{
			Cmd:         protocol.CmdSend,
			Name:        recipient,
			Message:     message,
			MsgPriority: sendPriority,
			TTL:         sendTTL,
			AwaitAck:    sendAwaitAck,
			Timeout:     sendTimeout,
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

// resolveAgentName resolves the agent name from --name flag or WAGGLE_AGENT_NAME env var
func resolveAgentName(cmd *cobra.Command) (string, error) {
	name, _ := cmd.Flags().GetString("name")
	if name == "" {
		name = os.Getenv("WAGGLE_AGENT_NAME")
	}
	if name == "" {
		return "", fmt.Errorf("agent name required: set WAGGLE_AGENT_NAME or use --name")
	}
	return name, nil
}

