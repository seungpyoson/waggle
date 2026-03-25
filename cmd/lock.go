package cmd

import (
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(lockCmd)
	rootCmd.AddCommand(unlockCmd)
	rootCmd.AddCommand(locksCmd)
}

var lockCmd = &cobra.Command{
	Use:   "lock <resource>",
	Short: "Acquire a lock on a resource",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resource := args[0]

		c, err := connectToBroker("")
		if err != nil {
			printErr("BROKER_NOT_RUNNING", err.Error())
			return nil
		}
		defer disconnectAndClose(c)

		req := protocol.Request{
			Cmd:      protocol.CmdLock,
			Resource: resource,
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

var unlockCmd = &cobra.Command{
	Use:   "unlock <resource>",
	Short: "Release a lock on a resource",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resource := args[0]

		c, err := connectToBroker("")
		if err != nil {
			printErr("BROKER_NOT_RUNNING", err.Error())
			return nil
		}
		defer disconnectAndClose(c)

		req := protocol.Request{
			Cmd:      protocol.CmdUnlock,
			Resource: resource,
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

var locksCmd = &cobra.Command{
	Use:   "locks",
	Short: "List all locks",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := connectToBroker("")
		if err != nil {
			printErr("BROKER_NOT_RUNNING", err.Error())
			return nil
		}
		defer disconnectAndClose(c)

		req := protocol.Request{
			Cmd: protocol.CmdLocks,
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

