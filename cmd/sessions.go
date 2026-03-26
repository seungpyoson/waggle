package cmd

import (
	"encoding/json"
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

		// Filter out ephemeral sessions (names starting with _)
		var agents []map[string]string
		if err := json.Unmarshal(resp.Data, &agents); err == nil {
			filtered := make([]map[string]string, 0, len(agents))
			for _, a := range agents {
				if name, ok := a["name"]; ok && len(name) > 0 && name[0] != '_' {
					filtered = append(filtered, a)
				}
			}
			resp.Data, _ = json.Marshal(filtered)
		}

		printJSON(resp)
		return nil
	},
}

