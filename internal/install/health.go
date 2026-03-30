package install

import (
	"fmt"
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
	// Only the exact canonical command counts as a waggle fingerprint.
	// Non-canonical references (e.g., user-edited paths) are detected separately
	// as stale references and surfaced with repair guidance.
	settingsPath := filepath.Join(claudeDir, "settings.json")
	settings, _ := readSettingsJSON(settingsPath)

	hookRegistered := false
	var staleRef string // non-canonical command containing "waggle-connect.sh"
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
									} else if strings.Contains(cmd, "waggle-connect.sh") {
										staleRef = cmd
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
		if staleRef != "" {
			// No canonical fingerprint, no files, but a stale waggle reference
			// exists in settings.json — surface it with repair guidance
			return []HealthIssue{{
				Asset:   settingsPath,
				Problem: "stale waggle hook reference in settings.json: " + staleRef,
				Repair:  repairCmd,
			}}, StateBroken
		}
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

	if staleRef != "" {
		// Canonical fingerprint exists, but there's also a stale reference
		issues = append(issues, HealthIssue{
			Asset:   settingsPath,
			Problem: "stale waggle hook reference in settings.json: " + staleRef,
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

// CheckAuggie checks the health of the Auggie integration.
// Auggie reads all files in ~/.augment/rules/, so waggle owns waggle.md entirely.
// Health is determined by whether the file exists and matches canonical content.
func CheckAuggie(homeDir string) ([]HealthIssue, AdapterState) {
	rulesPath := filepath.Join(homeDir, ".augment", "rules", "waggle.md")
	const repairCmd = "waggle install auggie"

	// Reject symlinks and non-regular files to maintain owned-file integrity
	if info, err := os.Lstat(rulesPath); err == nil && info.Mode()&os.ModeType != 0 {
		return []HealthIssue{{
			Asset:   rulesPath,
			Problem: fmt.Sprintf("not a regular file (mode: %s); remove it manually", info.Mode().Type()),
			Repair:  "rm " + rulesPath + " && " + repairCmd,
		}}, StateBroken
	}

	data, err := os.ReadFile(rulesPath)
	if os.IsNotExist(err) {
		return nil, StateNotInstalled
	}
	if err != nil {
		return []HealthIssue{{
			Asset:   rulesPath,
			Problem: "unable to read rules file: " + err.Error(),
			Repair:  repairCmd,
		}}, StateBroken
	}

	canonical, err := canonicalAuggieFile()
	if err != nil {
		return []HealthIssue{{
			Asset:   rulesPath,
			Problem: "unable to determine canonical content: " + err.Error(),
			Repair:  repairCmd,
		}}, StateBroken
	}

	if string(data) != canonical {
		return []HealthIssue{{
			Asset:   rulesPath,
			Problem: "content does not match expected; may need update",
			Repair:  repairCmd,
		}}, StateBroken
	}

	return nil, StateHealthy
}

// fileExists returns true if a path exists on disk (file or directory).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
