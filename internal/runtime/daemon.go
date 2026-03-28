package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/seungpyoson/waggle/internal/broker"
	"github.com/seungpyoson/waggle/internal/config"
)

type State struct {
	PID        int       `json:"pid,omitempty"`
	Running    bool      `json:"running"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	StoppedAt  time.Time `json:"stopped_at,omitempty"`
	WatchCount int       `json:"watch_count,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
}

func LoadState(paths config.Paths) (State, error) {
	if paths.RuntimeState == "" {
		return State{}, fmt.Errorf("runtime state path required")
	}

	data, err := os.ReadFile(paths.RuntimeState)
	if err != nil {
		if os.IsNotExist(err) {
			return State{}, nil
		}
		return State{}, fmt.Errorf("read runtime state: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("decode runtime state: %w", err)
	}
	return state, nil
}

func SaveState(paths config.Paths, state State) error {
	if paths.RuntimeDir == "" || paths.RuntimeState == "" {
		return fmt.Errorf("runtime paths required")
	}
	if err := os.MkdirAll(paths.RuntimeDir, 0o755); err != nil {
		return fmt.Errorf("create runtime directory: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime state: %w", err)
	}
	if err := os.WriteFile(paths.RuntimeState, data, 0o644); err != nil {
		return fmt.Errorf("write runtime state: %w", err)
	}
	return nil
}

func StartDaemon(paths config.Paths, args []string) error {
	if paths.RuntimeDir == "" || paths.RuntimeLog == "" {
		return fmt.Errorf("runtime paths required")
	}
	if err := os.MkdirAll(paths.RuntimeDir, 0o755); err != nil {
		return fmt.Errorf("create runtime directory: %w", err)
	}

	logFile, err := os.OpenFile(paths.RuntimeLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open runtime log: %w", err)
	}
	defer logFile.Close()

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}

	procAttr := &os.ProcAttr{
		Files: []*os.File{nil, logFile, logFile},
		Env:   os.Environ(),
	}

	process, err := os.StartProcess(exe, args, procAttr)
	if err != nil {
		return fmt.Errorf("start runtime daemon: %w", err)
	}
	if err := process.Release(); err != nil {
		return fmt.Errorf("release runtime daemon: %w", err)
	}
	return nil
}

func IsRunning(paths config.Paths) bool {
	return broker.IsRunning(paths.RuntimePID)
}

func WaitForReady(paths config.Paths, timeout, interval time.Duration) error {
	return broker.WaitForReady(paths.RuntimePID, timeout, interval)
}

func CleanupStale(paths config.Paths) error {
	if IsRunning(paths) {
		return fmt.Errorf("runtime already running")
	}
	if _, err := os.Stat(paths.RuntimePID); err == nil {
		if err := os.Remove(paths.RuntimePID); err != nil {
			return fmt.Errorf("remove stale runtime pid: %w", err)
		}
	}
	return nil
}

func RunDaemon(ctx context.Context, paths config.Paths, manager *Manager) error {
	if paths.RuntimeDir == "" || paths.RuntimePID == "" {
		return fmt.Errorf("runtime paths required")
	}
	if err := os.MkdirAll(paths.RuntimeDir, 0o755); err != nil {
		return fmt.Errorf("create runtime directory: %w", err)
	}
	if err := broker.WritePID(paths.RuntimePID); err != nil {
		return fmt.Errorf("write runtime pid: %w", err)
	}
	defer broker.RemovePID(paths.RuntimePID)

	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	startedAt := time.Now().UTC()
	if err := SaveState(paths, State{
		PID:       os.Getpid(),
		Running:   true,
		StartedAt: startedAt,
	}); err != nil {
		return err
	}

	if err := manager.Start(signalCtx); err != nil {
		_ = SaveState(paths, State{
			PID:       os.Getpid(),
			Running:   false,
			StartedAt: startedAt,
			StoppedAt: time.Now().UTC(),
			LastError: err.Error(),
		})
		return err
	}
	defer manager.Stop()

	ticker := time.NewTicker(config.Defaults.PollInterval)
	defer ticker.Stop()

	for {
		lastErr := ""
		if err := manager.LastDeliveryError(); err != nil {
			lastErr = err.Error()
		}
		if err := SaveState(paths, State{
			PID:        os.Getpid(),
			Running:    true,
			StartedAt:  startedAt,
			WatchCount: manager.WatchCount(),
			LastError:  lastErr,
		}); err != nil {
			return err
		}

		select {
		case <-signalCtx.Done():
			return SaveState(paths, State{
				PID:        os.Getpid(),
				Running:    false,
				StartedAt:  startedAt,
				StoppedAt:  time.Now().UTC(),
				WatchCount: manager.WatchCount(),
				LastError:  lastErr,
			})
		case <-ticker.C:
		}
	}
}
