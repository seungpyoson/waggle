package cmd

import "github.com/spf13/cobra"

var runtimePullProjectID string

func init() {
	runtimePullCmd.Flags().StringVar(&runtimePullProjectID, "project-id", "", "Project ID to pull from (defaults to current project)")
	runtimeCmd.AddCommand(runtimePullCmd)
}

var runtimePullCmd = &cobra.Command{
	Use:   "pull <agent>",
	Short: "Output unread machine-runtime records for an agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, _, err := openRuntimeStore()
		if err != nil {
			return err
		}
		defer store.Close()

		projectID, err := resolveRuntimeProjectID(runtimePullProjectID)
		if err != nil {
			return err
		}

		records, err := store.Unread(projectID, args[0])
		if err != nil {
			return err
		}
		for _, rec := range records {
			if err := store.MarkSurfaced(projectID, args[0], rec.MessageID); err != nil {
				return err
			}
		}

		printJSON(map[string]any{
			"ok":      true,
			"records": records,
		})
		return nil
	},
}
