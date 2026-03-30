package install

import (
	"os"
	"path/filepath"
	"strings"
)

// AdapterState represents the installation state of an adapter.
type AdapterState string

const (
	StateNotInstalled AdapterState = "not_installed" // Adapter was never installed
	StateHealthy      AdapterState = "healthy"       // Adapter is installed and all files present
	StateBroken       AdapterState = "broken"        // Adapter was installed but files are missing or inconsistent
)

// HealthIssue represents a problem with an adapter installation.
type HealthIssue struct {
	Asset   string // The file or resource that has the problem
	Problem string // Human-readable description of the problem
	Repair  string // Command to fix the problem
}

// CheckClaudeCode checks the health of the Claude Code integration.
// Evaluates fingerprint (hook registration) and file presence independently:
//
//	| Fingerprint | Files | → State        |
//	|-------------|-------|----------------|
//	| ✓           | ✓     | Healthy        |
//	| ✓           | ✗     | Broken         |
//	| ✗           | ✓     | Broken         |
//	| ✗           | ✗     | NotInstalled   |
func CheckClaudeCode(homeDir string) ([]HealthIssue, AdapterState) {
	var issues []HealthIssue
	claudeDir := filepath.Join(homeDir, ".claude")
	const repairCmd = "waggle install claude-code"

	// Step 1: Check for fingerprint (waggle hook registration in settings.json).
	// Uses exact canonical command match — same string the installer writes.
	settingsPath := filepath.Join(claudeDir, "settings.json")
	settings, _ := readSettingsJSON(settingsPath)

	hookRegistered := false
	if hooks, ok := settings["hooks"].(map[string]interface{}); ok {
		if sessionStart, ok := hooks["SessionStart"].([]interface{}); ok {
			for _, entry := range sessionStart {
				if entryMap, ok := entry.(map[string]interface{}); ok {
					if entryHooks, ok := entryMap["hooks"].([]interface{}); ok {
						for _, h := range entryHooks {
							if hMap, ok := h.(map[string]interface{}); ok {
								if cmd, ok := hMap["command"].(string); ok {
									if cmd == waggleHookCommand {
										hookRegistered = true
										break
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// Step 2: Check if waggle files are present on disk
	hookPath := filepath.Join(claudeDir, "hooks", "waggle-connect.sh")
	heartbeatPath := filepath.Join(claudeDir, "hooks", "waggle-heartbeat.sh")
	skillDir := filepath.Join(claudeDir, "skills", "waggle")

	hookExists := fileExists(hookPath)
	heartbeatExists := fileExists(heartbeatPath)
	skillDirExists := fileExists(skillDir)
	anyFileExists := hookExists || heartbeatExists || skillDirExists

	// Step 3: Derive state from fingerprint × files matrix
	if !hookRegistered && !anyFileExists {
		return nil, StateNotInstalled
	}

	if !hookRegistered {
		// Files exist but fingerprint is gone — orphaned install
		issues = append(issues, HealthIssue{
			Asset:   settingsPath,
			Problem: "hook registration missing from settings.json",
			Repair:  repairCmd,
		})
	}

	if !hookExists {
		issues = append(issues, HealthIssue{
			Asset:   hookPath,
			Problem: "waggle-connect.sh missing",
			Repair:  repairCmd,
		})
	}

	if !heartbeatExists {
		issues = append(issues, HealthIssue{
			Asset:   heartbeatPath,
			Problem: "waggle-heartbeat.sh missing",
			Repair:  repairCmd,
		})
	}

	if !skillDirExists {
		issues = append(issues, HealthIssue{
			Asset:   skillDir,
			Problem: "skills directory missing",
			Repair:  repairCmd,
		})
	} else {
		expectedSkills := []string{"waggle.md", "send.md", "inbox.md", "ack.md", "status.md", "claim.md", "done.md", "presence.md"}
		for _, skill := range expectedSkills {
			skillPath := filepath.Join(skillDir, skill)
			if !fileExists(skillPath) {
				issues = append(issues, HealthIssue{
					Asset:   skillPath,
					Problem: "skill file " + skill + " missing",
					Repair:  repairCmd,
				})
				break // Report only the first missing skill to avoid noise
			}
		}
	}

	if len(issues) > 0 {
		return issues, StateBroken
	}
	return nil, StateHealthy
}

// CheckCodex checks the health of the Codex integration.
// Same fingerprint × files matrix as CheckClaudeCode.
func CheckCodex(homeDir string) ([]HealthIssue, AdapterState) {
	var issues []HealthIssue
	codexDir := filepath.Join(homeDir, ".codex")
	const repairCmd = "waggle install codex"

	// Step 1: Check for fingerprint (WAGGLE-CODEX-BEGIN marker in AGENTS.md)
	agentsPath := filepath.Join(codexDir, "AGENTS.md")
	data, err := os.ReadFile(agentsPath)

	hasBeginMarker := err == nil && strings.Contains(string(data), codexBlockBegin)
	hasEndMarker := err == nil && strings.Contains(string(data), codexBlockEnd)

	// Step 2: Check if waggle files are present on disk
	skillPath := filepath.Join(codexDir, "skills", "waggle-runtime", "SKILL.md")
	skillExists := fileExists(skillPath)

	// Step 3: Derive state from fingerprint × files matrix
	if !hasBeginMarker && !skillExists {
		return nil, StateNotInstalled
	}

	if !hasBeginMarker {
		// Skill file exists but fingerprint is gone — orphaned install
		issues = append(issues, HealthIssue{
			Asset:   agentsPath,
			Problem: "managed block missing from AGENTS.md",
			Repair:  repairCmd,
		})
	} else if !hasEndMarker {
		// Fingerprint found but block is truncated
		issues = append(issues, HealthIssue{
			Asset:   agentsPath,
			Problem: "managed block truncated (begin marker without end marker)",
			Repair:  repairCmd,
		})
	}

	if !skillExists {
		issues = append(issues, HealthIssue{
			Asset:   skillPath,
			Problem: "SKILL.md missing",
			Repair:  repairCmd,
		})
	}

	if len(issues) > 0 {
		return issues, StateBroken
	}
	return nil, StateHealthy
}

// fileExists returns true if a path exists on disk (file or directory).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

