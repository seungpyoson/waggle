package cmd

import (
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

var (
	updatePriority int
	updateTags     string
)

func init() {
	taskCmd.AddCommand(taskUpdateCmd)
	taskUpdateCmd.Flags().IntVar(&updatePriority, "priority", -1, "New priority (0-100)")
	taskUpdateCmd.Flags().StringVar(&updateTags, "tags", "", "New tags (comma-separated)")
}

var taskUpdateCmd = &cobra.Command{
	Use:   "update <task_id>",
	Short: "Update a task's priority or tags",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]

		// At least one field must be specified
		if updatePriority == -1 && updateTags == "" {
			printErr("INVALID_REQUEST", "at least one of --priority or --tags must be specified")
			return nil
		}

		c, err := connectToBroker("")
		if err != nil {
			printErr("BROKER_NOT_RUNNING", err.Error())
			return nil
		}
		defer disconnectAndClose(c)

		req := protocol.Request{
			Cmd:    protocol.CmdTaskUpdate,
			TaskID: taskID,
		}

		if updatePriority != -1 {
			req.Priority = updatePriority
		}
		if updateTags != "" {
			req.Tags = updateTags
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

