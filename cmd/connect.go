package cmd

import (
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

var (
	connectName string
)

func init() {
	connectCmd.Flags().StringVar(&connectName, "name", "", "Session name (required)")
	connectCmd.MarkFlagRequired("name")
	rootCmd.AddCommand(connectCmd)
}

var connectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Connect to the broker",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := connectToBroker()
		if err != nil {
			printErr("BROKER_NOT_RUNNING", err.Error())
			return nil
		}
		defer c.Close()

		resp, err := c.Send(protocol.Request{
			Cmd:  protocol.CmdConnect,
			Name: connectName,
		})
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

