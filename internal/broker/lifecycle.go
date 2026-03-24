package broker

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// WritePID writes the current process ID to the specified file.
func WritePID(pidFile string) error {
	pid := os.Getpid()
	return os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", pid)), 0600)
}

// ReadPID reads the process ID from the specified file.
func ReadPID(pidFile string) (int, error) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, err
	}
	
	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0, fmt.Errorf("invalid PID in file: %w", err)
	}
	
	return pid, nil
}

// IsRunning checks if the process identified by the PID file is running.
// Returns false if the PID file doesn't exist or the process is not running.
func IsRunning(pidFile string) bool {
	pid, err := ReadPID(pidFile)
	if err != nil {
		return false
	}
	
	// Send signal 0 to check if process exists
	// This doesn't actually send a signal, just checks permissions
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	
	// On Unix, FindProcess always succeeds, so we need to send signal 0
	err = process.Signal(syscall.Signal(0))
	if err != nil {
		return false
	}
	
	return true
}

// EnsureNotRunning returns an error if a broker is already running.
func EnsureNotRunning(pidFile string) error {
	if IsRunning(pidFile) {
		pid, _ := ReadPID(pidFile)
		return fmt.Errorf("broker already running (PID %d)", pid)
	}
	return nil
}

// CleanupStale removes stale PID and socket files if the process is not running.
func CleanupStale(pidFile, socketPath string) error {
	// Only cleanup if process is not running
	if IsRunning(pidFile) {
		return fmt.Errorf("cannot cleanup: broker is running")
	}
	
	// Remove stale PID file
	if _, err := os.Stat(pidFile); err == nil {
		if err := os.Remove(pidFile); err != nil {
			return fmt.Errorf("removing stale PID file: %w", err)
		}
	}
	
	// Remove stale socket file
	if _, err := os.Stat(socketPath); err == nil {
		if err := os.Remove(socketPath); err != nil {
			return fmt.Errorf("removing stale socket file: %w", err)
		}
	}
	
	return nil
}

// RemovePID removes the PID file.
func RemovePID(pidFile string) error {
	if err := os.Remove(pidFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing PID file: %w", err)
	}
	return nil
}

// EnsureDirs creates the specified directories if they don't exist.
func EnsureDirs(dirs ...string) error {
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("creating directory %s: %w", dir, err)
		}
	}
	return nil
}

