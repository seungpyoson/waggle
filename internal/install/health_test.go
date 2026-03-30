package install

import (
	"os"
	"path/filepath"
	"testing"
)

// Test the three-state model for CheckClaudeCode

func TestCheckClaudeCode_NotInstalled(t *testing.T) {
	tmpHome := t.TempDir()

	// Fresh machine, no .claude directory at all
	issues, state := CheckClaudeCode(tmpHome)
	if state != StateNotInstalled {
		t.Errorf("expected StateNotInstalled, got %q", state)
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 issues for not_installed, got %d: %+v", len(issues), issues)
	}
}

func TestCheckClaudeCode_NotInstalled_NoFingerprint(t *testing.T) {
	tmpHome := t.TempDir()

	// .claude/settings.json exists but no waggle hook registered
	claudeDir := filepath.Join(tmpHome, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("failed to create .claude: %v", err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte("{}"), 0644); err != nil {
		t.Fatalf("failed to write settings.json: %v", err)
	}

	issues, state := CheckClaudeCode(tmpHome)
	if state != StateNotInstalled {
		t.Errorf("expected StateNotInstalled (no fingerprint), got %q", state)
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 issues for not_installed, got %d: %+v", len(issues), issues)
	}
}

func TestCheckClaudeCode_BrokenStaleReference(t *testing.T) {
	tmpHome := t.TempDir()

	// settings.json has a non-canonical waggle reference, no files on disk
	claudeDir := filepath.Join(tmpHome, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("failed to create .claude: %v", err)
	}
	staleSettings := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"bash /tmp/stale/waggle-connect.sh"}]}]}}`
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(staleSettings), 0644); err != nil {
		t.Fatalf("failed to write settings.json: %v", err)
	}

	issues, state := CheckClaudeCode(tmpHome)
	if state != StateBroken {
		t.Errorf("expected StateBroken (stale reference), got %q", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected issues for stale reference, got none")
	}

	foundStale := false
	for _, issue := range issues {
		if issue.Asset == filepath.Join(claudeDir, "settings.json") &&
			issue.Problem == "stale waggle hook reference in settings.json: bash /tmp/stale/waggle-connect.sh" {
			foundStale = true
			if issue.Repair != "waggle install claude-code" {
				t.Errorf("expected repair 'waggle install claude-code', got %q", issue.Repair)
			}
		}
	}
	if !foundStale {
		t.Errorf("did not find stale reference issue in: %+v", issues)
	}
}

func TestCheckClaudeCode_HealthyWithStaleReference(t *testing.T) {
	tmpHome := t.TempDir()

	// Install normally — canonical fingerprint + all files
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Rewrite settings.json to include both canonical and stale entries
	claudeDir := filepath.Join(tmpHome, ".claude")
	settingsPath := filepath.Join(claudeDir, "settings.json")
	bothHooks := `{"hooks":{"SessionStart":[` +
		`{"hooks":[{"type":"command","command":"` + waggleHookCommand + `"}]},` +
		`{"hooks":[{"type":"command","command":"bash /old/path/waggle-connect.sh"}]}` +
		`]}}`
	if err := os.WriteFile(settingsPath, []byte(bothHooks), 0644); err != nil {
		t.Fatalf("failed to write settings.json: %v", err)
	}

	// Canonical fingerprint + all files + stale reference → StateBroken (stale ref is an issue)
	issues, state := CheckClaudeCode(tmpHome)
	if state != StateBroken {
		t.Errorf("expected StateBroken (stale reference alongside canonical), got %q", state)
	}

	foundStale := false
	for _, issue := range issues {
		if issue.Problem == "stale waggle hook reference in settings.json: bash /old/path/waggle-connect.sh" {
			foundStale = true
		}
	}
	if !foundStale {
		t.Errorf("did not find stale reference issue in: %+v", issues)
	}
}

func TestCheckClaudeCode_Healthy(t *testing.T) {
	tmpHome := t.TempDir()

	// Install
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Check health
	issues, state := CheckClaudeCode(tmpHome)
	if state != StateHealthy {
		t.Errorf("expected StateHealthy, got %q", state)
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 issues, got %d: %+v", len(issues), issues)
	}
}

func TestCheckClaudeCode_BrokenMissingHook(t *testing.T) {
	tmpHome := t.TempDir()

	// Install
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Delete hook file (fingerprint remains via settings.json)
	hookPath := filepath.Join(tmpHome, ".claude", "hooks", "waggle-connect.sh")
	if err := os.Remove(hookPath); err != nil {
		t.Fatalf("failed to delete hook: %v", err)
	}

	// Check health
	issues, state := CheckClaudeCode(tmpHome)
	if state != StateBroken {
		t.Errorf("expected StateBroken, got %q", state)
	}
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

func TestCheckClaudeCode_BrokenMissingHeartbeat(t *testing.T) {
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
	issues, state := CheckClaudeCode(tmpHome)
	if state != StateBroken {
		t.Errorf("expected StateBroken, got %q", state)
	}
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

func TestCheckClaudeCode_BrokenMissingSkill(t *testing.T) {
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
	issues, state := CheckClaudeCode(tmpHome)
	if state != StateBroken {
		t.Errorf("expected StateBroken, got %q", state)
	}
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

func TestCheckClaudeCode_BrokenOrphanedInstall(t *testing.T) {
	tmpHome := t.TempDir()

	// Install to create files
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Remove hook registration from settings.json (fingerprint gone, files remain)
	claudeDir := filepath.Join(tmpHome, ".claude")
	if err := deregisterSessionStartHook(claudeDir); err != nil {
		t.Fatalf("failed to deregister hook: %v", err)
	}

	// Check health — files exist but fingerprint is gone: StateBroken (orphaned install)
	issues, state := CheckClaudeCode(tmpHome)
	if state != StateBroken {
		t.Errorf("expected StateBroken (orphaned install), got %q", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected issues for orphaned install, got none")
	}

	// Verify the registration issue is reported with repair guidance
	foundRegistration := false
	for _, issue := range issues {
		if issue.Problem == "hook registration missing from settings.json" {
			foundRegistration = true
			if issue.Repair != "waggle install claude-code" {
				t.Errorf("expected repair 'waggle install claude-code', got %q", issue.Repair)
			}
		}
	}
	if !foundRegistration {
		t.Errorf("did not find registration issue in: %+v", issues)
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
	issues, state := CheckClaudeCode(tmpHome)
	if state != StateBroken {
		t.Errorf("expected StateBroken, got %q", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected issues after breaking, got none")
	}

	// Repair by reinstalling
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("repair failed: %v", err)
	}

	// Check health again — should be clean
	issues, state = CheckClaudeCode(tmpHome)
	if state != StateHealthy {
		t.Errorf("expected StateHealthy after repair, got %q", state)
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 issues after repair, got %d: %+v", len(issues), issues)
	}
}

// Test the three-state model for CheckCodex

func TestCheckCodex_NotInstalled(t *testing.T) {
	tmpHome := t.TempDir()

	// Fresh machine, no .codex directory at all
	issues, state := CheckCodex(tmpHome)
	if state != StateNotInstalled {
		t.Errorf("expected StateNotInstalled, got %q", state)
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 issues for not_installed, got %d: %+v", len(issues), issues)
	}
}

func TestCheckCodex_NotInstalled_NoMarker(t *testing.T) {
	tmpHome := t.TempDir()

	// AGENTS.md exists but no WAGGLE-CODEX-BEGIN marker
	codexDir := filepath.Join(tmpHome, ".codex")
	if err := os.MkdirAll(codexDir, 0755); err != nil {
		t.Fatalf("failed to create .codex: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "AGENTS.md"), []byte("# Some content\nNo waggle marker"), 0644); err != nil {
		t.Fatalf("failed to write AGENTS.md: %v", err)
	}

	issues, state := CheckCodex(tmpHome)
	if state != StateNotInstalled {
		t.Errorf("expected StateNotInstalled (no marker), got %q", state)
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 issues for not_installed, got %d: %+v", len(issues), issues)
	}
}

func TestCheckCodex_Healthy(t *testing.T) {
	tmpHome := t.TempDir()

	// Install
	if err := installCodex(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Check health
	issues, state := CheckCodex(tmpHome)
	if state != StateHealthy {
		t.Errorf("expected StateHealthy, got %q", state)
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 issues, got %d: %+v", len(issues), issues)
	}
}

func TestCheckCodex_Broken(t *testing.T) {
	tmpHome := t.TempDir()

	// Install
	if err := installCodex(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Delete skill file (fingerprint in AGENTS.md remains)
	skillPath := filepath.Join(tmpHome, ".codex", "skills", "waggle-runtime", "SKILL.md")
	if err := os.Remove(skillPath); err != nil {
		t.Fatalf("failed to delete skill: %v", err)
	}

	// Check health
	issues, state := CheckCodex(tmpHome)
	if state != StateBroken {
		t.Errorf("expected StateBroken, got %q", state)
	}
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

func TestCheckCodex_BrokenTruncatedBlock(t *testing.T) {
	tmpHome := t.TempDir()

	// Install normally first
	if err := installCodex(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Truncate AGENTS.md: keep BEGIN marker but remove END marker
	agentsPath := filepath.Join(tmpHome, ".codex", "AGENTS.md")
	data, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	// Write only the begin marker with some content, no end marker
	truncated := "<!-- WAGGLE-CODEX-BEGIN -->\n## Waggle Runtime\nSome content here\n"
	if err := os.WriteFile(agentsPath, []byte(truncated), 0644); err != nil {
		t.Fatalf("write truncated AGENTS.md: %v", err)
	}
	_ = data // original data not needed

	// Check health — should detect truncated block
	issues, state := CheckCodex(tmpHome)
	if state != StateBroken {
		t.Errorf("expected StateBroken for truncated block, got %q", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected issues for truncated block, got none")
	}

	// Verify we found the truncation issue
	foundTruncation := false
	for _, issue := range issues {
		if issue.Asset == agentsPath && issue.Problem == "managed block truncated (begin marker without end marker)" {
			foundTruncation = true
			if issue.Repair != "waggle install codex" {
				t.Errorf("expected repair 'waggle install codex', got %q", issue.Repair)
			}
		}
	}
	if !foundTruncation {
		t.Errorf("did not find truncation issue in: %+v", issues)
	}
}

func TestCheckCodex_BrokenOrphanedInstall(t *testing.T) {
	tmpHome := t.TempDir()

	// Install
	if err := installCodex(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Remove AGENTS.md entirely (fingerprint marker now gone, SKILL.md remains)
	agentsPath := filepath.Join(tmpHome, ".codex", "AGENTS.md")
	if err := os.Remove(agentsPath); err != nil {
		t.Fatalf("failed to delete AGENTS.md: %v", err)
	}

	// Check health — AGENTS.md gone but skill file exists: StateBroken (orphaned install)
	issues, state := CheckCodex(tmpHome)
	if state != StateBroken {
		t.Errorf("expected StateBroken (orphaned install), got %q", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected issues for orphaned install, got none")
	}

	// Verify the managed block issue is reported with repair guidance
	foundBlock := false
	for _, issue := range issues {
		if issue.Problem == "managed block missing from AGENTS.md" {
			foundBlock = true
			if issue.Repair != "waggle install codex" {
				t.Errorf("expected repair 'waggle install codex', got %q", issue.Repair)
			}
		}
	}
	if !foundBlock {
		t.Errorf("did not find managed block issue in: %+v", issues)
	}
}

func TestCheckAuggie_NotInstalled(t *testing.T) {
	tmpHome := t.TempDir()

	issues, state := CheckAuggie(tmpHome)
	if state != StateNotInstalled {
		t.Errorf("expected StateNotInstalled, got %q", state)
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 issues for not_installed, got %d: %+v", len(issues), issues)
	}
}

func TestCheckAuggie_NotInstalled_NoMarkers(t *testing.T) {
	tmpHome := t.TempDir()
	rulesDir := filepath.Join(tmpHome, ".augment", "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatalf("failed to create rules dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "waggle.md"), []byte("# Existing rule\nNo managed block here\n"), 0644); err != nil {
		t.Fatalf("failed to write waggle.md: %v", err)
	}

	issues, state := CheckAuggie(tmpHome)
	if state != StateNotInstalled {
		t.Errorf("expected StateNotInstalled, got %q", state)
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 issues for not_installed, got %d: %+v", len(issues), issues)
	}
}

func TestCheckAuggie_BrokenTruncatedBlock(t *testing.T) {
	tmpHome := t.TempDir()
	rulesDir := filepath.Join(tmpHome, ".augment", "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatalf("failed to create rules dir: %v", err)
	}
	rulesPath := filepath.Join(rulesDir, "waggle.md")
	truncated := auggieBlockBegin + "\n## Waggle Runtime\n"
	if err := os.WriteFile(rulesPath, []byte(truncated), 0644); err != nil {
		t.Fatalf("failed to write truncated waggle.md: %v", err)
	}

	issues, state := CheckAuggie(tmpHome)
	if state != StateBroken {
		t.Errorf("expected StateBroken, got %q", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected issues for truncated block, got none")
	}

	foundTruncation := false
	for _, issue := range issues {
		if issue.Asset == rulesPath && issue.Problem == "managed block truncated (begin marker without end marker)" {
			foundTruncation = true
			if issue.Repair != "waggle install auggie" {
				t.Errorf("expected repair 'waggle install auggie', got %q", issue.Repair)
			}
		}
	}
	if !foundTruncation {
		t.Errorf("did not find truncation issue in: %+v", issues)
	}
}

func TestCheckAuggie_Healthy(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	issues, state := CheckAuggie(tmpHome)
	if state != StateHealthy {
		t.Errorf("expected StateHealthy, got %q", state)
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 issues, got %d: %+v", len(issues), issues)
	}
}

func TestCheckAuggie_RepairIdempotent(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	rulesPath := filepath.Join(tmpHome, ".augment", "rules", "waggle.md")
	if err := os.WriteFile(rulesPath, []byte(auggieBlockBegin+"\n## Waggle Runtime\n"), 0644); err != nil {
		t.Fatalf("failed to break waggle.md: %v", err)
	}

	issues, state := CheckAuggie(tmpHome)
	if state != StateBroken {
		t.Errorf("expected StateBroken, got %q", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected issues after breaking, got none")
	}

	if err := installAuggie(tmpHome); err != nil {
		t.Fatalf("repair failed: %v", err)
	}

	issues, state = CheckAuggie(tmpHome)
	if state != StateHealthy {
		t.Errorf("expected StateHealthy after repair, got %q", state)
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 issues after repair, got %d: %+v", len(issues), issues)
	}
}
