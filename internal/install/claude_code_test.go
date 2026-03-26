package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInstall_HookCreated(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	hookPath := filepath.Join(tmpHome, ".claude", "hooks", "waggle-connect.sh")
	info, err := os.Stat(hookPath)
	if err != nil {
		t.Fatalf("hook not created: %v", err)
	}

	// Check executable
	if info.Mode().Perm()&0111 == 0 {
		t.Errorf("hook not executable: %o", info.Mode().Perm())
	}
}

func TestInstall_SkillsCreated(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	skillDir := filepath.Join(tmpHome, ".claude", "skills", "waggle")
	expectedSkills := []string{"waggle.md", "send.md", "inbox.md", "ack.md", "status.md", "claim.md", "done.md", "presence.md"}

	for _, skill := range expectedSkills {
		skillPath := filepath.Join(skillDir, skill)
		if _, err := os.Stat(skillPath); err != nil {
			t.Errorf("skill %s not created: %v", skill, err)
		}
	}
}

func TestInstall_HeartbeatCreated(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	heartbeatPath := filepath.Join(tmpHome, ".claude", "hooks", "waggle-heartbeat.sh")
	info, err := os.Stat(heartbeatPath)
	if err != nil {
		t.Fatalf("heartbeat not created: %v", err)
	}

	// Check executable
	if info.Mode().Perm()&0111 == 0 {
		t.Errorf("heartbeat not executable: %o", info.Mode().Perm())
	}
}

func TestInstall_Idempotent(t *testing.T) {
	tmpHome := t.TempDir()

	// Install twice
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("first install failed: %v", err)
	}
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("second install failed: %v", err)
	}

	// Verify files still exist and are correct
	hookPath := filepath.Join(tmpHome, ".claude", "hooks", "waggle-connect.sh")
	if _, err := os.Stat(hookPath); err != nil {
		t.Errorf("hook missing after second install: %v", err)
	}
}

func TestInstall_HookRegistered(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	settingsPath := filepath.Join(tmpHome, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings.json not created: %v", err)
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	hooks, ok := settings["hooks"].(map[string]interface{})
	if !ok {
		t.Fatal("hooks section missing")
	}

	sessionStart, ok := hooks["SessionStart"].([]interface{})
	if !ok {
		t.Fatal("SessionStart array missing")
	}

	// Check for waggle hook
	found := false
	for _, entry := range sessionStart {
		entryMap, _ := entry.(map[string]interface{})
		entryHooks, _ := entryMap["hooks"].([]interface{})
		for _, h := range entryHooks {
			hMap, _ := h.(map[string]interface{})
			if cmd, _ := hMap["command"].(string); cmd == "bash $HOME/.claude/hooks/waggle-connect.sh" {
				found = true
				break
			}
		}
	}

	if !found {
		t.Error("waggle hook not registered in settings.json")
	}
}

func TestInstall_NoDuplicate(t *testing.T) {
	tmpHome := t.TempDir()

	// Install twice
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("first install failed: %v", err)
	}
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("second install failed: %v", err)
	}

	settingsPath := filepath.Join(tmpHome, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings.json not found: %v", err)
	}

	var settings map[string]interface{}
	json.Unmarshal(data, &settings)

	hooks := settings["hooks"].(map[string]interface{})
	sessionStart := hooks["SessionStart"].([]interface{})

	// Count waggle entries
	count := 0
	for _, entry := range sessionStart {
		entryMap, _ := entry.(map[string]interface{})
		entryHooks, _ := entryMap["hooks"].([]interface{})
		for _, h := range entryHooks {
			hMap, _ := h.(map[string]interface{})
			if cmd, _ := hMap["command"].(string); cmd == "bash $HOME/.claude/hooks/waggle-connect.sh" {
				count++
			}
		}
	}

	if count != 1 {
		t.Errorf("expected 1 waggle entry, got %d", count)
	}
}

func TestInstall_PreservesExisting(t *testing.T) {
	tmpHome := t.TempDir()
	claudeDir := filepath.Join(tmpHome, ".claude")
	os.MkdirAll(claudeDir, 0755)

	// Create settings.json with existing hook
	existingSettings := map[string]interface{}{
		"hooks": map[string]interface{}{
			"SessionStart": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": "echo 'existing hook'",
						},
					},
				},
			},
		},
	}
	data, _ := json.MarshalIndent(existingSettings, "", "  ")
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0644)

	// Install waggle
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Verify both hooks present
	settingsPath := filepath.Join(claudeDir, "settings.json")
	data, _ = os.ReadFile(settingsPath)
	var settings map[string]interface{}
	json.Unmarshal(data, &settings)

	hooks := settings["hooks"].(map[string]interface{})
	sessionStart := hooks["SessionStart"].([]interface{})

	if len(sessionStart) != 2 {
		t.Errorf("expected 2 hooks, got %d", len(sessionStart))
	}

	// Verify existing hook still there
	foundExisting := false
	for _, entry := range sessionStart {
		entryMap, _ := entry.(map[string]interface{})
		entryHooks, _ := entryMap["hooks"].([]interface{})
		for _, h := range entryHooks {
			hMap, _ := h.(map[string]interface{})
			if cmd, _ := hMap["command"].(string); cmd == "echo 'existing hook'" {
				foundExisting = true
			}
		}
	}

	if !foundExisting {
		t.Error("existing hook was removed")
	}
}

