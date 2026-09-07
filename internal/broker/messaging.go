package broker

import (
	"context"
	"fmt"
	"time"

	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/messages"
	"github.com/seungpyoson/waggle/internal/protocol"
)

// Every messaging request enters one transaction. Connection names are task
// worker labels; they convey no messaging identity or receipt authority.
func routeMessages(s *call, req protocol.Request) protocol.Response {
	var data any
	err := s.op.Write(context.Background(), func(tx *brokerstate.WriteTx) error {
		store := messages.NewStore(tx, s.broker.config.Messaging, time.Now())
		var command messages.Command
		switch req.Cmd {
		case protocol.CmdEnroll:
			if req.Enrollment == nil {
				return fmt.Errorf("enrollment inputs are required")
			}
			e := req.Enrollment
			if err := s.broker.native.Check(messages.Enrollment{Provider: e.Provider, Conversation: e.Conversation, Endpoint: e.Endpoint}); err != nil {
				return err
			}
			result, err := store.Apply(messages.Enroll{Provider: e.Provider, Conversation: e.Conversation, Endpoint: e.Endpoint, Label: e.Label})
			data = protocol.EnrollmentResponse{Incarnation: result.Enrollment.ID, Credential: result.Credential, State: result.Enrollment.State}
			return err
		case protocol.CmdSend, protocol.CmdEnqueue:
			command = messages.Enqueue{Credential: req.Credential, Recipient: req.Recipient, RequestID: req.IdempotencyKey, Body: req.Message, Within: req.Within, Hops: req.Hops, Offline: req.Cmd == protocol.CmdEnqueue}
		case protocol.CmdReply:
			if req.Within != 0 || req.Hops != 0 || req.ConversationID != "" || req.Recipient != "" {
				return fmt.Errorf("reply inherits its recipient, conversation, deadline and budget; overrides are invalid")
			}
			command = messages.Reply{Credential: req.Credential, MessageID: req.MessageID, RequestID: req.IdempotencyKey, Body: req.Message}
		case protocol.CmdAck:
			command = messages.Consume{Credential: req.Credential, MessageID: req.MessageID, Attempt: req.AttemptID}
		case protocol.CmdConversationStop:
			command = messages.Stop{Credential: req.Credential, Conversation: req.ConversationID, Reason: req.Reason}
		case protocol.CmdRetire:
			// Like broker stop, this is an explicit local operator action on the
			// owner-only socket. It cannot impersonate a consumption receipt.
			command = messages.Retire{ID: req.Recipient, Reason: req.Reason}
		case protocol.CmdInbox:
			items, err := store.Inbox(req.Credential)
			data = items
			return err
		case protocol.CmdWhoami:
			identity, err := store.Authenticate(req.Credential)
			data = identity
			return err
		case protocol.CmdPresence:
			items, err := store.Enrollments(req.After, messages.AllEnrollments)
			data = items
			return err
		}
		result, err := store.Apply(command)
		switch req.Cmd {
		case protocol.CmdConversationStop:
			data = map[string]string{"conversation_id": req.ConversationID, "dispatch": "stopped", "native_withdrawal": "unsupported"}
		case protocol.CmdRetire:
			data = result.Enrollment
		default:
			data = result.Message
		}
		return err
	})
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, err.Error())
	}
	switch req.Cmd {
	case protocol.CmdEnroll, protocol.CmdSend, protocol.CmdEnqueue, protocol.CmdReply, protocol.CmdAck, protocol.CmdConversationStop, protocol.CmdRetire:
		s.broker.wakeup()
	}
	return protocol.OKResponse(mustMarshal(data))
}
