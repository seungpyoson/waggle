package cmd

import (
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
		c, err := connectToBroker(connectName)
		if err != nil {
			printErr("BROKER_NOT_RUNNING", err.Error())
			return nil
		}
		defer disconnectAndClose(c)

		printJSON(map[string]any{"ok": true, "data": map[string]string{"name": connectName}})
		return nil
	},
}

