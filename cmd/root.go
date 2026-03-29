package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"github.com/seungpyoson/waggle/internal/broker"
	"github.com/seungpyoson/waggle/internal/client"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

var (
	noAutoStart bool
	paths       config.Paths
	rootCmd     = &cobra.Command{
		Use:   "waggle",
		Short: "Agent session coordination broker",
		Long:  "Waggle coordinates work between independent AI coding agent sessions through task distribution, file locks, and event streaming.",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if isBrokerIndependentCommand(cmd) {
				return nil
			}

			projectID, err := config.ResolveProjectID()
			if err != nil {
				return err
			}

			paths = config.NewPaths(projectID)

			if paths.DataDir == "" {
				return fmt.Errorf("cannot determine data paths: HOME not set")
			}

			if noAutoStart {
				return nil
			}

			needsStart := false
			if broker.IsRunning(paths.PID) {
				if !broker.IsResponding(paths.Socket, config.Defaults.HealthCheckTimeout) {
					if pid, err := broker.ReadPID(paths.PID); err == nil {
						fmt.Fprintf(os.Stderr, "waggle: unresponsive broker (PID %d) detected, starting fresh instance\n", pid)
					} else {
						fmt.Fprintf(os.Stderr, "waggle: unresponsive broker detected, starting fresh instance\n")
					}
					if err := os.Remove(paths.Socket); err != nil && !os.IsNotExist(err) {
						return fmt.Errorf("removing stale socket: %w", err)
					}
					if err := os.Remove(paths.PID); err != nil && !os.IsNotExist(err) {
						return fmt.Errorf("removing stale PID file: %w", err)
					}
					needsStart = true
				}
			} else {
				needsStart = true
			}

			if needsStart {
				if err := autoStartBroker(); err != nil {
					return err
				}
			}

			return nil
		},
	}
)

func init() {
	rootCmd.PersistentFlags().BoolVar(&noAutoStart, "no-auto-start", false, "Don't auto-start broker")
}

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
		case "start", "install", "help", "version", "runtime", "adapter":
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
		return nil, err
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
		if isTimeoutError(err) {
			cleanupStaleFiles()
		}
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

func isTimeoutError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func cleanupStaleFiles() {
	if err := os.Remove(paths.Socket); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "waggle: warning: failed to remove stale socket: %v\n", err)
	}
	if err := os.Remove(paths.PID); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "waggle: warning: failed to remove stale PID file: %v\n", err)
	}
}

func autoStartBroker() error {
	if err := broker.CleanupStale(paths.PID, paths.Socket); err != nil {
		return fmt.Errorf("cleaning up stale files: %w", err)
	}

	socketDir := filepath.Dir(paths.Socket)
	if err := broker.EnsureDirs(paths.DataDir, socketDir); err != nil {
		return fmt.Errorf("creating directories: %w", err)
	}

	args := []string{os.Args[0], "start", "--foreground"}
	if err := broker.StartDaemon(paths.DataDir, socketDir, paths.Log, paths.ProjectID, args); err != nil {
		return fmt.Errorf("starting broker daemon: %w", err)
	}

	if err := broker.WaitForReady(paths.PID, config.Defaults.StartupTimeout, config.Defaults.StartupPollInterval); err != nil {
		return fmt.Errorf("auto-start broker: %w", err)
	}

	return nil
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
