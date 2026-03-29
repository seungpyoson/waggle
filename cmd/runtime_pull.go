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

		messageIDs := make([]int64, 0, len(records))
		for _, rec := range records {
			messageIDs = append(messageIDs, rec.MessageID)
		}
		if err := store.MarkSurfacedBatch(projectID, args[0], messageIDs); err != nil {
			return err
		}

		printJSON(map[string]any{
			"ok":      true,
			"records": records,
		})
		return nil
	},
}
