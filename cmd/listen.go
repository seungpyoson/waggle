package cmd

import (
	"encoding/json"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

var (
	listenName   string
	listenOutput string
)

func init() {
	listenCmd.Flags().StringVar(&listenName, "name", "", "Agent name to listen as (required)")
	listenCmd.MarkFlagRequired("name")
	listenCmd.Flags().StringVar(&listenOutput, "output", "", "Output file path (default: stdout)")
	rootCmd.AddCommand(listenCmd)
}

var listenCmd = &cobra.Command{
	Use:   "listen",
	Short: "Listen for pushed messages (persistent connection)",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := connectToBroker(listenName)
		if err != nil {
			printErr("BROKER_NOT_RUNNING", err.Error())
			return nil
		}
		// Don't defer disconnectAndClose — handle signals manually

		// Set up output
		var output *os.File
		if listenOutput != "" {
			f, err := os.OpenFile(listenOutput, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err != nil {
				printErr("FILE_ERROR", err.Error())
				c.Close()
				return nil
			}
			defer f.Close()
			output = f
		} else {
			output = os.Stdout
		}

		// Read messages
		msgCh, err := c.ReadMessages()
		if err != nil {
			printErr("INTERNAL_ERROR", err.Error())
			c.Close()
			return nil
		}

		// Handle signals
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

		enc := json.NewEncoder(output)
		for {
			select {
			case msg, ok := <-msgCh:
				if !ok {
					// Channel closed — broker disconnected
					return nil
				}
				// Write JSON line with received_at
				line := map[string]any{
					"id":          msg.ID,
					"from":        msg.From,
					"body":        msg.Body,
					"sent_at":     msg.SentAt,
					"received_at": time.Now().UTC().Format(time.RFC3339),
				}
				enc.Encode(line)
				if listenOutput != "" {
					output.Sync()
				}
			case <-sigCh:
				disconnectAndClose(c)
				return nil
			}
		}
	},
}

