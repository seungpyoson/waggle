package cmd

import (
	"encoding/json"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

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
		// When --output is set, this command typically runs backgrounded (e.g., from
		// hooks). Redirect stderr to a .err file so errors don't corrupt the host
		// terminal (TUI). This must happen BEFORE any printErr/fmt.Fprintf calls.
		// Redirect both os.Stderr (Go-level) and fd 2 (OS-level) to catch all output.
		if listenOutput != "" {
			errFile, err := os.OpenFile(listenOutput+".err", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err == nil {
				os.Stderr = errFile
				// Also redirect OS fd 2 so log.Printf and any C-level writes go to file
				_ = unix.Dup2(int(errFile.Fd()), 2)
				defer errFile.Close()
			}
			// If errFile fails to open, keep original stderr — better than crashing
		}

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
				// Close connection directly — NOT disconnectAndClose.
				// disconnectAndClose calls c.Send() which races with the ReadMessages goroutine
				// that's concurrently calling c.scanner.Scan(). bufio.Scanner is NOT goroutine-safe.
				c.Close()
				return nil
			}
		}
	},
}
