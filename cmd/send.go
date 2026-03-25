package cmd

import (
	"fmt"
	"os"

	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

var (
	sendName string
)

func init() {
	sendCmd.Flags().StringVar(&sendName, "name", "", "Sender name (defaults to WAGGLE_AGENT_NAME)")
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
			Cmd:     protocol.CmdSend,
			Name:    recipient,
			Message: message,
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

