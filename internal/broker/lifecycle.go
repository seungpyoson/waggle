package broker

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
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

// IsResponding checks if the broker at socketPath actually processes requests.
// Performs a dial+send+read probe: connects, sends a status request, reads a
// response. Returns false if any step fails or times out — meaning the broker
// is zombie (listening but not accepting/responding) or dead.
//
// The probe uses CmdStatus which is in the broker's noSessionRequired set,
// so no session state is created. The unnamed session is cleaned up when the
// probe closes the connection.
func IsResponding(socketPath string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("unix", socketPath, timeout)
	if err != nil {
		return false
	}
	defer conn.Close()

	// Set deadline for the entire send+read exchange
	conn.SetDeadline(time.Now().Add(timeout))

	// Send a status request (no session required)
	req := struct {
		Cmd string `json:"cmd"`
	}{Cmd: "status"}
	data, err := json.Marshal(req)
	if err != nil {
		return false
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return false
	}

	// Read any response — we just need to know the broker is processing
	scanner := bufio.NewScanner(conn)
	// Match broker's MaxMessageSize to handle any status response size
	bufSize := 64 * 1024 // 64KB — generous for status response (~200B)
	scanner.Buffer(make([]byte, bufSize), bufSize)
	if !scanner.Scan() {
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

// WaitForReady polls until the broker process is running or the timeout expires.
// Sleep is capped to remaining time so the function never overshoots the deadline.
func WaitForReady(pidFile string, timeout, interval time.Duration) error {
	if timeout <= 0 {
		return fmt.Errorf("WaitForReady: timeout must be positive, got %v", timeout)
	}
	if interval <= 0 {
		return fmt.Errorf("WaitForReady: interval must be positive, got %v", interval)
	}
	deadline := time.Now().Add(timeout)
	for {
		if IsRunning(pidFile) {
			return nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("broker failed to start within %v", timeout)
		}
		sleep := interval
		if remaining < sleep {
			sleep = remaining
		}
		time.Sleep(sleep)
	}
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

// appendEnvOverride removes any existing entry for key from env, then appends key=value.
// This ensures the injected value always wins, even if the key was already set.
func appendEnvOverride(env []string, key, value string) []string {
	prefix := key + "="
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			filtered = append(filtered, e)
		}
	}
	return append(filtered, prefix+value)
}

// StartDaemon forks the broker as a background process.
// It redirects stdout/stderr to logFile and returns immediately.
func StartDaemon(dataDir, socketDir, logFile, projectID string, args []string) error {
	// Ensure directories exist
	if err := EnsureDirs(dataDir, socketDir); err != nil {
		return err
	}

	// Open log file for stdout/stderr
	log, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("opening log file: %w", err)
	}
	defer log.Close()

	// Get current executable path
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("getting executable path: %w", err)
	}

	// Start process with --foreground flag
	procAttr := &os.ProcAttr{
		Files: []*os.File{nil, log, log}, // stdin=nil, stdout=log, stderr=log
		Dir:   "",
		Env:   appendEnvOverride(os.Environ(), "WAGGLE_PROJECT_ID", projectID),
	}

	process, err := os.StartProcess(exe, args, procAttr)
	if err != nil {
		return fmt.Errorf("starting daemon: %w", err)
	}

	// Release the process (don't wait for it)
	if err := process.Release(); err != nil {
		return fmt.Errorf("releasing daemon process: %w", err)
	}

	return nil
}
