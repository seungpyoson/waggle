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
// Topology-aware managed block validation tests for CheckCodex.
// These verify that health matches the mutation contract: if validateMarkerTopology
// would reject the file, health must report StateBroken (not StateHealthy).

func TestCheckCodex_BrokenDuplicateBeginMarkers(t *testing.T) {
	tmpHome := t.TempDir()

	// Install normally first to get skill files in place
	if err := installCodex(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Corrupt AGENTS.md: duplicate begin markers
	agentsPath := filepath.Join(tmpHome, ".codex", "AGENTS.md")
	corrupted := codexBlockBegin + "\nsome content\n" + codexBlockEnd + "\n" + codexBlockBegin + "\nmore content\n" + codexBlockEnd + "\n"
	if err := os.WriteFile(agentsPath, []byte(corrupted), 0644); err != nil {
		t.Fatalf("write corrupted AGENTS.md: %v", err)
	}

	issues, state := CheckCodex(tmpHome)
	if state != StateBroken {
		t.Errorf("expected StateBroken for duplicate begin markers, got %q", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected issues for duplicate begin markers, got none")
	}

	foundTopology := false
	for _, issue := range issues {
		if issue.Asset == agentsPath && issue.Problem != "" {
			if issue.Problem == "managed block has invalid topology: duplicate begin markers (2 found); refusing to mutate" {
				foundTopology = true
			}
		}
	}
	if !foundTopology {
		t.Errorf("did not find topology issue for duplicate begin markers in: %+v", issues)
	}
}

func TestCheckCodex_BrokenReversedMarkers(t *testing.T) {
	tmpHome := t.TempDir()

	// Install normally first to get skill files in place
	if err := installCodex(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Corrupt AGENTS.md: end marker before begin marker
	agentsPath := filepath.Join(tmpHome, ".codex", "AGENTS.md")
	corrupted := codexBlockEnd + "\nsome content\n" + codexBlockBegin + "\n"
	if err := os.WriteFile(agentsPath, []byte(corrupted), 0644); err != nil {
		t.Fatalf("write corrupted AGENTS.md: %v", err)
	}

	issues, state := CheckCodex(tmpHome)
	if state != StateBroken {
		t.Errorf("expected StateBroken for reversed markers, got %q", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected issues for reversed markers, got none")
	}

	foundTopology := false
	for _, issue := range issues {
		if issue.Asset == agentsPath && issue.Problem != "" {
			if issue.Problem == "managed block has invalid topology: end marker appears before begin marker; refusing to mutate" {
				foundTopology = true
			}
		}
	}
	if !foundTopology {
		t.Errorf("did not find topology issue for reversed markers in: %+v", issues)
	}
}

func TestCheckCodex_BrokenDuplicateEndMarkers(t *testing.T) {
	tmpHome := t.TempDir()

	// Install normally first to get skill files in place
	if err := installCodex(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Corrupt AGENTS.md: duplicate end markers (both begin + end present, plus extra end)
	agentsPath := filepath.Join(tmpHome, ".codex", "AGENTS.md")
	corrupted := codexBlockBegin + "\nsome content\n" + codexBlockEnd + "\nextra\n" + codexBlockEnd + "\n"
	if err := os.WriteFile(agentsPath, []byte(corrupted), 0644); err != nil {
		t.Fatalf("write corrupted AGENTS.md: %v", err)
	}

	issues, state := CheckCodex(tmpHome)
	if state != StateBroken {
		t.Errorf("expected StateBroken for duplicate end markers, got %q", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected issues for duplicate end markers, got none")
	}

	foundTopology := false
	for _, issue := range issues {
		if issue.Asset == agentsPath && issue.Problem != "" {
			if issue.Problem == "managed block has invalid topology: duplicate end markers (2 found); refusing to mutate" {
				foundTopology = true
			}
		}
	}
	if !foundTopology {
		t.Errorf("did not find topology issue for duplicate end markers in: %+v", issues)
	}
}

func TestCheckCodex_BrokenOrphanedEndOnly(t *testing.T) {
	tmpHome := t.TempDir()

	// AGENTS.md with ONLY an end marker (no begin marker), no skill file.
	// Previously this fell through to StateNotInstalled because the fast path
	// checked !hasBeginMarker && !skillExists without validating topology.
	// Now topology validation runs first and catches the orphaned end marker.
	codexDir := filepath.Join(tmpHome, ".codex")
	if err := os.MkdirAll(codexDir, 0755); err != nil {
		t.Fatalf("failed to create .codex: %v", err)
	}
	agentsPath := filepath.Join(codexDir, "AGENTS.md")
	endOnly := "# My AGENTS.md\nSome user content\n" + codexBlockEnd + "\n"
	if err := os.WriteFile(agentsPath, []byte(endOnly), 0644); err != nil {
		t.Fatalf("write end-only AGENTS.md: %v", err)
	}

	issues, state := CheckCodex(tmpHome)
	if state != StateBroken {
		t.Errorf("expected StateBroken for orphaned end marker, got %q", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected issues for orphaned end marker, got none")
	}

	foundTopology := false
	for _, issue := range issues {
		if issue.Asset == agentsPath &&
			issue.Problem == "managed block has invalid topology: orphaned end marker without begin marker; refusing to mutate" {
			foundTopology = true
			if issue.Repair != "waggle install codex" {
				t.Errorf("expected repair 'waggle install codex', got %q", issue.Repair)
			}
		}
	}
	if !foundTopology {
		t.Errorf("did not find topology issue for orphaned end marker in: %+v", issues)
	}
}

func TestCheckCodex_BrokenOrphanedEndOnlyWithSkill(t *testing.T) {
	tmpHome := t.TempDir()

	// Install normally to get skill files, then corrupt AGENTS.md to end-only.
	// This tests the case where skill exists but AGENTS.md has only an end marker.
	if err := installCodex(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	agentsPath := filepath.Join(tmpHome, ".codex", "AGENTS.md")
	endOnly := "# My AGENTS.md\n" + codexBlockEnd + "\n"
	if err := os.WriteFile(agentsPath, []byte(endOnly), 0644); err != nil {
		t.Fatalf("write end-only AGENTS.md: %v", err)
	}

	issues, state := CheckCodex(tmpHome)
	if state != StateBroken {
		t.Errorf("expected StateBroken for orphaned end marker (with skill), got %q", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected issues for orphaned end marker, got none")
	}

	foundTopology := false
	for _, issue := range issues {
		if issue.Asset == agentsPath &&
			issue.Problem == "managed block has invalid topology: orphaned end marker without begin marker; refusing to mutate" {
			foundTopology = true
		}
	}
	if !foundTopology {
		t.Errorf("did not find topology issue for orphaned end marker in: %+v", issues)
	}
}

func TestCheckCodex_BrokenBeginMarkerNotAtLineStart(t *testing.T) {
	tmpHome := t.TempDir()

	// Install normally first to get skill files in place
	if err := installCodex(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Corrupt AGENTS.md: begin marker not at start of line
	agentsPath := filepath.Join(tmpHome, ".codex", "AGENTS.md")
	corrupted := "prefix " + codexBlockBegin + "\nsome content\n" + codexBlockEnd + "\n"
	if err := os.WriteFile(agentsPath, []byte(corrupted), 0644); err != nil {
		t.Fatalf("write corrupted AGENTS.md: %v", err)
	}

	issues, state := CheckCodex(tmpHome)
	if state != StateBroken {
		t.Errorf("expected StateBroken for begin marker not at line start, got %q", state)
	}
	if len(issues) == 0 {
		t.Fatal("expected issues for begin marker not at line start, got none")
	}

	foundTopology := false
	for _, issue := range issues {
		if issue.Asset == agentsPath && issue.Problem != "" {
			if issue.Problem == "managed block has invalid topology: begin marker not at start of line; refusing to mutate" {
				foundTopology = true
			}
		}
	}
	if !foundTopology {
		t.Errorf("did not find topology issue for begin marker not at line start in: %+v", issues)
	}
}

func TestCheckCodex_HealthyValidTopology(t *testing.T) {
	tmpHome := t.TempDir()

	// Install normally — this produces a valid managed block
	if err := installCodex(tmpHome); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Verify health reports healthy (topology is valid, content matches)
	issues, state := CheckCodex(tmpHome)
	if state != StateHealthy {
		t.Errorf("expected StateHealthy for valid topology, got %q", state)
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 issues for valid topology, got %d: %+v", len(issues), issues)
	}
}

// Auggie health tests live in auggie_test.go (owned-file model).
