package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckClaudeCode_Healthy(t *testing.T) {
	tmpHome := t.TempDir()

	// Install
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Check health
	issues := CheckClaudeCode(tmpHome)
	if len(issues) != 0 {
		t.Errorf("expected 0 issues, got %d: %+v", len(issues), issues)
	}
}

func TestCheckClaudeCode_MissingHook(t *testing.T) {
	tmpHome := t.TempDir()

	// Install
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Delete hook file
	hookPath := filepath.Join(tmpHome, ".claude", "hooks", "waggle-connect.sh")
	if err := os.Remove(hookPath); err != nil {
		t.Fatalf("failed to delete hook: %v", err)
	}

	// Check health
	issues := CheckClaudeCode(tmpHome)
	if len(issues) == 0 {
		t.Fatal("expected issues, got none")
	}

	// Verify we found the hook issue
	foundHookIssue := false
	for _, issue := range issues {
		if issue.Asset == hookPath && issue.Problem == "waggle-connect.sh missing" {
			foundHookIssue = true
			if issue.Repair != "waggle install claude-code" {
				t.Errorf("expected repair 'waggle install claude-code', got %q", issue.Repair)
			}
		}
	}
	if !foundHookIssue {
		t.Errorf("did not find hook issue in: %+v", issues)
	}
}

func TestCheckClaudeCode_MissingHeartbeat(t *testing.T) {
	tmpHome := t.TempDir()

	// Install
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Delete heartbeat file
	heartbeatPath := filepath.Join(tmpHome, ".claude", "hooks", "waggle-heartbeat.sh")
	if err := os.Remove(heartbeatPath); err != nil {
		t.Fatalf("failed to delete heartbeat: %v", err)
	}

	// Check health
	issues := CheckClaudeCode(tmpHome)
	if len(issues) == 0 {
		t.Fatal("expected issues, got none")
	}

	// Verify we found the heartbeat issue
	foundHeartbeatIssue := false
	for _, issue := range issues {
		if issue.Asset == heartbeatPath && issue.Problem == "waggle-heartbeat.sh missing" {
			foundHeartbeatIssue = true
		}
	}
	if !foundHeartbeatIssue {
		t.Errorf("did not find heartbeat issue in: %+v", issues)
	}
}

func TestCheckClaudeCode_MissingSkills(t *testing.T) {
	tmpHome := t.TempDir()

	// Install
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Delete skills directory
	skillDir := filepath.Join(tmpHome, ".claude", "skills", "waggle")
	if err := os.RemoveAll(skillDir); err != nil {
		t.Fatalf("failed to delete skills: %v", err)
	}

	// Check health
	issues := CheckClaudeCode(tmpHome)
	if len(issues) == 0 {
		t.Fatal("expected issues, got none")
	}

	// Verify we found the skills issue
	foundSkillsIssue := false
	for _, issue := range issues {
		if issue.Asset == skillDir && issue.Problem == "skills directory missing" {
			foundSkillsIssue = true
		}
	}
	if !foundSkillsIssue {
		t.Errorf("did not find skills issue in: %+v", issues)
	}
}

func TestCheckClaudeCode_BrokenSettings(t *testing.T) {
	tmpHome := t.TempDir()

	// Install
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Remove settings entry (uninstall removes it)
	if err := deregisterSessionStartHook(filepath.Join(tmpHome, ".claude")); err != nil {
		t.Fatalf("failed to deregister hook: %v", err)
	}

	// Check health
	issues := CheckClaudeCode(tmpHome)
	if len(issues) == 0 {
		t.Fatal("expected issues, got none")
	}

	// Verify we found the settings issue
	foundSettingsIssue := false
	settingsPath := filepath.Join(tmpHome, ".claude", "settings.json")
	for _, issue := range issues {
		if issue.Asset == settingsPath && issue.Problem == "waggle hook not registered in settings.json" {
			foundSettingsIssue = true
		}
	}
	if !foundSettingsIssue {
		t.Errorf("did not find settings issue in: %+v", issues)
	}
}

func TestCheckClaudeCode_RepairIdempotent(t *testing.T) {
	tmpHome := t.TempDir()

	// Install
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Delete hook to break it
	hookPath := filepath.Join(tmpHome, ".claude", "hooks", "waggle-connect.sh")
	if err := os.Remove(hookPath); err != nil {
		t.Fatalf("failed to delete hook: %v", err)
	}

	// Verify broken
	issues := CheckClaudeCode(tmpHome)
	if len(issues) == 0 {
		t.Fatal("expected issues after breaking, got none")
	}

	// Repair by reinstalling
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("repair failed: %v", err)
	}

	// Check health again — should be clean
	issues = CheckClaudeCode(tmpHome)
	if len(issues) != 0 {
		t.Errorf("expected 0 issues after repair, got %d: %+v", len(issues), issues)
	}
}

func TestCheckCodex_Healthy(t *testing.T) {
	tmpHome := t.TempDir()

	// Install
	if err := installCodex(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Check health
	issues := CheckCodex(tmpHome)
	if len(issues) != 0 {
		t.Errorf("expected 0 issues, got %d: %+v", len(issues), issues)
	}
}

func TestCheckCodex_MissingSkill(t *testing.T) {
	tmpHome := t.TempDir()

	// Install
	if err := installCodex(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Delete skill file
	skillPath := filepath.Join(tmpHome, ".codex", "skills", "waggle-runtime", "SKILL.md")
	if err := os.Remove(skillPath); err != nil {
		t.Fatalf("failed to delete skill: %v", err)
	}

	// Check health
	issues := CheckCodex(tmpHome)
	if len(issues) == 0 {
		t.Fatal("expected issues, got none")
	}

	// Verify we found the skill issue
	foundSkillIssue := false
	for _, issue := range issues {
		if issue.Asset == skillPath && issue.Problem == "SKILL.md missing" {
			foundSkillIssue = true
			if issue.Repair != "waggle install codex" {
				t.Errorf("expected repair 'waggle install codex', got %q", issue.Repair)
			}
		}
	}
	if !foundSkillIssue {
		t.Errorf("did not find skill issue in: %+v", issues)
	}
}

func TestCheckCodex_MissingBlock(t *testing.T) {
	tmpHome := t.TempDir()

	// Install
	if err := installCodex(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Remove AGENTS.md entirely
	agentsPath := filepath.Join(tmpHome, ".codex", "AGENTS.md")
	if err := os.Remove(agentsPath); err != nil {
		t.Fatalf("failed to delete AGENTS.md: %v", err)
	}

	// Check health
	issues := CheckCodex(tmpHome)
	if len(issues) == 0 {
		t.Fatal("expected issues, got none")
	}

	// Verify we found the AGENTS.md issue
	foundAgentsIssue := false
	for _, issue := range issues {
		if issue.Asset == agentsPath && issue.Problem == "AGENTS.md missing or unreadable" {
			foundAgentsIssue = true
		}
	}
	if !foundAgentsIssue {
		t.Errorf("did not find AGENTS.md issue in: %+v", issues)
	}
}

