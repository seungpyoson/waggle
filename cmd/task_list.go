package cmd

import (
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

var (
	listState string
	listType  string
	listOwner string
)

func init() {
	taskListCmd.Flags().StringVar(&listState, "state", "", "Filter by state")
	taskListCmd.Flags().StringVar(&listType, "type", "", "Filter by type")
	taskListCmd.Flags().StringVar(&listOwner, "owner", "", "Filter by owner")
	taskCmd.AddCommand(taskListCmd)
}

var taskListCmd = &cobra.Command{
	Use:   "list",
	Short: "List tasks",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := connectWithSession("")
		if err != nil {
			printErr("BROKER_NOT_RUNNING", err.Error())
			return nil
		}
		defer c.Close()

		req := protocol.Request{
			Cmd:   protocol.CmdTaskList,
			State: listState,
			Type:  listType,
			Owner: listOwner,
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

