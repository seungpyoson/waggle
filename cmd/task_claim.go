package cmd

import (
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

var (
	claimType string
	claimTags string
)

func init() {
	taskClaimCmd.Flags().StringVar(&claimType, "type", "", "Filter by task type")
	taskClaimCmd.Flags().StringVar(&claimTags, "tags", "", "Filter by tags (comma-separated)")
	taskCmd.AddCommand(taskClaimCmd)
}

var taskClaimCmd = &cobra.Command{
	Use:   "claim",
	Short: "Claim the next eligible task",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := connectToBroker("")
		if err != nil {
			printErr("BROKER_NOT_RUNNING", err.Error())
			return nil
		}
		defer disconnectAndClose(c)

		req := protocol.Request{
			Cmd:  protocol.CmdTaskClaim,
			Type: claimType,
			Tags: claimTags,
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

