package install

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// waggleHookCommand is the canonical command string written to settings.json.
// Used by install, uninstall, and health checks. Single source of truth.
const waggleHookCommand = "bash $HOME/.claude/hooks/waggle-connect.sh"

// wagglePushCommand identifies the retired hook for exact removal only.
const wagglePushCommand = "WAGGLE_PPID=$PPID node $HOME/.claude/hooks/waggle-push.js"

// The canonical Claude Code integration assets live in integrations/claude-code/.
// This mirrored copy exists in-package so go:embed can bundle them for install.
//
//go:embed all:claude-code
var claudeCodeFiles embed.FS

// hookFiles is the single source of truth for hook file installation.
// Maps embedded asset name → installed filename under ~/.claude/hooks/.
// Used by both install and uninstall to prevent orphaned files.
var hookFiles = []struct {
	embedded, installed string
	perm                os.FileMode
}{
	{"claude-code/hook.sh", "waggle-connect.sh", 0o755},
	{"claude-code/heartbeat.sh", "waggle-heartbeat.sh", 0o755},
}

// InstallClaudeCode installs waggle integration for Claude Code.
// Copies hook, heartbeat, and skills to ~/.claude/ and registers the hook in settings.json.
func InstallClaudeCode() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}
	return installClaudeCode(home)
}

// UninstallClaudeCode removes waggle integration from Claude Code.
// Removes hook, heartbeat, skills, and deregisters from settings.json.
func UninstallClaudeCode() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}
	return uninstallClaudeCode(home)
}

// installClaudeCode is the internal implementation that takes a home directory.
// This allows testing with t.TempDir() instead of real ~/.claude/
func installClaudeCode(homeDir string) error {
	claudeDir := filepath.Join(homeDir, ".claude")
	if _, err := readSettingsJSON(filepath.Join(claudeDir, "settings.json")); err != nil {
		return err
	}

	// 1. Copy hook files (from single source of truth: hookFiles)
	hookDir := filepath.Join(claudeDir, "hooks")
	if err := safeMkdirAll(hookDir, homeDir, 0o755); err != nil {
		return fmt.Errorf("creating hooks dir: %w", err)
	}
	for _, hf := range hookFiles {
		data, err := claudeCodeFiles.ReadFile(hf.embedded)
		if err != nil {
			return fmt.Errorf("reading embedded %s: %w", hf.embedded, err)
		}
		if err := safeWriteFile(filepath.Join(hookDir, hf.installed), data, hf.perm, homeDir); err != nil {
			return fmt.Errorf("writing %s: %w", hf.installed, err)
		}
	}

	// 2. Copy skills
	skillDir := filepath.Join(claudeDir, "skills", "waggle")
	if err := safeMkdirAll(skillDir, homeDir, 0o755); err != nil {
		return fmt.Errorf("creating skills dir: %w", err)
	}
	skillFiles := []string{"waggle.md", "send.md", "inbox.md", "ack.md", "status.md", "claim.md", "done.md", "presence.md"}
	for _, name := range skillFiles {
		data, err := claudeCodeFiles.ReadFile("claude-code/skills/" + name)
		if err != nil {
			return fmt.Errorf("reading embedded skill %s: %w", name, err)
		}
		if err := safeWriteFile(filepath.Join(skillDir, name), data, 0o644, homeDir); err != nil {
			return fmt.Errorf("writing skill %s: %w", name, err)
		}
	}

	// 3. Register hooks in settings.json
	if err := registerSessionStartHook(claudeDir); err != nil {
		return fmt.Errorf("registering hook: %w", err)
	}

	if err := retireClaudeDelivery(claudeDir); err != nil {
		return fmt.Errorf("retiring delivery hook: %w", err)
	}

	// 4. Retire the former shell delivery path.
	if err := uninstallShellHook(homeDir); err != nil {
		return fmt.Errorf("retiring shell hook: %w", err)
	}

	return nil
}

// uninstallClaudeCode is the internal implementation that takes a home directory.
func uninstallClaudeCode(homeDir string) error {
	claudeDir := filepath.Join(homeDir, ".claude")
	if _, err := readSettingsJSON(filepath.Join(claudeDir, "settings.json")); err != nil {
		return err
	}

	// Remove hook files (from single source of truth: hookFiles)
	for _, hf := range hookFiles {
		if err := safeRemove(filepath.Join(claudeDir, "hooks", hf.installed), homeDir); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s: %w", hf.installed, err)
		}
	}

	// Remove skill directory
	if err := safeRemoveAll(filepath.Join(claudeDir, "skills", "waggle"), homeDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing skills directory: %w", err)
	}

	// Deregister hooks from settings.json
	if err := deregisterSessionStartHook(claudeDir); err != nil {
		return fmt.Errorf("deregistering hook: %w", err)
	}

	if err := retireClaudeDelivery(claudeDir); err != nil {
		return fmt.Errorf("deregistering push hook: %w", err)
	}

	return nil
}

// readSettingsJSON reads and parses settings.json.
// Missing or empty files are treated as empty settings. Invalid JSON returns
// an error so callers do not silently discard unrelated user settings.
func readSettingsJSON(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]interface{}), nil
		}
		return nil, fmt.Errorf("read settings.json: %w", err)
	}
	if len(data) == 0 {
		return make(map[string]interface{}), nil
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("parse settings.json: %w", err)
	}
	if settings == nil {
		return nil, fmt.Errorf("settings.json must contain an object")
	}
	return settings, nil
}
