package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

func awaitEndpointRemoval(ctx context.Context, pidPath string, poll, deadline time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for {
		_, err := os.Lstat(pidPath)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect broker PID file: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
		case <-tick.C:
		}
	}
}

func init() {
	rootCmd.AddCommand(stopCmd)
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the broker daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := connectToBroker("")
		if err != nil {
			printErr("BROKER_NOT_RUNNING", err.Error())
			return nil
		}
		defer disconnectAndClose(c)

		resp, err := c.Send(protocol.Request{Cmd: protocol.CmdStop})
		if err != nil {
			printErr("INTERNAL_ERROR", err.Error())
			return nil
		}

		if !resp.OK {
			printErr(resp.Code, resp.Error)
			return nil
		}
		if err := awaitEndpointRemoval(cmd.Context(), paths.PID, config.Defaults.ShutdownPollInterval, config.Defaults.ShutdownTimeout); err != nil {
			printErr("SHUTDOWN_INCOMPLETE", "stop requested; broker still draining (ownership retained)")
			return nil
		}

		printJSON(map[string]any{
			"ok":      true,
			"message": "broker stopped",
		})
		return nil
	},
}
