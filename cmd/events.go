package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(eventsCmd)
	eventsCmd.AddCommand(subscribeCmd)
	eventsCmd.AddCommand(publishCmd)
}

var eventsCmd = &cobra.Command{
	Use:   "events",
	Short: "Event streaming commands",
}

var subscribeCmd = &cobra.Command{
	Use:   "subscribe <topic>",
	Short: "Subscribe to events on a topic",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		topic := args[0]

		c, err := connectToBroker()
		if err != nil {
			printErr("BROKER_NOT_RUNNING", err.Error())
			return nil
		}
		defer c.Close()

		// Send subscribe request
		resp, err := c.Send(protocol.Request{
			Cmd:   protocol.CmdSubscribe,
			Topic: topic,
		})
		if err != nil {
			printErr("INTERNAL_ERROR", err.Error())
			return nil
		}

		if !resp.OK {
			printErr(resp.Code, resp.Error)
			return nil
		}

		// Setup signal handler for graceful shutdown
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

		// Read events in a goroutine
		eventCh := make(chan protocol.Response)
		errCh := make(chan error)

		go func() {
			for {
				event, err := c.Receive()
				if err != nil {
					errCh <- err
					return
				}
				eventCh <- event
			}
		}()

		// Print events until interrupted
		for {
			select {
			case <-sigCh:
				return nil
			case err := <-errCh:
				printErr("INTERNAL_ERROR", err.Error())
				return nil
			case event := <-eventCh:
				data, _ := json.Marshal(event)
				fmt.Println(string(data))
			}
		}
	},
}

var publishCmd = &cobra.Command{
	Use:   "publish <topic> <message>",
	Short: "Publish an event to a topic",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		topic := args[0]
		message := args[1]

		c, err := connectToBroker()
		if err != nil {
			printErr("BROKER_NOT_RUNNING", err.Error())
			return nil
		}
		defer c.Close()

		resp, err := c.Send(protocol.Request{
			Cmd:     protocol.CmdPublish,
			Topic:   topic,
			Message: message,
		})
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

