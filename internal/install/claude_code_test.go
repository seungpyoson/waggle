package install

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestInstall_HookUsesRuntimeBridgeNotBackgroundListener(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	hookPath := filepath.Join(tmpHome, ".claude", "hooks", "waggle-connect.sh")
	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}

	content := string(data)
	for _, forbidden := range []string{"waggle listen", "pkill -f", "disown", "waggle runtime start", "waggle runtime watch", "waggle runtime pull"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("hook should not contain %q", forbidden)
		}
	}
	if !strings.Contains(content, "waggle adapter bootstrap claude-code") {
		t.Fatalf("hook should contain %q", "waggle adapter bootstrap claude-code")
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

func TestInstall_PushHookCreated(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	pushPath := filepath.Join(tmpHome, ".claude", "hooks", "waggle-push.js")
	info, err := os.Stat(pushPath)
	if err != nil {
		t.Fatalf("push hook not created: %v", err)
	}

	// Check executable
	if info.Mode().Perm()&0111 == 0 {
		t.Errorf("push hook not executable: %o", info.Mode().Perm())
	}

	// Verify content uses WAGGLE_PPID env var
	data, _ := os.ReadFile(pushPath)
	content := string(data)
	if !strings.Contains(content, "WAGGLE_PPID") {
		t.Error("push hook missing WAGGLE_PPID env var usage")
	}
	if !strings.Contains(content, "agent-ppid-") {
		t.Error("push hook missing agent-ppid- map file reference")
	}
	if !strings.Contains(content, "toLowerCase()") {
		t.Error("push hook missing lowercase TTY fallback normalization")
	}
	if !strings.Contains(content, "additionalContext") {
		t.Error("push hook missing additionalContext output")
	}
	if !strings.Contains(content, `if (!/^\d+$/.test(ppid)) process.exit(0);`) {
		t.Error("push hook missing non-numeric PPID early exit")
	}
}

func TestInstall_PushHookRecoversOrphansAndRefreshesBothMappings(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	pushPath := filepath.Join(tmpHome, ".claude", "hooks", "waggle-push.js")
	data, err := os.ReadFile(pushPath)
	if err != nil {
		t.Fatalf("read push hook: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "fs.utimesSync(pointerFile, now, now)") {
		t.Fatal("push hook should refresh pointer mapping alongside session mapping")
	}
	if !strings.Contains(content, "fs.readdirSync(") {
		t.Fatal("push hook should recover orphaned consumed signal files")
	}
}

func TestInstall_PushHookValidatesSessionTokens(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	pushPath := filepath.Join(tmpHome, ".claude", "hooks", "waggle-push.js")
	data, err := os.ReadFile(pushPath)
	if err != nil {
		t.Fatalf("read push hook: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "const tokenPattern = /^[a-zA-Z0-9_-]+$/;") {
		t.Fatal("push hook should define session token validation regex")
	}
	if !strings.Contains(content, "if (!tokenPattern.test(agent) || !tokenPattern.test(project)) process.exit(0);") {
		t.Fatal("push hook should reject invalid agent/project tokens from session file")
	}
}

func TestPushHookSourcesStayIdentical(t *testing.T) {
	installCopy, err := os.ReadFile(filepath.Join("claude-code", "waggle-push.js"))
	if err != nil {
		t.Fatalf("read install copy: %v", err)
	}

	integrationCopy, err := os.ReadFile(filepath.Join("..", "..", "integrations", "claude-code", "waggle-push.js"))
	if err != nil {
		t.Fatalf("read integration copy: %v", err)
	}

	if string(installCopy) != string(integrationCopy) {
		t.Fatal("waggle-push.js copies diverged")
	}
}

func TestInstall_PreToolUseHookRegistered(t *testing.T) {
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

	preToolUse, ok := hooks["PreToolUse"].([]interface{})
	if !ok {
		t.Fatal("PreToolUse array missing")
	}

	found := false
	for _, entry := range preToolUse {
		entryMap, _ := entry.(map[string]interface{})
		entryHooks, _ := entryMap["hooks"].([]interface{})
		for _, h := range entryHooks {
			hMap, _ := h.(map[string]interface{})
			if cmd, _ := hMap["command"].(string); cmd == wagglePushCommand {
				found = true
				break
			}
		}
	}

	if !found {
		t.Error("waggle push hook not registered in PreToolUse")
	}

	// Verify the command uses WAGGLE_PPID=$PPID prefix
	if !strings.Contains(wagglePushCommand, "WAGGLE_PPID=$PPID") {
		t.Error("wagglePushCommand missing WAGGLE_PPID=$PPID prefix")
	}
}

func TestInstall_PreToolUseNoDuplicate(t *testing.T) {
	tmpHome := t.TempDir()

	// Install twice
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("first install failed: %v", err)
	}
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("second install failed: %v", err)
	}

	settingsPath := filepath.Join(tmpHome, ".claude", "settings.json")
	data, _ := os.ReadFile(settingsPath)
	var settings map[string]interface{}
	json.Unmarshal(data, &settings)

	hooks := settings["hooks"].(map[string]interface{})
	preToolUse := hooks["PreToolUse"].([]interface{})

	count := 0
	for _, entry := range preToolUse {
		entryMap, _ := entry.(map[string]interface{})
		entryHooks, _ := entryMap["hooks"].([]interface{})
		for _, h := range entryHooks {
			hMap, _ := h.(map[string]interface{})
			if cmd, _ := hMap["command"].(string); cmd == wagglePushCommand {
				count++
			}
		}
	}

	if count != 1 {
		t.Errorf("expected 1 PreToolUse waggle entry, got %d", count)
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

	pushPath := filepath.Join(tmpHome, ".claude", "hooks", "waggle-push.js")
	if _, err := os.Stat(pushPath); !os.IsNotExist(err) {
		t.Error("push hook still exists after uninstall")
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

	// Check no SessionStart waggle entry
	sessionStart, _ := hooks["SessionStart"].([]interface{})
	for _, entry := range sessionStart {
		entryMap, _ := entry.(map[string]interface{})
		entryHooks, _ := entryMap["hooks"].([]interface{})
		for _, h := range entryHooks {
			hMap, _ := h.(map[string]interface{})
			if cmd, _ := hMap["command"].(string); cmd == waggleHookCommand {
				t.Error("waggle SessionStart hook still in settings.json after uninstall")
			}
		}
	}

	// Check no PreToolUse waggle entry
	preToolUse, _ := hooks["PreToolUse"].([]interface{})
	for _, entry := range preToolUse {
		entryMap, _ := entry.(map[string]interface{})
		entryHooks, _ := entryMap["hooks"].([]interface{})
		for _, h := range entryHooks {
			hMap, _ := h.(map[string]interface{})
			if cmd, _ := hMap["command"].(string); cmd == wagglePushCommand {
				t.Error("waggle PreToolUse hook still in settings.json after uninstall")
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

func TestUninstall_PermissionError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test not meaningful when running as root")
	}

	tmpHome := t.TempDir()

	// Install first
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Make hooks directory non-writable to prevent deletion
	hooksDir := filepath.Join(tmpHome, ".claude", "hooks")
	os.Chmod(hooksDir, 0555)
	t.Cleanup(func() {
		os.Chmod(hooksDir, 0755)
	})

	// Uninstall should return an error (not silently succeed)
	err := uninstallClaudeCode(tmpHome)
	if err == nil {
		t.Error("expected permission error, got nil")
	}
}

func TestEmbeddedFilesMatchSource(t *testing.T) {
	// The test file is in internal/install/, so ../../integrations/claude-code/ is the source
	sourceDir := filepath.Join("..", "..", "integrations", "claude-code")
	embedDir := filepath.Join("claude-code")

	// Check source dir exists
	if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
		t.Skip("source directory not found (running outside repo root)")
	}

	sourceFiles := make(map[string][]byte)
	err := filepath.WalkDir(sourceDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(sourceDir, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sourceFiles[rel] = data
		return nil
	})
	if err != nil {
		t.Fatalf("walking source dir: %v", err)
	}

	embedFiles := make(map[string][]byte)
	err = filepath.WalkDir(embedDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(embedDir, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		embedFiles[rel] = data
		return nil
	})
	if err != nil {
		t.Fatalf("walking embed dir: %v", err)
	}

	// Check all source files exist in embed dir with same content
	for name, sourceData := range sourceFiles {
		embedData, ok := embedFiles[name]
		if !ok {
			t.Errorf("file %s exists in integrations/claude-code/ but not in internal/install/claude-code/", name)
			continue
		}
		if !bytes.Equal(sourceData, embedData) {
			t.Errorf("embedded copy diverged from source: %s — run 'cp integrations/claude-code/%s internal/install/claude-code/%s' to sync", name, name, name)
		}
	}

	// Check no extra files in embed dir
	for name := range embedFiles {
		if _, ok := sourceFiles[name]; !ok {
			t.Errorf("file %s exists in internal/install/claude-code/ but not in integrations/claude-code/", name)
		}
	}

	if len(sourceFiles) != len(embedFiles) {
		t.Errorf("file count mismatch: source=%d, embed=%d", len(sourceFiles), len(embedFiles))
	}
}

func TestInstall_EmptySettingsJSON(t *testing.T) {
	tmpHome := t.TempDir()
	claudeDir := filepath.Join(tmpHome, ".claude")
	os.MkdirAll(claudeDir, 0755)
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(""), 0644)

	err := installClaudeCode(tmpHome)
	if err != nil {
		t.Fatalf("install failed with empty settings.json: %v", err)
	}

	// Verify settings.json is now valid with waggle hook
	data, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("settings.json is not valid JSON after install: %v", err)
	}
	hooks, ok := settings["hooks"].(map[string]interface{})
	if !ok {
		t.Fatal("no hooks section in settings.json")
	}
	sessionStart, ok := hooks["SessionStart"].([]interface{})
	if !ok {
		t.Fatal("no SessionStart in hooks")
	}
	found := false
	for _, entry := range sessionStart {
		if entryMap, ok := entry.(map[string]interface{}); ok {
			if entryHooks, ok := entryMap["hooks"].([]interface{}); ok {
				for _, h := range entryHooks {
					if hMap, ok := h.(map[string]interface{}); ok {
						if cmd, ok := hMap["command"].(string); ok && strings.Contains(cmd, "waggle") {
							found = true
						}
					}
				}
			}
		}
	}
	if !found {
		t.Error("waggle hook not found in SessionStart")
	}
}

func TestInstall_CorruptedSettingsJSON(t *testing.T) {
	tmpHome := t.TempDir()
	claudeDir := filepath.Join(tmpHome, ".claude")
	os.MkdirAll(claudeDir, 0755)
	settingsPath := filepath.Join(claudeDir, "settings.json")
	os.WriteFile(settingsPath, []byte("not json{{"), 0644)

	err := installClaudeCode(tmpHome)
	if err == nil {
		t.Fatal("expected install to fail with corrupted settings.json")
	}
	if !strings.Contains(err.Error(), "parse settings.json") {
		t.Fatalf("expected parse error, got: %v", err)
	}

	data, readErr := os.ReadFile(settingsPath)
	if readErr != nil {
		t.Fatalf("failed to read settings.json after failed install: %v", readErr)
	}
	if string(data) != "not json{{" {
		t.Fatalf("corrupted settings.json should be preserved, got %q", string(data))
	}

	for _, path := range []string{
		filepath.Join(tmpHome, ".claude", "hooks", "waggle-connect.sh"),
		filepath.Join(tmpHome, ".claude", "hooks", "waggle-heartbeat.sh"),
		filepath.Join(tmpHome, ".claude", "hooks", "waggle-push.js"),
		filepath.Join(tmpHome, ".claude", "skills", "waggle"),
		filepath.Join(tmpHome, ".waggle", "shell-hook.sh"),
	} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("install should not leave partial artifact %s after invalid settings.json, stat err: %v", path, statErr)
		}
	}
}

