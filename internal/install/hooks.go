package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func registerSessionStartHook(directory string) error {
	return setHookCommand(directory, "SessionStart", waggleHookCommand, true)
}
func deregisterSessionStartHook(directory string) error {
	return setHookCommand(directory, "SessionStart", waggleHookCommand, false)
}

func retireClaudeDelivery(directory string) error {
	if err := setHookCommand(directory, "PreToolUse", wagglePushCommand, false); err != nil {
		return err
	}
	err := safeRemove(filepath.Join(directory, "hooks", "waggle-push.js"), filepath.Dir(directory))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// setHookCommand owns hook registration changes. Matching an exact command
// removes only that command; event metadata and unrelated sibling hooks survive.
func setHookCommand(directory, event, command string, present bool) error {
	path := filepath.Join(directory, "settings.json")
	settings, err := readSettingsJSON(path)
	if err != nil {
		return err
	}
	hooks := make(map[string]interface{})
	if value, exists := settings["hooks"]; exists {
		var ok bool
		hooks, ok = value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("settings hooks must be an object")
		}
	}
	entries := []interface{}{}
	if value, exists := hooks[event]; exists {
		var ok bool
		entries, ok = value.([]interface{})
		if !ok {
			return fmt.Errorf("settings %s hooks must be an array", event)
		}
	}
	filtered := make([]interface{}, 0, len(entries))
	found := false
	for _, value := range entries {
		entry, ok := value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("settings %s hook entry must be an object", event)
		}
		items, ok := entry["hooks"].([]interface{})
		if !ok {
			return fmt.Errorf("settings %s entry hooks must be an array", event)
		}
		kept := make([]interface{}, 0, len(items))
		for _, item := range items {
			hook, ok := item.(map[string]interface{})
			if !ok {
				return fmt.Errorf("settings %s hook must be an object", event)
			}
			if hook["command"] == command {
				if present && !found {
					kept = append(kept, hook)
				}
				found = true
			} else {
				kept = append(kept, hook)
			}
		}
		if len(kept) > 0 || len(items) == 0 {
			entry["hooks"] = kept
			filtered = append(filtered, entry)
		}
	}
	if present && !found {
		filtered = append(filtered, map[string]interface{}{"hooks": []interface{}{map[string]interface{}{"type": "command", "command": command}}})
	}
	if !present && !found {
		return nil
	}
	hooks[event] = filtered
	settings["hooks"] = hooks
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	info, err := os.Stat(path)
	if err == nil {
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect settings permissions: %w", err)
	}
	return safeWriteFile(path, data, mode, filepath.Dir(directory))
}
