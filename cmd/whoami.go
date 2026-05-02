package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/seungpyoson/waggle/internal/config"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(whoamiCmd)
}

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show this shell's Waggle runtime identity",
	RunE: func(cmd *cobra.Command, args []string) error {
		runtimePaths := config.NewPaths("")
		if runtimePaths.RuntimeDir == "" {
			printJSON(map[string]any{"ok": false, "found": false, "reason": "HOME not set"})
			return nil
		}

		ppid := currentAgentPPID()
		if ppid != "" && !isSafeRuntimeToken(ppid) {
			printJSON(map[string]any{"ok": false, "found": false, "reason": "invalid WAGGLE_AGENT_PPID"})
			return nil
		}

		nonce, source, err := readCurrentSessionNonce(runtimePaths.RuntimeDir, ppid, os.Getenv("TTY"))
		if err != nil {
			printJSON(map[string]any{"ok": false, "found": false, "reason": err.Error()})
			return nil
		}
		if !isSafeRuntimeToken(nonce) {
			printJSON(map[string]any{"ok": false, "found": false, "reason": "invalid session mapping"})
			return nil
		}

		sessionData, err := os.ReadFile(filepath.Join(runtimePaths.RuntimeDir, "agent-session-"+nonce))
		if err != nil {
			printJSON(map[string]any{"ok": false, "found": false, "reason": "session mapping is stale"})
			return nil
		}
		lines := strings.Split(strings.TrimRight(string(sessionData), "\n"), "\n")
		if len(lines) < 2 || !isSafeRuntimeToken(lines[0]) || !isSafeRuntimeToken(lines[1]) {
			printJSON(map[string]any{"ok": false, "found": false, "reason": "session mapping is malformed"})
			return nil
		}

		printJSON(map[string]any{
			"ok":          true,
			"found":       true,
			"agent_name":  lines[0],
			"project_key": lines[1],
			"ppid":        ppid,
			"source":      source,
		})
		return nil
	},
}

func currentAgentPPID() string {
	ppid := os.Getenv("WAGGLE_AGENT_PPID")
	if ppid == "" {
		ppid = strconv.Itoa(os.Getppid())
	}
	return ppid
}

func readCurrentSessionNonce(runtimeDir, ppid, tty string) (string, string, error) {
	if ppid != "" && isSafeRuntimeToken(ppid) {
		if data, err := os.ReadFile(filepath.Join(runtimeDir, "agent-ppid-"+ppid)); err == nil {
			return strings.TrimSpace(string(data)), "ppid", nil
		}
	}
	if ttyName := safeTTYToken(tty); ttyName != "" {
		if data, err := os.ReadFile(filepath.Join(runtimeDir, "agent-tty-"+ttyName)); err == nil {
			return strings.TrimSpace(string(data)), "tty", nil
		}
	}
	return "", "", fmt.Errorf("no session mapping for parent process or TTY")
}

func safeTTYToken(tty string) string {
	tty = strings.TrimSpace(tty)
	if tty == "" {
		return ""
	}
	base := filepath.Base(tty)
	if !isSafeRuntimeToken(base) {
		return ""
	}
	return base
}

func isSafeRuntimeToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}
