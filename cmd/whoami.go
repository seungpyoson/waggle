package cmd

import (
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

		ppid := os.Getenv("WAGGLE_AGENT_PPID")
		if ppid == "" {
			ppid = strconv.Itoa(os.Getppid())
		}
		if !isSafeRuntimeToken(ppid) {
			printJSON(map[string]any{"ok": false, "found": false, "reason": "invalid WAGGLE_AGENT_PPID"})
			return nil
		}

		nonceData, err := os.ReadFile(filepath.Join(runtimePaths.RuntimeDir, "agent-ppid-"+ppid))
		if err != nil {
			printJSON(map[string]any{"ok": false, "found": false, "reason": "no session mapping for parent process"})
			return nil
		}
		nonce := strings.TrimSpace(string(nonceData))
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
		})
		return nil
	},
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
