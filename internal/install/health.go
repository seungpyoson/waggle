package install

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/seungpyoson/waggle/internal/fsutil"
)

// AdapterState represents the installation state of an adapter.
type AdapterState string

const (
	StateNotInstalled AdapterState = "not_installed" // Adapter was never installed
	StateHealthy      AdapterState = "healthy"       // Adapter is installed and all files present
	StateBroken       AdapterState = "broken"        // Adapter was installed but files are missing or inconsistent
)

var claudeCodeSkillFiles = []string{"waggle.md", "send.md", "inbox.md", "ack.md", "status.md", "claim.md", "done.md", "presence.md"}

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
	settings, settingsErr := readSettingsJSON(settingsPath)

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
	pushPath := filepath.Join(claudeDir, "hooks", "waggle-push.js")
	skillDir := filepath.Join(claudeDir, "skills", "waggle")

	hookExists := fileExists(hookPath)
	heartbeatExists := fileExists(heartbeatPath)
	pushExists := fileExists(pushPath)
	skillDirExists := fileExists(skillDir)
	anyFileExists := hookExists || heartbeatExists || pushExists || skillDirExists

	if settingsErr != nil {
		return []HealthIssue{{
			Asset:   settingsPath,
			Problem: "cannot parse settings.json: " + settingsErr.Error(),
			Repair:  "fix or remove invalid settings.json, then run " + repairCmd,
		}}, StateBroken
	}

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

	if !pushExists {
		issues = append(issues, HealthIssue{
			Asset:   pushPath,
			Problem: "waggle-push.js missing",
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
		for _, skill := range claudeCodeSkillFiles {
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

	if hookExists {
		appendEmbeddedFileIssue(&issues, hookPath, claudeCodeFiles, "claude-code/hook.sh", "waggle-connect.sh", repairCmd)
	}
	if heartbeatExists {
		appendEmbeddedFileIssue(&issues, heartbeatPath, claudeCodeFiles, "claude-code/heartbeat.sh", "waggle-heartbeat.sh", repairCmd)
	}
	if pushExists {
		appendEmbeddedFileIssue(&issues, pushPath, claudeCodeFiles, "claude-code/waggle-push.js", "waggle-push.js", repairCmd)
	}
	if skillDirExists {
		for _, skill := range claudeCodeSkillFiles {
			skillPath := filepath.Join(skillDir, skill)
			if fileExists(skillPath) {
				appendEmbeddedFileIssue(&issues, skillPath, claudeCodeFiles, "claude-code/skills/"+skill, "skill file "+skill, repairCmd)
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
//
// The managed block in AGENTS.md is validated with the same topology rules
// that upsertManagedBlock/removeManagedBlock enforce, so health never reports
// "healthy" for a file that mutation would reject.
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

	// Step 3: Validate marker topology before deriving state.
	// Any marker presence (begin OR end) means the file has waggle artifacts.
	// Topology must be validated first so that orphaned/corrupt markers are
	// caught as StateBroken rather than falling through to StateNotInstalled.
	hasAnyMarker := hasBeginMarker || hasEndMarker
	if hasAnyMarker {
		if topErr := validateMarkerTopology(string(data), codexBlockBegin, codexBlockEnd); topErr != nil {
			issues = append(issues, HealthIssue{
				Asset:   agentsPath,
				Problem: "managed block has invalid topology: " + topErr.Error(),
				Repair:  repairCmd,
			})
		}
	}

	// Step 4: Derive state from fingerprint × files matrix
	if !hasBeginMarker && !skillExists && len(issues) == 0 {
		// No begin marker, no skill file, and no topology issues — truly not installed.
		return nil, StateNotInstalled
	}

	if !hasBeginMarker && len(issues) == 0 {
		// Skill file exists but fingerprint is gone — orphaned install.
		// (If topology already flagged an issue, skip this to avoid redundant messaging.)
		issues = append(issues, HealthIssue{
			Asset:   agentsPath,
			Problem: "managed block missing from AGENTS.md",
			Repair:  repairCmd,
		})
	} else if hasBeginMarker && !hasEndMarker {
		// Fingerprint found but block is truncated — useful self-heal guidance.
		// (Topology doesn't flag begin-without-end since upsert/remove self-heal it.)
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
	} else {
		appendEmbeddedFileIssue(&issues, skillPath, codexFiles, "codex/skills/waggle-runtime/SKILL.md", "SKILL.md", repairCmd)
	}

	if hasBeginMarker && hasEndMarker && len(issues) == 0 {
		appendManagedBlockContentIssue(&issues, agentsPath, string(data), codexBlockBegin, codexBlockEnd, codexFiles, "codex/AGENTS-block.md", repairCmd)
	}

	if len(issues) > 0 {
		return issues, StateBroken
	}
	return nil, StateHealthy
}

// CheckGemini checks the health of the Gemini integration.
// Same topology-aware flow as CheckCodex: detect any marker, validate topology,
// then derive state. This ensures health never reports "not_installed" or "healthy"
// for a file that upsertManagedBlock would reject.
func CheckGemini(homeDir string) ([]HealthIssue, AdapterState) {
	const repairCmd = "waggle install gemini"
	var issues []HealthIssue
	geminiDir := filepath.Join(homeDir, ".gemini")
	geminiFilePath := filepath.Join(geminiDir, "GEMINI.md")

	// Step 1: Read file
	data, err := os.ReadFile(geminiFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, StateNotInstalled
		}
		return []HealthIssue{{
			Asset:   geminiFilePath,
			Problem: "failed to read GEMINI.md: " + err.Error(),
			Repair:  repairCmd,
		}}, StateBroken
	}

	content := string(data)
	hasBeginMarker := strings.Contains(content, geminiBlockBegin)
	hasEndMarker := strings.Contains(content, geminiBlockEnd)

	// Step 2: Validate marker topology before deriving state.
	// Any marker presence (begin OR end) means the file has waggle artifacts.
	hasAnyMarker := hasBeginMarker || hasEndMarker
	if hasAnyMarker {
		if topErr := validateMarkerTopology(content, geminiBlockBegin, geminiBlockEnd); topErr != nil {
			issues = append(issues, HealthIssue{
				Asset:   geminiFilePath,
				Problem: "managed block has invalid topology: " + topErr.Error(),
				Repair:  repairCmd,
			})
		}
	}

	// Step 3: Derive state
	if !hasAnyMarker {
		return nil, StateNotInstalled
	}

	if hasBeginMarker && !hasEndMarker && len(issues) == 0 {
		issues = append(issues, HealthIssue{
			Asset:   geminiFilePath,
			Problem: "managed block truncated (begin marker without end marker)",
			Repair:  repairCmd,
		})
	}

	if hasBeginMarker && hasEndMarker && len(issues) == 0 {
		appendManagedBlockContentIssue(&issues, geminiFilePath, content, geminiBlockBegin, geminiBlockEnd, geminiFiles, "gemini/GEMINI-block.md", repairCmd)
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

	// Validate ancestor path — symlinked parent directories are broken.
	rulesDir := filepath.Dir(rulesPath)
	if fsutil.HasAncestorSymlink(rulesPath, homeDir) {
		return []HealthIssue{{
			Asset:   rulesDir,
			Problem: "symlink in ancestor path: refusing to use path with ancestor symlink",
			Repair:  "rm the symlink and re-run " + repairCmd,
		}}, StateBroken
	}

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

// CheckAugment checks the health of the Augment integration.
// Same topology-aware flow as CheckGemini: detect any marker, validate topology,
// then derive state. This ensures health never reports "not_installed" or "healthy"
// for a file that upsertManagedBlock would reject.
func CheckAugment(homeDir string) ([]HealthIssue, AdapterState) {
	const repairCmd = "waggle install augment"
	var issues []HealthIssue
	skillPath := filepath.Join(homeDir, ".augment", "skills", "waggle.md")

	data, err := os.ReadFile(skillPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, StateNotInstalled
		}
		return []HealthIssue{{
			Asset:   skillPath,
			Problem: "cannot read skill file: " + err.Error(),
			Repair:  repairCmd,
		}}, StateBroken
	}

	content := string(data)
	hasBeginMarker := strings.Contains(content, augmentBlockBegin)
	hasEndMarker := strings.Contains(content, augmentBlockEnd)

	// Validate marker topology before deriving state.
	hasAnyMarker := hasBeginMarker || hasEndMarker
	if hasAnyMarker {
		if topErr := validateMarkerTopology(content, augmentBlockBegin, augmentBlockEnd); topErr != nil {
			issues = append(issues, HealthIssue{
				Asset:   skillPath,
				Problem: "managed block has invalid topology: " + topErr.Error(),
				Repair:  repairCmd,
			})
		}
	}

	if !hasAnyMarker {
		return nil, StateNotInstalled
	}

	if hasBeginMarker && !hasEndMarker && len(issues) == 0 {
		issues = append(issues, HealthIssue{
			Asset:   skillPath,
			Problem: "managed block truncated (begin marker without end marker)",
			Repair:  repairCmd,
		})
	}

	if hasBeginMarker && hasEndMarker && len(issues) == 0 {
		appendManagedBlockContentIssue(&issues, skillPath, content, augmentBlockBegin, augmentBlockEnd, augmentFiles, "augment/SKILL-block.md", repairCmd)
	}

	if len(issues) > 0 {
		return issues, StateBroken
	}
	return nil, StateHealthy
}

func CheckTool(homeDir, tool string) ([]HealthIssue, AdapterState, bool) {
	switch tool {
	case "claude-code":
		issues, state := CheckClaudeCode(homeDir)
		return issues, state, true
	case "codex":
		issues, state := CheckCodex(homeDir)
		return issues, state, true
	case "gemini":
		issues, state := CheckGemini(homeDir)
		return issues, state, true
	case "auggie":
		issues, state := CheckAuggie(homeDir)
		return issues, state, true
	case "augment":
		issues, state := CheckAugment(homeDir)
		return issues, state, true
	default:
		return nil, "", false
	}
}

type embeddedFileReader interface {
	ReadFile(name string) ([]byte, error)
}

func appendEmbeddedFileIssue(issues *[]HealthIssue, path string, files embeddedFileReader, embeddedPath, assetName, repairCmd string) {
	data, err := os.ReadFile(path)
	if err != nil {
		*issues = append(*issues, HealthIssue{
			Asset:   path,
			Problem: "unable to read " + assetName + ": " + err.Error(),
			Repair:  repairCmd,
		})
		return
	}

	canonical, err := files.ReadFile(embeddedPath)
	if err != nil {
		*issues = append(*issues, HealthIssue{
			Asset:   path,
			Problem: "unable to determine canonical " + assetName + " content: " + err.Error(),
			Repair:  repairCmd,
		})
		return
	}

	if !bytes.Equal(data, canonical) {
		*issues = append(*issues, HealthIssue{
			Asset:   path,
			Problem: assetName + " content does not match expected",
			Repair:  repairCmd,
		})
	}
}

func appendManagedBlockContentIssue(issues *[]HealthIssue, path, content, begin, end string, files embeddedFileReader, embeddedPath, repairCmd string) {
	body, ok := managedBlockBody(content, begin, end)
	if !ok {
		return
	}

	canonical, err := files.ReadFile(embeddedPath)
	if err != nil {
		*issues = append(*issues, HealthIssue{
			Asset:   path,
			Problem: "unable to determine canonical managed block content: " + err.Error(),
			Repair:  repairCmd,
		})
		return
	}

	if strings.TrimSpace(body) != strings.TrimSpace(string(canonical)) {
		*issues = append(*issues, HealthIssue{
			Asset:   path,
			Problem: "managed block content does not match expected",
			Repair:  repairCmd,
		})
	}
}

func managedBlockBody(content, begin, end string) (string, bool) {
	beginIdx := strings.Index(content, begin)
	if beginIdx < 0 {
		return "", false
	}
	bodyStart := beginIdx + len(begin)
	endRel := strings.Index(content[bodyStart:], end)
	if endRel < 0 {
		return "", false
	}
	return content[bodyStart : bodyStart+endRel], true
}

// fileExists returns true if a path exists on disk (file or directory).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
