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
			// Skip auto-start for commands that don't need broker
			brokerIndependent := map[string]bool{
				"start":   true,
				"install": true,
				"help":    true,
				"version": true,
			}
			if brokerIndependent[cmd.Name()] {
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

			// Auto-start broker if not running
			if !broker.IsRunning(paths.PID) {
				// Cleanup stale files
				if err := broker.CleanupStale(paths.PID, paths.Socket); err != nil {
					return fmt.Errorf("cleaning up stale files: %w", err)
				}

				// Ensure directories exist
				socketDir := filepath.Dir(paths.Socket)
				if err := broker.EnsureDirs(paths.DataDir, socketDir); err != nil {
					return fmt.Errorf("creating directories: %w", err)
				}

				// Start daemon
				args := []string{os.Args[0], "start", "--foreground"}
				if err := broker.StartDaemon(paths.DataDir, socketDir, paths.Log, projectID, args); err != nil {
					return fmt.Errorf("starting broker daemon: %w", err)
				}

				// Wait for broker to start
				if err := broker.WaitForReady(paths.PID, config.Defaults.StartupTimeout, config.Defaults.StartupPollInterval); err != nil {
					return fmt.Errorf("auto-start broker: %w", err)
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

// disconnectAndClose sends a clean disconnect command then closes the connection.
// This tells the broker to keep claimed tasks (not requeue them).
// Use this instead of raw c.Close() for all CLI commands.
// Uses a short deadline so an unresponsive broker doesn't hang the CLI.
func disconnectAndClose(c *client.Client) {
	if c == nil {
		return
	}
	// Set a 2-second deadline for the disconnect handshake.
	// If the broker is unresponsive, we close anyway — the broker will
	// treat the socket drop as an unclean disconnect and requeue tasks.
	// This is the correct fallback: better to requeue than hang.
	if err := c.SetDeadline(config.Defaults.DisconnectTimeout); err == nil {
		_, _ = c.Send(protocol.Request{Cmd: protocol.CmdDisconnect})
	}
	// If SetDeadline failed, connection is already broken — skip Send, just close.
	c.Close()
}

