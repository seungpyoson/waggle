package spawn

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
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
		// Note: AppleScript doesn't return the child PID directly
		// This is a known limitation mentioned in the brief
		return 0, nil

	case ITerm2:
		// Use AppleScript to open a new tab in iTerm2
		script := fmt.Sprintf(`tell application "iTerm2" to tell current window to create tab with default profile command "%s"`, fullCmd)
		execCmd := exec.Command("osascript", "-e", script)
		if err := execCmd.Run(); err != nil {
			return 0, fmt.Errorf("failed to open iTerm2 tab: %w", err)
		}
		// Note: AppleScript doesn't return the child PID directly
		return 0, nil

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

