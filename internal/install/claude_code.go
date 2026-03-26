package install

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed all:claude-code
var claudeCodeFiles embed.FS

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

	// 1. Copy hook
	hookDir := filepath.Join(claudeDir, "hooks")
	if err := os.MkdirAll(hookDir, 0755); err != nil {
		return fmt.Errorf("creating hooks dir: %w", err)
	}
	hookData, err := claudeCodeFiles.ReadFile("claude-code/hook.sh")
	if err != nil {
		return fmt.Errorf("reading embedded hook.sh: %w", err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "waggle-connect.sh"), hookData, 0755); err != nil {
		return fmt.Errorf("writing hook: %w", err)
	}

	// 2. Copy heartbeat script
	heartbeatData, err := claudeCodeFiles.ReadFile("claude-code/heartbeat.sh")
	if err != nil {
		return fmt.Errorf("reading embedded heartbeat.sh: %w", err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "waggle-heartbeat.sh"), heartbeatData, 0755); err != nil {
		return fmt.Errorf("writing heartbeat: %w", err)
	}

	// 3. Copy skills
	skillDir := filepath.Join(claudeDir, "skills", "waggle")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return fmt.Errorf("creating skills dir: %w", err)
	}
	skillFiles := []string{"waggle.md", "send.md", "inbox.md", "ack.md", "status.md", "claim.md", "done.md", "presence.md"}
	for _, name := range skillFiles {
		data, err := claudeCodeFiles.ReadFile("claude-code/skills/" + name)
		if err != nil {
			return fmt.Errorf("reading embedded skill %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, name), data, 0644); err != nil {
			return fmt.Errorf("writing skill %s: %w", name, err)
		}
	}

	// 4. Register hook in settings.json
	if err := registerSessionStartHook(claudeDir); err != nil {
		return fmt.Errorf("registering hook: %w", err)
	}

	return nil
}

// uninstallClaudeCode is the internal implementation that takes a home directory.
func uninstallClaudeCode(homeDir string) error {
	claudeDir := filepath.Join(homeDir, ".claude")

	// Remove hook files
	for _, name := range []string{"waggle-connect.sh", "waggle-heartbeat.sh"} {
		if err := os.Remove(filepath.Join(claudeDir, "hooks", name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s: %w", name, err)
		}
	}

	// Remove skill directory
	if err := os.RemoveAll(filepath.Join(claudeDir, "skills", "waggle")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing skills directory: %w", err)
	}

	// Deregister hook from settings.json
	if err := deregisterSessionStartHook(claudeDir); err != nil {
		return fmt.Errorf("deregistering hook: %w", err)
	}

	return nil
}

// readSettingsJSON reads and parses settings.json.
// Always returns a usable map — missing, empty, or corrupted files
// produce an empty map. The file is input, not a precondition.
func readSettingsJSON(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return make(map[string]interface{}), nil
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return make(map[string]interface{}), nil
	}
	return settings, nil
}

// registerSessionStartHook adds the waggle hook to settings.json SessionStart array.
// Uses JSON parsing to safely merge without overwriting existing hooks.
func registerSessionStartHook(claudeDir string) error {
	settingsPath := filepath.Join(claudeDir, "settings.json")

	// Read existing settings
	settings, _ := readSettingsJSON(settingsPath)

	// Get or create hooks section
	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = make(map[string]interface{})
	}

	// Get or create SessionStart array
	sessionStart, _ := hooks["SessionStart"].([]interface{})

	// Check if waggle hook already registered
	waggleHook := map[string]interface{}{
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": "bash $HOME/.claude/hooks/waggle-connect.sh",
			},
		},
	}

	// Check for existing waggle entry
	for _, entry := range sessionStart {
		entryMap, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		entryHooks, _ := entryMap["hooks"].([]interface{})
		for _, h := range entryHooks {
			hMap, _ := h.(map[string]interface{})
			if cmd, _ := hMap["command"].(string); cmd == "bash $HOME/.claude/hooks/waggle-connect.sh" {
				return nil // already registered
			}
		}
	}

	// Add waggle hook
	sessionStart = append(sessionStart, waggleHook)
	hooks["SessionStart"] = sessionStart
	settings["hooks"] = hooks

	// Write back
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling settings: %w", err)
	}

	return os.WriteFile(settingsPath, out, 0644)
}

// deregisterSessionStartHook removes the waggle hook from settings.json.
func deregisterSessionStartHook(claudeDir string) error {
	settingsPath := filepath.Join(claudeDir, "settings.json")

	settings, _ := readSettingsJSON(settingsPath)

	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		return nil
	}

	sessionStart, _ := hooks["SessionStart"].([]interface{})
	if sessionStart == nil {
		return nil
	}

	// Filter out waggle entries
	var filtered []interface{}
	for _, entry := range sessionStart {
		entryMap, ok := entry.(map[string]interface{})
		if !ok {
			filtered = append(filtered, entry)
			continue
		}
		entryHooks, _ := entryMap["hooks"].([]interface{})
		isWaggle := false
		for _, h := range entryHooks {
			hMap, _ := h.(map[string]interface{})
			if cmd, _ := hMap["command"].(string); cmd == "bash $HOME/.claude/hooks/waggle-connect.sh" {
				isWaggle = true
				break
			}
		}
		if !isWaggle {
			filtered = append(filtered, entry)
		}
	}

	hooks["SessionStart"] = filtered
	settings["hooks"] = hooks

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling settings: %w", err)
	}

	return os.WriteFile(settingsPath, out, 0644)
}