func TestInstall_CreatesSettingsIfMissing(t *testing.T) {
	tmpHome := t.TempDir()

	// Don't create settings.json beforehand
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	settingsPath := filepath.Join(tmpHome, ".claude", "settings.json")
	if _, err := os.Stat(settingsPath); err != nil {
		t.Fatalf("settings.json not created: %v", err)
	}

	// Verify it has waggle hook
	data, _ := os.ReadFile(settingsPath)
	var settings map[string]interface{}
	json.Unmarshal(data, &settings)

	hooks := settings["hooks"].(map[string]interface{})
	sessionStart := hooks["SessionStart"].([]interface{})

	if len(sessionStart) != 1 {
		t.Errorf("expected 1 hook, got %d", len(sessionStart))
	}
}

func TestUninstall_Clean(t *testing.T) {
	tmpHome := t.TempDir()

	// Install first
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Uninstall
	if err := uninstallClaudeCode(tmpHome); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}

	// Verify files removed
	hookPath := filepath.Join(tmpHome, ".claude", "hooks", "waggle-connect.sh")
	if _, err := os.Stat(hookPath); !os.IsNotExist(err) {
		t.Error("hook still exists after uninstall")
	}

	heartbeatPath := filepath.Join(tmpHome, ".claude", "hooks", "waggle-heartbeat.sh")
	if _, err := os.Stat(heartbeatPath); !os.IsNotExist(err) {
		t.Error("heartbeat still exists after uninstall")
	}

	skillDir := filepath.Join(tmpHome, ".claude", "skills", "waggle")
	if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
		t.Error("skill directory still exists after uninstall")
	}
}

func TestUninstall_DeregistersHook(t *testing.T) {
	tmpHome := t.TempDir()

	// Install then uninstall
	installClaudeCode(tmpHome)
	if err := uninstallClaudeCode(tmpHome); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}

	// Verify hook removed from settings.json
	settingsPath := filepath.Join(tmpHome, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		// settings.json might not exist if it was empty
		return
	}

	var settings map[string]interface{}
	json.Unmarshal(data, &settings)

	hooks, ok := settings["hooks"].(map[string]interface{})
	if !ok {
		return
	}

	sessionStart, ok := hooks["SessionStart"].([]interface{})
	if !ok {
		return
	}

	// Check no waggle entry
	for _, entry := range sessionStart {
		entryMap, _ := entry.(map[string]interface{})
		entryHooks, _ := entryMap["hooks"].([]interface{})
		for _, h := range entryHooks {
			hMap, _ := h.(map[string]interface{})
			if cmd, _ := hMap["command"].(string); cmd == "bash $HOME/.claude/hooks/waggle-connect.sh" {
				t.Error("waggle hook still in settings.json after uninstall")
			}
		}
	}
}

func TestUninstall_PreservesOther(t *testing.T) {
	tmpHome := t.TempDir()
	claudeDir := filepath.Join(tmpHome, ".claude")
	os.MkdirAll(claudeDir, 0755)

	// Create settings with existing hook
	existingSettings := map[string]interface{}{
		"hooks": map[string]interface{}{
			"SessionStart": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": "echo 'keep this'",
						},
					},
				},
			},
		},
	}
	data, _ := json.MarshalIndent(existingSettings, "", "  ")
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0644)

	// Install then uninstall
	installClaudeCode(tmpHome)
	uninstallClaudeCode(tmpHome)

	// Verify existing hook still there
	settingsPath := filepath.Join(claudeDir, "settings.json")
	data, _ = os.ReadFile(settingsPath)
	var settings map[string]interface{}
	json.Unmarshal(data, &settings)

	hooks := settings["hooks"].(map[string]interface{})
	sessionStart := hooks["SessionStart"].([]interface{})

	if len(sessionStart) != 1 {
		t.Errorf("expected 1 hook after uninstall, got %d", len(sessionStart))
	}

	// Verify it's the existing hook
	entryMap := sessionStart[0].(map[string]interface{})
	entryHooks := entryMap["hooks"].([]interface{})
	hMap := entryHooks[0].(map[string]interface{})
	if cmd := hMap["command"].(string); cmd != "echo 'keep this'" {
		t.Errorf("wrong hook preserved: %s", cmd)
	}
}

func TestUninstall_IdempotentNoFiles(t *testing.T) {
	tmpHome := t.TempDir()

	// Uninstall without installing first
	if err := uninstallClaudeCode(tmpHome); err != nil {
		t.Fatalf("uninstall failed when nothing installed: %v", err)
	}

	// Should be no error
}

