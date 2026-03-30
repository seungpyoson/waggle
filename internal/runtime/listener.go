package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/seungpyoson/waggle/internal/client"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
)

// BrokerListenerFactory connects runtime watches directly to the broker push stream.
type BrokerListenerFactory struct{}

func NewBrokerListenerFactory() *BrokerListenerFactory {
	return &BrokerListenerFactory{}
}

func (f *BrokerListenerFactory) NewListener(w Watch) (Listener, error) {
	if w.ProjectID == "" {
		return nil, fmt.Errorf("project_id required")
	}
	if w.AgentName == "" {
		return nil, fmt.Errorf("agent_name required")
	}

	paths := config.NewPaths(w.ProjectID)
	if paths.Socket == "" {
		return nil, fmt.Errorf("cannot determine broker socket path")
	}
	return &brokerListener{
		socketPath: paths.Socket,
		name:       w.AgentName + "-push",
	}, nil
}

func (f *BrokerListenerFactory) CatchUp(w Watch, handler DeliveryHandler) error {
	if w.ProjectID == "" || w.AgentName == "" {
		return fmt.Errorf("project_id and agent_name required")
	}

	paths := config.NewPaths(w.ProjectID)
	if paths.Socket == "" {
		return fmt.Errorf("cannot determine broker socket path")
	}

	c, err := client.Connect(paths.Socket, config.Defaults.ConnectTimeout)
	if err != nil {
		return err
	}
	defer func() {
		// Clean disconnect prevents task requeue and lock release.
		if err := c.SetDeadline(config.Defaults.DisconnectTimeout); err == nil {
			_, _ = c.Send(protocol.Request{Cmd: protocol.CmdDisconnect})
		}
		c.Close()
	}()

	// Set deadline for the entire catch-up operation (handshake + inbox + disconnect).
	if err := c.SetDeadline(config.Defaults.ConnectTimeout); err != nil {
		return fmt.Errorf("set deadline: %w", err)
	}
	resp, err := c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: w.AgentName})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s: %s", resp.Code, resp.Error)
	}

	resp, err = c.Send(protocol.Request{Cmd: protocol.CmdInbox})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("inbox: %s: %s", resp.Code, resp.Error)
	}

	var msgs []struct {
		ID        int64  `json:"id"`
		From      string `json:"from"`
		Body      string `json:"body"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.Unmarshal(resp.Data, &msgs); err != nil {
		return fmt.Errorf("parse inbox: %w", err)
	}

	for _, msg := range msgs {
		sentAt, err := time.Parse(time.RFC3339, msg.CreatedAt)
		if err != nil {
			return fmt.Errorf("parse created_at for message %d: %w", msg.ID, err)
		}
		if err := handler(Delivery{
			MessageID:  msg.ID,
			FromName:   msg.From,
			Body:       msg.Body,
			SentAt:     sentAt,
			ReceivedAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
	}

	return nil
}

type brokerListener struct {
	socketPath string
	name       string
}

func (l *brokerListener) Listen(ctx context.Context, handler DeliveryHandler) error {
	c, err := client.Connect(l.socketPath, config.Defaults.ConnectTimeout)
	if err != nil {
		return err
	}
	defer c.Close()

	if err := c.SetDeadline(config.Defaults.ConnectTimeout); err != nil {
		return fmt.Errorf("set handshake deadline: %w", err)
	}
	resp, err := c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: l.name})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s: %s", resp.Code, resp.Error)
	}
	if err := c.ClearDeadline(); err != nil {
		return fmt.Errorf("clear deadline: %w", err)
	}

	msgCh, err := c.ReadMessages()
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-msgCh:
			if !ok {
				return nil
			}
			delivery, err := pushedMessageToDelivery(msg)
			if err != nil {
				return err
			}
			if err := handler(delivery); err != nil {
				return err
			}
		}
	}
}

func pushedMessageToDelivery(msg client.PushedMessage) (Delivery, error) {
	sentAt, err := time.Parse(time.RFC3339, msg.SentAt)
	if err != nil {
		return Delivery{}, fmt.Errorf("parse sent_at: %w", err)
	}
	return Delivery{
		MessageID:  msg.ID,
		FromName:   msg.From,
		Body:       msg.Body,
		SentAt:     sentAt,
		ReceivedAt: time.Now().UTC(),
	}, nil
}
