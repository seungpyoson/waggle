package cmd

import (
	"fmt"
	"os"

	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(sessionsCmd)
}

var sessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "List connected agent sessions",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := connectToBroker(fmt.Sprintf("_discovery-%d", os.Getpid()))
		if err != nil {
			printErr("BROKER_NOT_RUNNING", err.Error())
			return nil
		}
		defer disconnectAndClose(c)

		resp, err := c.Send(protocol.Request{Cmd: protocol.CmdPresence})
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

