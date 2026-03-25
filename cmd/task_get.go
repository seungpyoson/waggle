package cmd

import (
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

func init() {
	taskCmd.AddCommand(taskGetCmd)
}

var taskGetCmd = &cobra.Command{
	Use:   "get <task_id>",
	Short: "Get task details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]

		c, err := connectToBroker("")
		if err != nil {
			printErr("BROKER_NOT_RUNNING", err.Error())
			return nil
		}
		defer disconnectAndClose(c)

		req := protocol.Request{
			Cmd:    protocol.CmdTaskGet,
			TaskID: taskID,
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

