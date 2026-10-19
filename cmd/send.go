package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
	"os"
)

// resolveAgentName resolves the agent name from --name flag or WAGGLE_AGENT_NAME env var
func resolveAgentName(cmd *cobra.Command) (string, error) {
	name, _ := cmd.Flags().GetString("name")
	if name == "" {
		name = os.Getenv("WAGGLE_AGENT_NAME")
	}
	if name == "" {
		return "", fmt.Errorf("agent name required: set WAGGLE_AGENT_NAME or use --name")
	}
	return name, nil
}
