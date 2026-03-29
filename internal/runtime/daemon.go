package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/seungpyoson/waggle/internal/broker"
	"github.com/seungpyoson/waggle/internal/config"
)

type State struct {
	PID          int             `json:"pid,omitempty"`
	Running      bool            `json:"running"`
	StartedAt    time.Time       `json:"started_at,omitempty"`
	StoppedAt    time.Time       `json:"stopped_at,omitempty"`
	WatchCount   int             `json:"watch_count,omitempty"`
	LastError    string          `json:"last_error,omitempty"`
	RecentErrors []ErrorEntry    `json:"recent_errors,omitempty"`
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
	if err := writeFileAtomic(paths.RuntimeState, data, 0o644); err != nil {
		return fmt.Errorf("write runtime state: %w", err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	tmp, err := os.CreateTemp(dir, base+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
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

func WaitForReady(paths config.Paths, timeout, interval time.Duration) error {
	if timeout <= 0 {
		return fmt.Errorf("WaitForReady: timeout must be positive, got %v", timeout)
	}
	if interval <= 0 {
		return fmt.Errorf("WaitForReady: interval must be positive, got %v", interval)
	}

	deadline := time.Now().Add(timeout)
	for {
		if state, err := currentState(paths); err == nil && state.Running {
			if pid, err := broker.ReadPID(paths.RuntimePID); err == nil && state.PID == pid {
				return nil
			}
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("runtime failed to become ready within %v", timeout)
		}
		sleep := interval
		if remaining < sleep {
			sleep = remaining
		}
		time.Sleep(sleep)
	}
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

func AcquireStartLock(paths config.Paths, staleAfter time.Duration) (func() error, error) {
	if paths.RuntimeStartLockDir == "" {
		return nil, fmt.Errorf("runtime start lock path required")
	}
	if staleAfter <= 0 {
		return nil, fmt.Errorf("staleAfter must be positive, got %v", staleAfter)
	}
	if err := os.MkdirAll(paths.RuntimeDir, 0o755); err != nil {
		return nil, fmt.Errorf("create runtime directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.RuntimeStartLockDir), 0o755); err != nil {
		return nil, fmt.Errorf("create runtime start lock parent: %w", err)
	}

	if err := os.Mkdir(paths.RuntimeStartLockDir, 0o755); err == nil {
		return func() error {
			if err := os.Remove(paths.RuntimeStartLockDir); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("release runtime start lock: %w", err)
			}
			return nil
		}, nil
	} else if !os.IsExist(err) {
		return nil, fmt.Errorf("acquire runtime start lock: %w", err)
	}

	info, err := os.Stat(paths.RuntimeStartLockDir)
	if err != nil {
		if os.IsNotExist(err) {
			return AcquireStartLock(paths, staleAfter)
		}
		return nil, fmt.Errorf("stat runtime start lock: %w", err)
	}
	if time.Since(info.ModTime()) > staleAfter {
		if err := os.RemoveAll(paths.RuntimeStartLockDir); err != nil {
			return nil, fmt.Errorf("remove stale runtime start lock: %w", err)
		}
		return AcquireStartLock(paths, staleAfter)
	}
	return nil, fmt.Errorf("runtime start already in progress")
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
	current := State{
		PID:       os.Getpid(),
		Running:   false,
		StartedAt: startedAt,
	}
	if err := SaveState(paths, current); err != nil {
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

	current.Running = true
	current.WatchCount = manager.WatchCount()
	if err := SaveState(paths, current); err != nil {
		return err
	}
	lastSaved := current

	ticker := time.NewTicker(config.Defaults.RuntimeStateRefreshInterval)
	defer ticker.Stop()

	for {
		lastErr := ""
		if err := manager.LastDeliveryError(); err != nil {
			lastErr = err.Error()
		}
		current = State{
			PID:          os.Getpid(),
			Running:      true,
			StartedAt:    startedAt,
			WatchCount:   manager.WatchCount(),
			LastError:    lastErr,
			RecentErrors: manager.RecentErrors(),
		}
		if !sameState(lastSaved, current) {
			if err := SaveState(paths, current); err != nil {
				return err
			}
			lastSaved = current
		}

		select {
		case <-signalCtx.Done():
			current = State{
				PID:        os.Getpid(),
				Running:    false,
				StartedAt:  startedAt,
				StoppedAt:  time.Now().UTC(),
				WatchCount: manager.WatchCount(),
				LastError:  lastErr,
			}
			if !sameState(lastSaved, current) {
				if err := SaveState(paths, current); err != nil {
					return err
				}
			}
			return nil
		case <-ticker.C:
		}
	}
}

func currentState(paths config.Paths) (State, error) {
	state, err := LoadState(paths)
	if err != nil {
		return State{}, err
	}
	return state, nil
}

func sameState(a, b State) bool {
	// Note: RecentErrors is intentionally excluded from comparison.
	// Allow up-to-2s propagation latency for error ring buffer updates.
	return a.PID == b.PID &&
		a.Running == b.Running &&
		a.StartedAt.Equal(b.StartedAt) &&
		a.StoppedAt.Equal(b.StoppedAt) &&
		a.WatchCount == b.WatchCount &&
		a.LastError == b.LastError
}

func IsRunning(paths config.Paths) bool {
	pid, err := broker.ReadPID(paths.RuntimePID)
	if err != nil {
		return false
	}
	state, err := currentState(paths)
	if err != nil {
		return false
	}
	if !state.Running || state.PID != pid {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}
