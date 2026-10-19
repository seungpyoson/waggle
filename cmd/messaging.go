package cmd

import (
	"crypto/rand"
	"fmt"
	"os"
	"time"

	"github.com/seungpyoson/waggle/internal/client"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

func init() {
	for _, spec := range []struct{ name, description, wire string }{
		{"send", "Send directly to a receive-bound incarnation", protocol.CmdSend},
		{"enqueue", "Store explicitly for a bound or disconnected incarnation", protocol.CmdEnqueue},
	} {
		var within time.Duration
		var hops int
		var requestID string
		cmd := &cobra.Command{Use: spec.name + " <recipient-id> <message>", Short: spec.description, Args: cobra.ExactArgs(2)}
		defaults := config.NewMessagingConfig()
		cmd.Flags().DurationVar(&within, "within", defaults.DefaultDeadline, "Conversation deadline from now")
		cmd.Flags().IntVar(&hops, "hops", defaults.DefaultHops, "Maximum conversation dispatches, inherited by replies")
		cmd.Flags().StringVar(&requestID, "request-id", "", "Caller-owned idempotency key; generated when omitted")
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			return messagingRPC(cmd, protocol.Request{Cmd: spec.wire, Recipient: args[0], Message: args[1], Within: within, Hops: hops, IdempotencyKey: requestID})
		}
		rootCmd.AddCommand(cmd)
	}
	var attempt string
	ack := &cobra.Command{Use: "ack <message-id>", Short: "Record consumption by this enrolled recipient", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return messagingRPC(cmd, protocol.Request{Cmd: protocol.CmdAck, MessageID: args[0], AttemptID: attempt})
	}}
	ack.Flags().StringVar(&attempt, "attempt", "", "Exact attempt ID from the Waggle envelope")
	ack.MarkFlagRequired("attempt")
	rootCmd.AddCommand(ack)
	var replyRequestID string
	reply := &cobra.Command{Use: "reply <message-id> <message>", Short: "Reply with inherited correlation, deadline and budget", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		return messagingRPC(cmd, protocol.Request{Cmd: protocol.CmdReply, MessageID: args[0], Message: args[1], IdempotencyKey: replyRequestID})
	}}
	reply.Flags().StringVar(&replyRequestID, "request-id", "", "Caller-owned idempotency key; generated when omitted")
	rootCmd.AddCommand(reply)
	for _, spec := range []struct{ name, description, wire string }{
		{"inbox", "Read message evidence without acknowledging", protocol.CmdInbox},
		{"whoami", "Show the identity authenticated by this session's credential", protocol.CmdWhoami},
	} {
		rootCmd.AddCommand(&cobra.Command{Use: spec.name, Short: spec.description, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			return messagingRPC(cmd, protocol.Request{Cmd: spec.wire})
		}})
	}
	var after string
	sessions := &cobra.Command{Use: "sessions", Short: "List a bounded page of canonical enrollments and native readiness", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		return messagingRPC(cmd, protocol.Request{Cmd: protocol.CmdPresence, After: after})
	}}
	sessions.Flags().StringVar(&after, "after", "", "Continue after the final incarnation ID in the preceding page")
	rootCmd.AddCommand(sessions)
	conversation := &cobra.Command{Use: "conversation", Short: "Control future Waggle dispatches"}
	conversation.AddCommand(&cobra.Command{Use: "stop <conversation-id> <reason>", Short: "Stop future dispatches; native withdrawal is unsupported", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		return messagingRPC(cmd, protocol.Request{Cmd: protocol.CmdConversationStop, ConversationID: args[0], Reason: args[1]})
	}})
	rootCmd.AddCommand(conversation)
	rootCmd.AddCommand(&cobra.Command{Use: "retire <incarnation-id> <reason>", Short: "Permanently disable routing; previously submitted native work may remain", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		return messagingRPC(cmd, protocol.Request{Cmd: protocol.CmdRetire, Recipient: args[0], Reason: args[1]})
	}})
}

// Messaging RPC has no named-connection handshake. The broker authenticates
// each operation from the scoped enrollment credential in the same transaction.
func messagingRPC(cmd *cobra.Command, req protocol.Request) error {
	switch req.Cmd {
	case protocol.CmdSend, protocol.CmdEnqueue, protocol.CmdReply:
		if req.IdempotencyKey == "" {
			req.IdempotencyKey = rand.Text()
		}
	}
	req.Credential = os.Getenv(config.IncarnationCredentialEnv)
	if req.Cmd != protocol.CmdPresence && req.Cmd != protocol.CmdRetire && req.Credential == "" {
		return fmt.Errorf("missing enrollment credential; start the broker and restart this provider session")
	}
	c, err := client.Connect(paths.Socket, config.Defaults.ConnectTimeout)
	if err != nil {
		return err
	}
	defer c.Close()
	if err = c.SetDeadline(config.Defaults.ConnectTimeout); err != nil {
		return err
	}
	response, err := c.Send(req)
	if err != nil {
		return err
	}
	if !response.OK {
		return fmt.Errorf("%s: %s", response.Code, response.Error)
	}
	printJSON(response)
	return nil
}
