package install

import (
	"os"
	"path/filepath"
	"strings"
)

// HealthIssue represents a problem with an adapter installation.
type HealthIssue struct {
	Asset   string // The file or resource that has the problem
	Problem string // Human-readable description of the problem
	Repair  string // Command to fix the problem
}

// CheckClaudeCode checks the health of the Claude Code integration.
// Returns a list of issues found (empty slice = healthy).
func CheckClaudeCode(homeDir string) []HealthIssue {
	var issues []HealthIssue
	claudeDir := filepath.Join(homeDir, ".claude")

	// Check hook file
	hookPath := filepath.Join(claudeDir, "hooks", "waggle-connect.sh")
	if _, err := os.Stat(hookPath); os.IsNotExist(err) {
		issues = append(issues, HealthIssue{
			Asset:   hookPath,
			Problem: "waggle-connect.sh missing",
			Repair:  "waggle install claude-code",
		})
	}

	// Check heartbeat file
	heartbeatPath := filepath.Join(claudeDir, "hooks", "waggle-heartbeat.sh")
	if _, err := os.Stat(heartbeatPath); os.IsNotExist(err) {
		issues = append(issues, HealthIssue{
			Asset:   heartbeatPath,
			Problem: "waggle-heartbeat.sh missing",
			Repair:  "waggle install claude-code",
		})
	}

	// Check skills directory and files
	skillDir := filepath.Join(claudeDir, "skills", "waggle")
	if _, err := os.Stat(skillDir); os.IsNotExist(err) {
		issues = append(issues, HealthIssue{
			Asset:   skillDir,
			Problem: "skills directory missing",
			Repair:  "waggle install claude-code",
		})
	} else {
		// Check for expected skill files
		expectedSkills := []string{"waggle.md", "send.md", "inbox.md", "ack.md", "status.md", "claim.md", "done.md", "presence.md"}
		for _, skill := range expectedSkills {
			skillPath := filepath.Join(skillDir, skill)
			if _, err := os.Stat(skillPath); os.IsNotExist(err) {
				issues = append(issues, HealthIssue{
					Asset:   skillPath,
					Problem: "skill file " + skill + " missing",
					Repair:  "waggle install claude-code",
				})
				break // Report only the first missing skill to avoid noise
			}
		}
	}

	// Check settings.json for SessionStart hook registration
	settingsPath := filepath.Join(claudeDir, "settings.json")
	settings, err := readSettingsJSON(settingsPath)
	if err != nil {
		// settings.json doesn't exist or is unreadable — adapter not installed
		issues = append(issues, HealthIssue{
			Asset:   settingsPath,
			Problem: "settings.json missing or unreadable",
			Repair:  "waggle install claude-code",
		})
	} else {
		// Check if waggle hook is registered
		hookRegistered := false
		if hooks, ok := settings["hooks"].(map[string]interface{}); ok {
			if sessionStart, ok := hooks["SessionStart"].([]interface{}); ok {
				for _, entry := range sessionStart {
					if entryMap, ok := entry.(map[string]interface{}); ok {
						if entryHooks, ok := entryMap["hooks"].([]interface{}); ok {
							for _, h := range entryHooks {
								if hMap, ok := h.(map[string]interface{}); ok {
									if cmd, ok := hMap["command"].(string); ok {
										if strings.Contains(cmd, "waggle-connect.sh") {
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

		if !hookRegistered {
			issues = append(issues, HealthIssue{
				Asset:   settingsPath,
				Problem: "waggle hook not registered in settings.json",
				Repair:  "waggle install claude-code",
			})
		} else {
			// Hook is registered, but verify the referenced file exists
			if _, err := os.Stat(hookPath); os.IsNotExist(err) {
				issues = append(issues, HealthIssue{
					Asset:   hookPath,
					Problem: "settings.json references waggle-connect.sh but file is missing",
					Repair:  "waggle install claude-code",
				})
			}
		}
	}

	return issues
}

// CheckCodex checks the health of the Codex integration.
// Returns a list of issues found (empty slice = healthy).
func CheckCodex(homeDir string) []HealthIssue {
	var issues []HealthIssue
	codexDir := filepath.Join(homeDir, ".codex")

	// Check skill file
	skillPath := filepath.Join(codexDir, "skills", "waggle-runtime", "SKILL.md")
	if _, err := os.Stat(skillPath); os.IsNotExist(err) {
		issues = append(issues, HealthIssue{
			Asset:   skillPath,
			Problem: "SKILL.md missing",
			Repair:  "waggle install codex",
		})
	}

	// Check AGENTS.md for managed block
	agentsPath := filepath.Join(codexDir, "AGENTS.md")
	data, err := os.ReadFile(agentsPath)
	if err != nil {
		issues = append(issues, HealthIssue{
			Asset:   agentsPath,
			Problem: "AGENTS.md missing or unreadable",
			Repair:  "waggle install codex",
		})
	} else {
		// Check for waggle managed block markers
		if !strings.Contains(string(data), codexBlockBegin) {
			issues = append(issues, HealthIssue{
				Asset:   agentsPath,
				Problem: "waggle managed block missing from AGENTS.md",
				Repair:  "waggle install codex",
			})
		}
	}

	return issues
}

