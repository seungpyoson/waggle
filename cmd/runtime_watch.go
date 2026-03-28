package cmd

import (
	rt "github.com/seungpyoson/waggle/internal/runtime"
	"github.com/spf13/cobra"
)

var (
	runtimeWatchProjectID   string
	runtimeWatchSource      string
	runtimeUnwatchProjectID string
	runtimeWatchesProjectID string
)

func init() {
	runtimeWatchCmd.Flags().StringVar(&runtimeWatchProjectID, "project-id", "", "Project ID to watch (defaults to current project)")
	runtimeWatchCmd.Flags().StringVar(&runtimeWatchSource, "source", "explicit", "Registration source")
	runtimeUnwatchCmd.Flags().StringVar(&runtimeUnwatchProjectID, "project-id", "", "Project ID to unwatch (defaults to current project)")
	runtimeWatchesCmd.Flags().StringVar(&runtimeWatchesProjectID, "project-id", "", "Filter watches to a specific project ID")

	runtimeCmd.AddCommand(runtimeWatchCmd)
	runtimeCmd.AddCommand(runtimeUnwatchCmd)
	runtimeCmd.AddCommand(runtimeWatchesCmd)
}

var runtimeWatchCmd = &cobra.Command{
	Use:   "watch <agent>",
	Short: "Register a machine-runtime watch for an agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, _, err := openRuntimeStore()
		if err != nil {
			return err
		}
		defer store.Close()

		projectID, err := resolveRuntimeProjectID(runtimeWatchProjectID)
		if err != nil {
			return err
		}

		watch := rt.Watch{
			ProjectID: projectID,
			AgentName: args[0],
			Source:    runtimeWatchSource,
		}
		if err := store.UpsertWatch(watch); err != nil {
			return err
		}

		printJSON(map[string]any{
			"ok":    true,
			"watch": watch,
		})
		return nil
	},
}

var runtimeUnwatchCmd = &cobra.Command{
	Use:   "unwatch <agent>",
	Short: "Remove a machine-runtime watch for an agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, _, err := openRuntimeStore()
		if err != nil {
			return err
		}
		defer store.Close()

		projectID, err := resolveRuntimeProjectID(runtimeUnwatchProjectID)
		if err != nil {
			return err
		}

		if err := store.RemoveWatch(projectID, args[0]); err != nil {
			return err
		}

		printJSON(map[string]any{
			"ok":         true,
			"project_id": projectID,
			"agent_name": args[0],
		})
		return nil
	},
}

var runtimeWatchesCmd = &cobra.Command{
	Use:   "watches",
	Short: "List machine-runtime watches",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, _, err := openRuntimeStore()
		if err != nil {
			return err
		}
		defer store.Close()

		watches, err := store.ListWatches()
		if err != nil {
			return err
		}

		if runtimeWatchesProjectID != "" {
			filtered := make([]rt.Watch, 0, len(watches))
			for _, watch := range watches {
				if watch.ProjectID == runtimeWatchesProjectID {
					filtered = append(filtered, watch)
				}
			}
			watches = filtered
		}

		printJSON(map[string]any{
			"ok":      true,
			"watches": watches,
		})
		return nil
	},
}
