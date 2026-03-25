package spawn

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestLoadAgentConfig_Default — missing file creates default config with claude/codex/gemini
func TestLoadAgentConfig_Default(t *testing.T) {
	tmpDir := t.TempDir()

	cfg, err := LoadAgentConfig(tmpDir)
	if err != nil {
		t.Fatalf("LoadAgentConfig() error = %v, want nil", err)
	}

	if cfg == nil {
		t.Fatal("LoadAgentConfig() returned nil config")
	}

	// Verify default agent is set
	if cfg.Default == "" {
		t.Error("default agent should be set")
	}

	// Verify claude, codex, gemini agents exist
	for _, name := range []string{"claude", "codex", "gemini"} {
		if _, ok := cfg.Agents[name]; !ok {
			t.Errorf("default config should include %q agent", name)
		}
	}
}

// TestLoadAgentConfig_Custom — write custom JSON, load, verify
func TestLoadAgentConfig_Custom(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "agents.json")

	customCfg := AgentConfig{
		Default: "custom",
		Agents: map[string]AgentDef{
			"custom": {
				Cmd:  "custom-agent",
				Args: []string{"--flag"},
			},
		},
	}

	data, err := json.MarshalIndent(customCfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadAgentConfig(tmpDir)
	if err != nil {
		t.Fatalf("LoadAgentConfig() error = %v, want nil", err)
	}

	if cfg.Default != "custom" {
		t.Errorf("default = %q, want 'custom'", cfg.Default)
	}

	agent, ok := cfg.Agents["custom"]
	if !ok {
		t.Fatal("custom agent not found")
	}

	if agent.Cmd != "custom-agent" {
		t.Errorf("agent.Cmd = %q, want 'custom-agent'", agent.Cmd)
	}

	if len(agent.Args) != 1 || agent.Args[0] != "--flag" {
		t.Errorf("agent.Args = %v, want ['--flag']", agent.Args)
	}
}

// TestLoadAgentConfig_Invalid — write malformed JSON, expect error
func TestLoadAgentConfig_Invalid(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "agents.json")

	if err := os.WriteFile(configPath, []byte("{invalid json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadAgentConfig(tmpDir)
	if err == nil {
		t.Error("LoadAgentConfig() with invalid JSON should return error")
	}
}

// TestGetAgent_Default — empty type returns default agent
func TestGetAgent_Default(t *testing.T) {
	cfg := &AgentConfig{
		Default: "claude",
		Agents: map[string]AgentDef{
			"claude": {Cmd: "claude"},
		},
	}

	agent, err := cfg.GetAgent("")
	if err != nil {
		t.Fatalf("GetAgent(\"\") error = %v, want nil", err)
	}

	if agent.Cmd != "claude" {
		t.Errorf("agent.Cmd = %q, want 'claude'", agent.Cmd)
	}
}

// TestGetAgent_Specific — "codex" returns codex agent def
func TestGetAgent_Specific(t *testing.T) {
	cfg := &AgentConfig{
		Default: "claude",
		Agents: map[string]AgentDef{
			"claude": {Cmd: "claude"},
			"codex":  {Cmd: "codex"},
		},
	}

	agent, err := cfg.GetAgent("codex")
	if err != nil {
		t.Fatalf("GetAgent(\"codex\") error = %v, want nil", err)
	}

	if agent.Cmd != "codex" {
		t.Errorf("agent.Cmd = %q, want 'codex'", agent.Cmd)
	}
}

// TestGetAgent_Unknown — unknown type returns error
func TestGetAgent_Unknown(t *testing.T) {
	cfg := &AgentConfig{
		Default: "claude",
		Agents: map[string]AgentDef{
			"claude": {Cmd: "claude"},
		},
	}

	_, err := cfg.GetAgent("unknown")
	if err == nil {
		t.Error("GetAgent(\"unknown\") should return error")
	}
}

