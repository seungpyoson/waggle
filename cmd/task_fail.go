package cmd

import (
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

var (
	failToken string
)

func init() {
	taskFailCmd.Flags().StringVar(&failToken, "token", "", "Claim token (required)")
	taskFailCmd.MarkFlagRequired("token")
	taskCmd.AddCommand(taskFailCmd)
}

var taskFailCmd = &cobra.Command{
	Use:   "fail <task_id> <reason>",
	Short: "Fail a task",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		reason := args[1]

		c, err := connectToBroker("")
		if err != nil {
			printErr("BROKER_NOT_RUNNING", err.Error())
			return nil
		}
		defer c.Close()

		req := protocol.Request{
			Cmd:        protocol.CmdTaskFail,
			TaskID:     taskID,
			ClaimToken: failToken,
			Reason:     reason,
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

