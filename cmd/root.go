package cmd

import (
	"encoding/json"
	"fmt"
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
	paths config.Paths
	rootCmd = &cobra.Command{
		Use:   "waggle",
		Short: "Agent session coordination broker",
		Long:  "Waggle coordinates work between independent AI coding agent sessions through task distribution, file locks, and event streaming.",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Skip auto-start for start command itself
			if cmd.Name() == "start" {
				return nil
			}

			// Detect project root and compute paths
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getting current directory: %w", err)
			}

			root, err := config.FindProjectRoot(cwd)
			if err != nil {
				return err
			}

			paths = config.NewPaths(root)

			// Check if socket path is empty (no HOME)
			if paths.Socket == "" {
				return fmt.Errorf("cannot determine socket path: HOME not set")
			}

			// Auto-start broker if not running
			if !broker.IsRunning(paths.PID) {
				// Cleanup stale files
				if err := broker.CleanupStale(paths.PID, paths.Socket); err != nil {
					return fmt.Errorf("cleaning up stale files: %w", err)
				}

				// Ensure directories exist
				socketDir := filepath.Dir(paths.Socket)
				if err := broker.EnsureDirs(paths.WaggleDir, socketDir); err != nil {
					return fmt.Errorf("creating directories: %w", err)
				}

				// Start daemon
				args := []string{os.Args[0], "start", "--foreground"}
				if err := broker.StartDaemon(paths.WaggleDir, socketDir, paths.Log, args); err != nil {
					return fmt.Errorf("starting broker daemon: %w", err)
				}

				// Wait for broker to start
				if err := broker.WaitForReady(paths.PID, config.Defaults.StartupTimeout, config.Defaults.StartupPollInterval); err != nil {
					return err
				}
			}

			return nil
		},
	}
)

// Execute runs the root command
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// printJSON marshals v to JSON and prints to stdout
func printJSON(v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		printErr("INTERNAL_ERROR", fmt.Sprintf("marshaling response: %v", err))
		return
	}
	fmt.Println(string(data))
}

// printErr prints an error response and exits with code 1
func printErr(code, message string) {
	resp := map[string]any{
		"ok":    false,
		"code":  code,
		"error": message,
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Fprintln(os.Stderr, string(data))
	os.Exit(1)
}

// connectToBroker establishes a connection with session handshake.
// If name is empty, generates a unique session name (cli-{pid}).
// This is the ONLY way to connect — every command needs a session.
func connectToBroker(name string) (*client.Client, error) {
	if name == "" {
		name = "cli-" + strconv.Itoa(os.Getpid())
	}

	c, err := client.Connect(paths.Socket)
	if err != nil {
		return nil, err
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

	return c, nil
}

