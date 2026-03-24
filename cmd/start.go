package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/seungpyoson/waggle/internal/broker"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/spf13/cobra"
)

var (
	foreground bool
)

func init() {
	startCmd.Flags().BoolVar(&foreground, "foreground", false, "Run broker in foreground (used by daemon)")
	rootCmd.AddCommand(startCmd)
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the broker daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
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

		if paths.Socket == "" {
			return fmt.Errorf("cannot determine socket path: HOME not set")
		}

		if foreground {
			// Run broker inline (called by daemon fork)
			socketDir := filepath.Dir(paths.Socket)
			if err := broker.EnsureDirs(paths.WaggleDir, socketDir); err != nil {
				return fmt.Errorf("creating directories: %w", err)
			}

			// Create broker
			b, err := broker.New(broker.Config{
				SocketPath: paths.Socket,
				DBPath:     paths.DB,
			})
			if err != nil {
				return fmt.Errorf("creating broker: %w", err)
			}

			// Write PID file
			if err := broker.WritePID(paths.PID); err != nil {
				return fmt.Errorf("writing PID file: %w", err)
			}

			// Serve (blocks until shutdown)
			if err := b.Serve(); err != nil {
				return fmt.Errorf("serving: %w", err)
			}

			// Cleanup PID file on exit
			broker.RemovePID(paths.PID)
			return nil
		}

		// Check if already running
		if broker.IsRunning(paths.PID) {
			pid, _ := broker.ReadPID(paths.PID)
			printJSON(map[string]any{
				"ok":      true,
				"message": fmt.Sprintf("broker already running (PID %d)", pid),
			})
			return nil
		}

		// Cleanup stale files
		if err := broker.CleanupStale(paths.PID, paths.Socket); err != nil {
			return fmt.Errorf("cleaning up stale files: %w", err)
		}

		// Start daemon
		socketDir := filepath.Dir(paths.Socket)
		daemonArgs := []string{os.Args[0], "start", "--foreground"}
		if err := broker.StartDaemon(paths.WaggleDir, socketDir, paths.Log, daemonArgs); err != nil {
			return fmt.Errorf("starting daemon: %w", err)
		}

		// Wait for broker to start
		for i := 0; i < 20; i++ {
			time.Sleep(100 * time.Millisecond)
			if broker.IsRunning(paths.PID) {
				pid, _ := broker.ReadPID(paths.PID)
				printJSON(map[string]any{
					"ok":      true,
					"message": fmt.Sprintf("broker started (PID %d)", pid),
				})
				return nil
			}
		}

		return fmt.Errorf("broker failed to start (check %s)", paths.Log)
	},
}

