package spawn

import (
	"fmt"
	"regexp"
	"strings"
)

// EnvMap is a map of environment variable key-value pairs.
type EnvMap map[string]string

// BuildShellCommand constructs a safe shell command string from env vars and a command.
// All values are single-quote escaped. Keys are validated (alphanumeric + underscore only).
// Returns: "KEY1='val1' KEY2='val2' cmd arg1 arg2"
func BuildShellCommand(env EnvMap, cmd string, args []string) (string, error) {
	if cmd == "" {
		return "", fmt.Errorf("command cannot be empty")
	}

	var parts []string

	// Build env vars with validation and quoting
	for k, v := range env {
		if err := validateEnvKey(k); err != nil {
			return "", err
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, shellQuote(v)))
	}

	// Add command
	parts = append(parts, cmd)

	// Add args
	parts = append(parts, args...)

	return strings.Join(parts, " "), nil
}

// BuildAppleScript wraps a shell command in AppleScript for Terminal.app or iTerm2.
// The shell command is escaped for embedding inside AppleScript double-quoted strings.
func BuildAppleScript(terminal Terminal, shellCmd string) string {
	escaped := escapeAppleScript(shellCmd)
	switch terminal {
	case TerminalApp:
		return fmt.Sprintf(`tell application "Terminal" to do script "%s"`, escaped)
	case ITerm2:
		return fmt.Sprintf(`tell application "iTerm2" to tell current window to create tab with default profile command "%s"`, escaped)
	default:
		return ""
	}
}

// BuildPgrepPattern constructs an exact-match pattern for finding a process by agent name.
// Prevents partial matches (e.g., "w" matching "worker-1").
func BuildPgrepPattern(name string) string {
	// Use word boundary pattern: WAGGLE_AGENT_NAME=<name> followed by space or end of line
	// This prevents "worker-1" from matching "worker-10"
	return fmt.Sprintf("WAGGLE_AGENT_NAME=%s( |$)", regexp.QuoteMeta(name))
}

// shellQuote wraps a value in single quotes with proper escaping.
// Single-quote escape: replace ' with '\'' (end quote, escaped quote, start quote)
func shellQuote(s string) string {
	// Replace each single quote with '\''
	escaped := strings.ReplaceAll(s, "'", `'\''`)
	return fmt.Sprintf("'%s'", escaped)
}

// escapeAppleScript escapes a string for use inside AppleScript double-quoted strings.
// Escapes: \ → \\, " → \"
func escapeAppleScript(s string) string {
	// Escape backslashes first, then quotes
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// validateEnvKey checks that an env key contains only [A-Za-z0-9_].
// Rejects keys with =, spaces, or shell metacharacters.
func validateEnvKey(key string) error {
	if key == "" {
		return fmt.Errorf("env key cannot be empty")
	}
	// Must start with letter or underscore, followed by alphanumeric or underscore
	matched, _ := regexp.MatchString(`^[A-Za-z_][A-Za-z0-9_]*$`, key)
	if !matched {
		return fmt.Errorf("invalid env key %q: must contain only [A-Za-z0-9_] and start with letter or underscore", key)
	}
	return nil
}

