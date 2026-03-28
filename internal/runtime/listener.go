package runtime

import (
	"context"
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
