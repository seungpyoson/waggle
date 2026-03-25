package spawn

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type AgentConfig struct {
	Default string                `json:"default"`
	Agents  map[string]AgentDef  `json:"agents"`
}

type AgentDef struct {
	Cmd  string   `json:"cmd"`
	Args []string `json:"args,omitempty"`
}

// LoadAgentConfig loads agent configuration from the given config directory.
// If the config file doesn't exist, creates a default config.
func LoadAgentConfig(configDir string) (*AgentConfig, error) {
	configPath := filepath.Join(configDir, "agents.json")

	// Check if file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Create default config
		defaultCfg := &AgentConfig{
			Default: "claude",
			Agents: map[string]AgentDef{
				"claude": {
					Cmd:  "claude",
					Args: []string{"-p", "--output-format", "stream-json"},
				},
				"codex": {
					Cmd: "codex",
				},
				"gemini": {
					Cmd: "gemini",
				},
			},
		}

		// Write default config to file
		data, err := json.MarshalIndent(defaultCfg, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshaling default config: %w", err)
		}

		if err := os.WriteFile(configPath, data, 0644); err != nil {
			return nil, fmt.Errorf("writing default config: %w", err)
		}

		return defaultCfg, nil
	}

	// Read existing config
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg AgentConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	return &cfg, nil
}

// GetAgent returns the agent definition for the given type.
// If agentType is empty, returns the default agent.
func (c *AgentConfig) GetAgent(agentType string) (*AgentDef, error) {
	// Use default if agentType is empty
	if agentType == "" {
		agentType = c.Default
	}

	// Look up agent
	agent, ok := c.Agents[agentType]
	if !ok {
		return nil, fmt.Errorf("unknown agent type: %s", agentType)
	}

	return &agent, nil
}

