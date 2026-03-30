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
// Returns (issues, state) where:
// - StateNotInstalled: no fingerprint (waggle hook not registered), zero issues
// - StateHealthy: fingerprint present, all files present, zero issues
// - StateBroken: fingerprint present, some files missing, issues listed
func CheckClaudeCode(homeDir string) ([]HealthIssue, AdapterState) {
	var issues []HealthIssue
	claudeDir := filepath.Join(homeDir, ".claude")

	// Step 1: Check for fingerprint (waggle hook registration in settings.json)
	settingsPath := filepath.Join(claudeDir, "settings.json")
	settings, _ := readSettingsJSON(settingsPath)

	// Look for waggle hook in SessionStart
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

	// If no fingerprint, return StateNotInstalled immediately
	if !hookRegistered {
		return nil, StateNotInstalled
	}

	// Step 2: Fingerprint found — check if all files are present
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

	// Determine final state
	if len(issues) > 0 {
		return issues, StateBroken
	}
	return nil, StateHealthy
}

// CheckCodex checks the health of the Codex integration.
// Returns (issues, state) where:
// - StateNotInstalled: no fingerprint (WAGGLE-CODEX-BEGIN marker in AGENTS.md absent), zero issues
// - StateHealthy: fingerprint present, SKILL.md present, zero issues
// - StateBroken: fingerprint present, SKILL.md missing, issues listed
func CheckCodex(homeDir string) ([]HealthIssue, AdapterState) {
	var issues []HealthIssue
	codexDir := filepath.Join(homeDir, ".codex")

	// Step 1: Check for fingerprint (WAGGLE-CODEX-BEGIN marker in AGENTS.md)
	agentsPath := filepath.Join(codexDir, "AGENTS.md")
	data, err := os.ReadFile(agentsPath)
	if err != nil {
		// AGENTS.md doesn't exist or is unreadable — no fingerprint
		return nil, StateNotInstalled
	}

	// Check for waggle managed block marker
	content := string(data)
	if !strings.Contains(content, codexBlockBegin) {
		// No fingerprint
		return nil, StateNotInstalled
	}

	// Step 2: Fingerprint found — check managed block integrity.
	// The installer treats a missing end marker as corruption (see managed_block.go),
	// so health must detect the same state.
	if !strings.Contains(content, codexBlockEnd) {
		issues = append(issues, HealthIssue{
			Asset:   agentsPath,
			Problem: "managed block truncated (begin marker without end marker)",
			Repair:  "waggle install codex",
		})
	}

	// Step 3: Check if all files are present
	skillPath := filepath.Join(codexDir, "skills", "waggle-runtime", "SKILL.md")
	if _, err := os.Stat(skillPath); os.IsNotExist(err) {
		issues = append(issues, HealthIssue{
			Asset:   skillPath,
			Problem: "SKILL.md missing",
			Repair:  "waggle install codex",
		})
	}

	// Determine final state
	if len(issues) > 0 {
		return issues, StateBroken
	}
	return nil, StateHealthy
}

