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
	noAutoStart bool
	paths       config.Paths
	rootCmd     = &cobra.Command{
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

			if noAutoStart {
				// Still resolve paths (commands need them), but don't start broker
				// If broker isn't running, commands that need it will fail on connect
				return nil
			}

			needsStart := false
			if broker.IsRunning(paths.PID) {
				if !broker.IsResponding(paths.Socket, config.Defaults.HealthCheckTimeout) {
					// Zombie: process exists but not responding — warn, clean up, restart
					pid, _ := broker.ReadPID(paths.PID)
					fmt.Fprintf(os.Stderr, "waggle: unresponsive broker (PID %d) detected, starting fresh instance\n", pid)
					os.Remove(paths.Socket)
					os.Remove(paths.PID)
					needsStart = true
				}
				// else: healthy, skip auto-start
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

	c, err := client.Connect(paths.Socket, config.Defaults.ConnectTimeout)
	if err != nil {
		return nil, err
	}

	// Deadline for handshake only — cleared after success
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
		// On timeout, clean up stale files so retry/next invocation can auto-start
		cleanupStaleFiles()
		return nil, err
	}

	if !resp.OK {
		c.Close()
		return nil, fmt.Errorf("%s: %s", resp.Code, resp.Error)
	}

	// Clear deadline — streaming commands need to read indefinitely
	if err := c.ClearDeadline(); err != nil {
		c.Close()
		return nil, fmt.Errorf("clear deadline: %w", err)
	}

	return c, nil
}

// cleanupStaleFiles removes socket and PID files when a connect timeout
// suggests the broker is zombie. Best-effort, errors ignored.
func cleanupStaleFiles() {
	os.Remove(paths.Socket)
	os.Remove(paths.PID)
}

// autoStartBroker cleans up stale files, ensures directories exist, starts the
// broker daemon, and waits for it to become ready.
func autoStartBroker() error {
	// Cleanup stale files (idempotent — ignore errors, files may already be gone)
	broker.CleanupStale(paths.PID, paths.Socket)

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

