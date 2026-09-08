package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

type AgentConfig struct {
	Default  string              `json:"default"`
	Terminal string              `json:"terminal"`
	Agents   map[string]AgentDef `json:"agents"`
}
type AgentDef struct {
	Cmd  string   `json:"cmd"`
	Args []string `json:"args,omitempty"`
}

const LaunchShell = "sh"

// Defaults are selected before executable lookup. Errors never change selection.
func NewAgentConfig() *AgentConfig {
	return &AgentConfig{Default: "claude", Terminal: map[string]string{"darwin": "terminal", "linux": "x-terminal-emulator"}[runtime.GOOS], Agents: map[string]AgentDef{
		"claude": {Cmd: "claude"}, "codex": {Cmd: "codex"}, "gemini": {Cmd: "gemini"},
	}}
}

// LoadAgentConfig reads without creating state. An absent file uses declared
// defaults; an unreadable or invalid file is an error.
func LoadAgentConfig(configDir string) (*AgentConfig, error) {
	path := filepath.Join(configDir, Defaults.AgentConfigFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewAgentConfig(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read agent configuration: %w", err)
	}
	var cfg AgentConfig
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse agent configuration: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("agent configuration requires one JSON object")
	}
	return &cfg, nil
}

func (c *AgentConfig) GetAgent(name string) (string, AgentDef, error) {
	if name == "" {
		name = c.Default
	}
	agent, ok := c.Agents[name]
	if !ok || agent.Cmd == "" {
		return "", AgentDef{}, fmt.Errorf("agent type %q requires a configured executable", name)
	}
	return name, agent, nil
}
