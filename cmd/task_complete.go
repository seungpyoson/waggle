package cmd

import (
	"encoding/json"

	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

var (
	completeToken string
)

func init() {
	taskCompleteCmd.Flags().StringVar(&completeToken, "token", "", "Claim token (required)")
	taskCompleteCmd.MarkFlagRequired("token")
	taskCmd.AddCommand(taskCompleteCmd)
}

var taskCompleteCmd = &cobra.Command{
	Use:   "complete <task_id> <result>",
	Short: "Complete a task",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		result := args[1]

		c, err := connectToBroker("")
		if err != nil {
			printErr("BROKER_NOT_RUNNING", err.Error())
			return nil
		}
		defer disconnectAndClose(c)

		req := protocol.Request{
			Cmd:        protocol.CmdTaskComplete,
			TaskID:     taskID,
			ClaimToken: completeToken,
			Result:     json.RawMessage(result),
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

