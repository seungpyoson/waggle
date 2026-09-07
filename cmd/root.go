package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/seungpyoson/waggle/internal/client"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

var (
	paths   config.Paths
	rootCmd = &cobra.Command{
		Use:   "waggle",
		Short: "Agent session coordination broker",
		Long:  "Waggle coordinates work between independent AI coding agent sessions through task distribution, file locks, and event streaming.",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if isBrokerIndependentCommand(cmd) {
				return nil
			}

			projectID, err := config.ResolveProjectID(cmd.Context())
			if err != nil {
				return err
			}

			paths = config.NewPaths(projectID)

			if paths.DataDir == "" {
				return fmt.Errorf("cannot determine data paths: HOME not set")
			}

			return nil
		},
	}
)

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func printJSON(v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		printErr("INTERNAL_ERROR", fmt.Sprintf("marshaling response: %v", err))
		return
	}
	fmt.Fprintln(rootCmd.OutOrStdout(), string(data))
}

func printErr(code, message string) {
	resp := map[string]any{
		"ok":    false,
		"code":  code,
		"error": message,
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Fprintln(rootCmd.ErrOrStderr(), string(data))
	os.Exit(1)
}

func isBrokerIndependentCommand(cmd *cobra.Command) bool {
	for current := cmd; current != nil; current = current.Parent() {
		switch current.Name() {
		case "start", "install", "uninstall", "help", "version", "status", "enroll":
			return true
		}
	}
	return false
}

func connectToBroker(name string) (*client.Client, error) {
	if name == "" {
		name = "cli-" + strconv.Itoa(os.Getpid())
	}

	c, err := client.Connect(paths.Socket, config.Defaults.ConnectTimeout)
	if err != nil {
		return nil, fmt.Errorf("broker unavailable; run waggle start: %w", err)
	}

	if err := c.SetDeadline(config.Defaults.ConnectTimeout); err != nil {
		c.Close()
		return nil, fmt.Errorf("set handshake deadline: %w", err)
	}

	resp, err := c.Send(protocol.Request{
		Cmd:  protocol.CmdConnect,
		Name: name,
	})
	if err != nil {
		c.Close()
		return nil, err
	}

	if !resp.OK {
		c.Close()
		return nil, fmt.Errorf("%s: %s", resp.Code, resp.Error)
	}

	if err := c.ClearDeadline(); err != nil {
		c.Close()
		return nil, fmt.Errorf("clear deadline: %w", err)
	}

	return c, nil
}

func disconnectAndClose(c *client.Client) {
	if c == nil {
		return
	}
	if err := c.SetDeadline(config.Defaults.DisconnectTimeout); err == nil {
		_, _ = c.Send(protocol.Request{Cmd: protocol.CmdDisconnect})
	}
	c.Close()
}
