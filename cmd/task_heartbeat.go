package cmd

import (
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

var (
	heartbeatToken string
)

func init() {
	taskHeartbeatCmd.Flags().StringVar(&heartbeatToken, "token", "", "Claim token (required)")
	taskHeartbeatCmd.MarkFlagRequired("token")
	taskCmd.AddCommand(taskHeartbeatCmd)
}

var taskHeartbeatCmd = &cobra.Command{
	Use:   "heartbeat <task_id>",
	Short: "Send heartbeat for a task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]

		c, err := connectWithSession("cli")
		if err != nil {
			printErr("BROKER_NOT_RUNNING", err.Error())
			return nil
		}
		defer c.Close()

		req := protocol.Request{
			Cmd:        protocol.CmdTaskHeartbeat,
			TaskID:     taskID,
			ClaimToken: heartbeatToken,
		}

		resp, err := c.Send(req)
		if err != nil {
			printErr("INTERNAL_ERROR", err.Error())
			return nil
		}

		if !resp.OK {
			printErr(resp.Code, resp.Error)
			return nil
		}

		printJSON(resp)
		return nil
	},
}

