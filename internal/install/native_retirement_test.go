package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNativeInstallRetiresOnlyOwnedDeliveryHooks(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, ".claude")
	if err := os.MkdirAll(filepath.Join(directory, "hooks"), 0700); err != nil {
		t.Fatal(err)
	}
	unrelated := map[string]interface{}{"type": "command", "command": "user-owned-hook", "timeout": float64(7)}
	settings := map[string]interface{}{"permissions": map[string]interface{}{"defaultMode": "plan"}, "hooks": map[string]interface{}{
		"PreToolUse": []interface{}{map[string]interface{}{"matcher": "Bash", "hooks": []interface{}{map[string]interface{}{"type": "command", "command": wagglePushCommand}, unrelated}}},
	}}
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "settings.json")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "hooks", "waggle-push.js"), []byte("retired"), 0600); err != nil {
		t.Fatal(err)
	}
	rc := filepath.Join(home, ".zshenv")
	if err = os.WriteFile(rc, []byte("user-before\n"+shellHookBegin+"\nretired shell code\n"+shellHookEnd+"\nuser-after\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = installClaudeCode(home); err != nil {
		t.Fatal(err)
	}
	current, err := readSettingsJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := current["hooks"].(map[string]interface{})["PreToolUse"].([]interface{})
	if len(entries) != 1 {
		t.Fatal("retirement removed a mixed user-owned entry")
	}
	entry := entries[0].(map[string]interface{})
	if entry["matcher"] != "Bash" || !reflect.DeepEqual(entry["hooks"], []interface{}{unrelated}) {
		t.Fatal("retirement changed unrelated hook metadata or command")
	}
	if !reflect.DeepEqual(current["permissions"], settings["permissions"]) {
		t.Fatal("native installation changed permissions")
	}
	settingsInfo, err := os.Stat(path)
	if err != nil || settingsInfo.Mode().Perm() != 0600 {
		t.Fatal("native installation changed settings file permissions")
	}
	if _, err = os.Lstat(filepath.Join(directory, "hooks", "waggle-push.js")); !os.IsNotExist(err) {
		t.Fatal("retired push consumer still exists")
	}
	data, err = os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "user-before\nuser-after\n" {
		t.Fatal("shell retirement changed unrelated contents")
	}
	info, err := os.Stat(rc)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("shell retirement changed file permissions")
	}
}

func TestHookMutationRejectsMalformedSettingsWithoutReplacingThem(t *testing.T) {
	directory := filepath.Join(t.TempDir(), ".claude")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "settings.json")
	original := []byte(`{"hooks":{"SessionStart":"malformed"},"user":"preserve"}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := registerSessionStartHook(directory); err == nil {
		t.Fatal("malformed hooks silently replaced")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatal("invalid configuration was mutated")
	}
}
