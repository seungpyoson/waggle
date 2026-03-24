package cmd

import (
	"encoding/json"

	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

var (
	taskType          string
	taskTags          string
	taskDependsOn     string
	taskLease         int
	taskMaxRetries    int
	taskPriority      int
	taskIdempotencyKey string
)

func init() {
	taskCreateCmd.Flags().StringVar(&taskType, "type", "", "Task type")
	taskCreateCmd.Flags().StringVar(&taskTags, "tags", "", "Task tags (comma-separated)")
	taskCreateCmd.Flags().StringVar(&taskDependsOn, "depends-on", "", "Task dependencies (comma-separated IDs)")
	taskCreateCmd.Flags().IntVar(&taskLease, "lease", 0, "Lease duration in seconds")
	taskCreateCmd.Flags().IntVar(&taskMaxRetries, "max-retries", 0, "Maximum retries")
	taskCreateCmd.Flags().IntVar(&taskPriority, "priority", 0, "Task priority")
	taskCreateCmd.Flags().StringVar(&taskIdempotencyKey, "idempotency-key", "", "Idempotency key")
	taskCmd.AddCommand(taskCreateCmd)
}

var taskCreateCmd = &cobra.Command{
	Use:   "create <payload>",
	Short: "Create a new task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		payload := args[0]

		c, err := connectWithSession("cli")
		if err != nil {
			printErr("BROKER_NOT_RUNNING", err.Error())
			return nil
		}
		defer c.Close()

		req := protocol.Request{
			Cmd:            protocol.CmdTaskCreate,
			Payload:        json.RawMessage(payload),
			Type:           taskType,
			Tags:           taskTags,
			DependsOn:      taskDependsOn,
			Priority:       taskPriority,
			Lease:          taskLease,
			MaxRetries:     taskMaxRetries,
			IdempotencyKey: taskIdempotencyKey,
		}

		resp, err := c.Send(req)
		if err != nil {
			printErr("INTERNAL_ERROR", err.Error())
			return nil
		}

		if !resp.OK {
			printErr(resp.Code, resp.Error)
			return nil
		}

		printJSON(resp)
		return nil
	},
}

