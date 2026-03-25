package spawn

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Terminal int

const (
	TerminalApp Terminal = iota
	ITerm2
	LinuxDefault
	Unknown
)

// Detect returns the current terminal emulator.
func Detect() Terminal {
	// Check TERM_PROGRAM env var
	termProgram := os.Getenv("TERM_PROGRAM")

	switch termProgram {
	case "Apple_Terminal":
		return TerminalApp
	case "iTerm.app":
		return ITerm2
	}

	// On macOS, default to Terminal.app if TERM_PROGRAM not set
	if runtime.GOOS == "darwin" {
		return TerminalApp
	}

	// Check for Linux terminals in PATH
	if runtime.GOOS == "linux" {
		for _, term := range []string{"gnome-terminal", "xterm", "x-terminal-emulator"} {
			if _, err := exec.LookPath(term); err == nil {
				return LinuxDefault
			}
		}
	}

	return Unknown
}

// OpenTab opens a new terminal tab with the given command and env vars.
// Returns the PID of the spawned process.
func OpenTab(t Terminal, name string, cmd string, env map[string]string) (int, error) {
	// Build env string
	var envParts []string
	for k, v := range env {
		envParts = append(envParts, fmt.Sprintf("%s=%s", k, v))
	}
	envStr := strings.Join(envParts, " ")

	// Build full command with env vars
	fullCmd := cmd
	if envStr != "" {
		fullCmd = envStr + " " + cmd
	}

	switch t {
	case TerminalApp:
		// Use AppleScript to open a new tab in Terminal.app
		script := fmt.Sprintf(`tell application "Terminal" to do script "%s"`, fullCmd)
		execCmd := exec.Command("osascript", "-e", script)
		if err := execCmd.Run(); err != nil {
			return 0, fmt.Errorf("failed to open Terminal.app tab: %w", err)
		}
		// Poll for the spawned process PID using WAGGLE_AGENT_NAME env marker
		pid, err := findSpawnedPID(name, 3*time.Second)
		if err != nil {
			// Tab opened but couldn't find PID — return 0 as fallback
			return 0, nil
		}
		return pid, nil

	case ITerm2:
		// Use AppleScript to open a new tab in iTerm2
		script := fmt.Sprintf(`tell application "iTerm2" to tell current window to create tab with default profile command "%s"`, fullCmd)
		execCmd := exec.Command("osascript", "-e", script)
		if err := execCmd.Run(); err != nil {
			return 0, fmt.Errorf("failed to open iTerm2 tab: %w", err)
		}
		// Poll for the spawned process PID using WAGGLE_AGENT_NAME env marker
		pid, err := findSpawnedPID(name, 3*time.Second)
		if err != nil {
			// Tab opened but couldn't find PID — return 0 as fallback
			return 0, nil
		}
		return pid, nil

	case LinuxDefault:
		// Try gnome-terminal first
		if _, err := exec.LookPath("gnome-terminal"); err == nil {
			execCmd := exec.Command("gnome-terminal", "--", "bash", "-c", fullCmd)
			if err := execCmd.Start(); err != nil {
				return 0, fmt.Errorf("failed to open gnome-terminal: %w", err)
			}
			return execCmd.Process.Pid, nil
		}

		// Try xterm
		if _, err := exec.LookPath("xterm"); err == nil {
			execCmd := exec.Command("xterm", "-e", fullCmd)
			if err := execCmd.Start(); err != nil {
				return 0, fmt.Errorf("failed to open xterm: %w", err)
			}
			return execCmd.Process.Pid, nil
		}

		// Try x-terminal-emulator
		if _, err := exec.LookPath("x-terminal-emulator"); err == nil {
			execCmd := exec.Command("x-terminal-emulator", "-e", fullCmd)
			if err := execCmd.Start(); err != nil {
				return 0, fmt.Errorf("failed to open x-terminal-emulator: %w", err)
			}
			return execCmd.Process.Pid, nil
		}

		return 0, fmt.Errorf("no supported Linux terminal emulator found")

	case Unknown:
		return 0, fmt.Errorf("cannot detect terminal emulator")

	default:
		return 0, fmt.Errorf("unsupported terminal type: %v", t)
	}
}

// findSpawnedPID polls for a process with WAGGLE_AGENT_NAME=<name> in its environment.
// Uses pgrep -f to find the process by searching the full command line.
// Returns the PID if found within the timeout, or an error if not found.
func findSpawnedPID(name string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	searchPattern := "WAGGLE_AGENT_NAME=" + name

	for time.Now().Before(deadline) {
		// pgrep -f searches the full command line including env vars
		out, err := exec.Command("pgrep", "-f", searchPattern).Output()
		if err == nil && len(out) > 0 {
			// Parse first PID from output
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) > 0 {
				pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
				if err == nil && pid > 0 {
					return pid, nil
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}

	return 0, fmt.Errorf("could not find spawned process for %s within %v", name, timeout)
}

