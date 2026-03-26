package cmd

import (
	"encoding/json"
	"fmt"
	"time"

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
	taskCreateTTL     string
)

func init() {
	taskCreateCmd.Flags().StringVar(&taskType, "type", "", "Task type")
	taskCreateCmd.Flags().StringVar(&taskTags, "tags", "", "Task tags (comma-separated)")
	taskCreateCmd.Flags().StringVar(&taskDependsOn, "depends-on", "", "Task dependencies (comma-separated IDs)")
	taskCreateCmd.Flags().IntVar(&taskLease, "lease", 0, "Lease duration in seconds")
	taskCreateCmd.Flags().IntVar(&taskMaxRetries, "max-retries", 0, "Maximum retries")
	taskCreateCmd.Flags().IntVar(&taskPriority, "priority", 0, "Task priority")
	taskCreateCmd.Flags().StringVar(&taskIdempotencyKey, "idempotency-key", "", "Idempotency key")
	taskCreateCmd.Flags().StringVar(&taskCreateTTL, "ttl", "", "Task TTL (e.g., '5m', '1h', '30s') — auto-cancel if unclaimed")
	taskCmd.AddCommand(taskCreateCmd)
}

var taskCreateCmd = &cobra.Command{
	Use:   "create <payload>",
	Short: "Create a new task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		payload := args[0]

		c, err := connectToBroker("")
		if err != nil {
			printErr("BROKER_NOT_RUNNING", err.Error())
			return nil
		}
		defer disconnectAndClose(c)

		// Parse TTL
		var ttlSeconds int
		if taskCreateTTL != "" {
			d, err := time.ParseDuration(taskCreateTTL)
			if err != nil {
				printErr("INVALID_REQUEST", fmt.Sprintf("invalid ttl duration: %v", err))
				return nil
			}
			ttlSeconds = int(d.Seconds())
			// Reject sub-second TTL that would silently become no-TTL
			if d > 0 && ttlSeconds == 0 {
				printErr("INVALID_REQUEST", "ttl must be at least 1 second")
				return nil
			}
		}

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
			TTL:            ttlSeconds,
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

