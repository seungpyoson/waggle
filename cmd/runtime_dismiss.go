package cmd

import (
	"github.com/spf13/cobra"
)

func init() {
	runtimeCmd.AddCommand(runtimeDismissCmd)
}

var runtimeDismissCmd = &cobra.Command{
	Use:   "dismiss <agent-name>",
	Short: "Dismiss all surfaced records for an agent",
	Long:  "Dismiss all surfaced-but-not-dismissed records for the named agent. Normal path uses Bootstrap which auto-dismisses.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, _, err := openRuntimeStore()
		if err != nil {
			return err
		}
		defer store.Close()

		projectID, err := resolveRuntimeProjectID("")
		if err != nil {
			return err
		}

		agentName := args[0]
		count, err := store.DismissAllSurfaced(projectID, agentName)
		if err != nil {
			return err
		}

		printJSON(map[string]any{
			"ok":      true,
			"message": "records dismissed",
			"count":   count,
			"agent":   agentName,
			"project": projectID,
		})
		return nil
	},
}
