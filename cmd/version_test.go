package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	// Reset version vars to defaults for test isolation
	oldVersion := Version
	oldCommit := Commit
	oldBuildTime := BuildTime
	defer func() {
		Version = oldVersion
		Commit = oldCommit
		BuildTime = oldBuildTime
	}()

	tests := []struct {
		name      string
		version   string
		commit    string
		buildTime string
		wantOK    bool
	}{
		{
			name:      "default values",
			version:   "dev",
			commit:    "unknown",
			buildTime: "unknown",
			wantOK:    true,
		},
		{
			name:      "injected values",
			version:   "1.0.0",
			commit:    "abc123",
			buildTime: "2026-03-29T00:00:00Z",
			wantOK:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set package vars
			Version = tt.version
			Commit = tt.commit
			BuildTime = tt.buildTime

			// Capture output
			var stdout, stderr bytes.Buffer
			rootCmd.SetOut(&stdout)
			rootCmd.SetErr(&stderr)
			rootCmd.SetArgs([]string{"version"})

			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("command failed: %v", err)
			}

			output := stdout.String()

			// Parse JSON
			var result map[string]interface{}
			if err := json.Unmarshal([]byte(output), &result); err != nil {
				t.Fatalf("output is not valid JSON: %v\nOutput: %s", err, output)
			}

			// Check required fields
			if ok, exists := result["ok"].(bool); !exists || ok != tt.wantOK {
				t.Errorf("ok field: got %v, want %v", result["ok"], tt.wantOK)
			}

			if v, exists := result["version"].(string); !exists || v != tt.version {
				t.Errorf("version field: got %v, want %v", result["version"], tt.version)
			}

			if c, exists := result["commit"].(string); !exists || c != tt.commit {
				t.Errorf("commit field: got %v, want %v", result["commit"], tt.commit)
			}

			if b, exists := result["built"].(string); !exists || b != tt.buildTime {
				t.Errorf("built field: got %v, want %v", result["built"], tt.buildTime)
			}

			// Check for human-readable line (should be after JSON or part of it)
			// The command should output both JSON and a readable line
			if !strings.Contains(output, tt.version) {
				t.Errorf("output should contain version %q: %s", tt.version, output)
			}
		})
	}
}

func TestVersionCommand_BrokerIndependent(t *testing.T) {
	// Version command should work without broker running
	// This is tested by checking isBrokerIndependentCommand covers it
	cmd := rootCmd
	for _, sub := range cmd.Commands() {
		if sub.Name() == "version" {
			if !isBrokerIndependentCommand(sub) {
				t.Error("version command should be broker-independent")
			}
			return
		}
	}
	t.Error("version command not found in root commands")
}

