package cmd

import (
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(stopCmd)
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the broker daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := connectToBroker()
		if err != nil {
			printErr("BROKER_NOT_RUNNING", err.Error())
			return nil
		}
		defer c.Close()

		resp, err := c.Send(protocol.Request{Cmd: protocol.CmdStop})
		if err != nil {
			printErr("INTERNAL_ERROR", err.Error())
			return nil
		}

		if !resp.OK {
			printErr(resp.Code, resp.Error)
			return nil
		}

		printJSON(map[string]any{
			"ok":      true,
			"message": "broker stopped",
		})
		return nil
	},
}

