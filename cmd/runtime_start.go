package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/seungpyoson/waggle/internal/broker"
	"github.com/seungpyoson/waggle/internal/config"
	rt "github.com/seungpyoson/waggle/internal/runtime"
	"github.com/spf13/cobra"
)

var runtimeForeground bool

func init() {
	runtimeStartCmd.Flags().BoolVar(&runtimeForeground, "foreground", false, "Run machine runtime in foreground")
	if err := runtimeStartCmd.Flags().MarkHidden("foreground"); err != nil {
		panic(fmt.Sprintf("hide runtime foreground flag: %v", err))
	}

	runtimeCmd.AddCommand(runtimeStartCmd)
	runtimeCmd.AddCommand(runtimeStopCmd)
	runtimeCmd.AddCommand(runtimeStatusCmd)
}

var runtimeStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the local machine runtime daemon",
	RunE: func(cmd *cobra.Command, args []string) (retErr error) {
		runtimePaths, err := resolveRuntimePaths()
		if err != nil {
			return err
		}

		if runtimeForeground {
			store, err := rt.NewStore(runtimePaths.RuntimeDB)
			if err != nil {
				return err
			}
			defer store.Close()

			manager := rt.NewManager(store, rt.NewBrokerListenerFactory(), rt.NewCommandNotifier())
			manager.SetSignalDir(runtimePaths.RuntimeSignalDir)
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return rt.RunDaemon(ctx, runtimePaths, manager)
		}

		if rt.IsRunning(runtimePaths) {
			pid, _ := broker.ReadPID(runtimePaths.RuntimePID)
			printJSON(map[string]any{
				"ok":      true,
				"message": fmt.Sprintf("runtime already running (PID %d)", pid),
				"pid":     pid,
				"running": true,
			})
			return nil
		}

		if err := rt.CleanupStale(runtimePaths); err != nil {
			return err
		}

		releaseLock, err := rt.AcquireStartLock(runtimePaths, config.Defaults.RuntimeStartLockStaleThreshold)
		if err != nil {
			return err
		}
		defer func() {
			if err := releaseLock(); err != nil {
				if retErr == nil {
					retErr = fmt.Errorf("release runtime start lock: %w", err)
					return
				}
				log.Printf("warning: release runtime start lock: %v", err)
			}
		}()

		daemonArgs := []string{os.Args[0], "runtime", "start", "--foreground"}
		if err := rt.StartDaemon(runtimePaths, daemonArgs); err != nil {
			return err
		}
		if err := rt.WaitForReady(runtimePaths, config.Defaults.StartupTimeout, config.Defaults.StartupPollInterval); err != nil {
			return fmt.Errorf("runtime failed to start (check %s): %w", runtimePaths.RuntimeLog, err)
		}

		pid, err := broker.ReadPID(runtimePaths.RuntimePID)
		if err != nil {
			return fmt.Errorf("runtime started but cannot read PID: %w", err)
		}

		printJSON(map[string]any{
			"ok":      true,
			"message": fmt.Sprintf("runtime started (PID %d)", pid),
			"pid":     pid,
			"running": true,
		})
		return nil
	},
}

var runtimeStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the local machine runtime daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		runtimePaths, err := resolveRuntimePaths()
		if err != nil {
			return err
		}
		if !rt.IsRunning(runtimePaths) {
			printJSON(map[string]any{
				"ok":      true,
				"message": "runtime not running",
				"running": false,
			})
			return nil
		}

		pid, err := broker.ReadPID(runtimePaths.RuntimePID)
		if err != nil {
			return fmt.Errorf("read runtime pid: %w", err)
		}
		process, err := os.FindProcess(pid)
		if err != nil {
			return fmt.Errorf("find runtime process: %w", err)
		}
		if err := process.Signal(syscall.SIGTERM); err != nil {
			return fmt.Errorf("signal runtime process: %w", err)
		}
		deadline := time.Now().Add(config.Defaults.ShutdownTimeout)
		for rt.IsRunning(runtimePaths) {
			if time.Now().After(deadline) {
				return fmt.Errorf("runtime still running after SIGTERM")
			}
			time.Sleep(config.Defaults.ShutdownPollInterval)
		}

		printJSON(map[string]any{
			"ok":      true,
			"message": "runtime stopped",
			"running": false,
		})
		return nil
	},
}

var runtimeStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show local machine runtime status",
	RunE: func(cmd *cobra.Command, args []string) error {
		runtimePaths, err := resolveRuntimePaths()
		if err != nil {
			return err
		}

		state, err := rt.LoadState(runtimePaths)
		if err != nil {
			return err
		}
		state.Running = rt.IsRunning(runtimePaths)
		if state.Running && state.PID == 0 {
			if pid, err := broker.ReadPID(runtimePaths.RuntimePID); err == nil {
				state.PID = pid
			}
		}
		recentErrors := state.RecentErrors
		if recentErrors == nil {
			recentErrors = []rt.ErrorEntry{}
		}

		printJSON(map[string]any{
			"ok":            true,
			"pid":           state.PID,
			"running":       state.Running,
			"started_at":    state.StartedAt,
			"stopped_at":    state.StoppedAt,
			"watch_count":   state.WatchCount,
			"last_error":    state.LastError,
			"recent_errors": recentErrors,
		})
		return nil
	},
}