func TestUninstall_CorruptedSettingsJSONPreservesArtifacts(t *testing.T) {
	tmpHome := t.TempDir()
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	claudeDir := filepath.Join(tmpHome, ".claude")
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte("not json{{"), 0644); err != nil {
		t.Fatalf("write corrupted settings.json: %v", err)
	}

	err := uninstallClaudeCode(tmpHome)
	if err == nil {
		t.Fatal("expected uninstall to fail with corrupted settings.json")
	}
	if !strings.Contains(err.Error(), "parse settings.json") {
		t.Fatalf("expected parse error, got: %v", err)
	}

	data, readErr := os.ReadFile(settingsPath)
	if readErr != nil {
		t.Fatalf("failed to read settings.json after failed uninstall: %v", readErr)
	}
	if string(data) != "not json{{" {
		t.Fatalf("corrupted settings.json should be preserved, got %q", string(data))
	}

	for _, path := range []string{
		filepath.Join(tmpHome, ".claude", "hooks", "waggle-connect.sh"),
		filepath.Join(tmpHome, ".claude", "hooks", "waggle-heartbeat.sh"),
		filepath.Join(tmpHome, ".claude", "hooks", "waggle-push.js"),
		filepath.Join(tmpHome, ".claude", "skills", "waggle"),
	} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("uninstall should preserve artifact %s after invalid settings.json: %v", path, statErr)
		}
	}
}

func TestUninstall_EmptySettingsJSON(t *testing.T) {
	tmpHome := t.TempDir()
	claudeDir := filepath.Join(tmpHome, ".claude")
	os.MkdirAll(claudeDir, 0755)
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(""), 0644)

	err := uninstallClaudeCode(tmpHome)
	if err != nil {
		t.Fatalf("uninstall failed with empty settings.json: %v", err)
	}
}
