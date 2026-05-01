package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
		agentName:  w.AgentName,
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

	pushToken, err := pushTokenForAgent(paths.Socket, w.AgentName)
	if err != nil {
		return err
	}

	c, err := client.Connect(paths.Socket, config.Defaults.ConnectTimeout)
	if err != nil {
		releasePushTokenForAgent(paths.Socket, w.AgentName, pushToken)
		return err
	}
	connected := false
	releaseOnReturn := true
	defer func() {
		// Clean disconnect prevents task requeue and lock release.
		if connected {
			sendDisconnect(c, fmt.Sprintf("catch-up %q/%q", w.ProjectID, w.AgentName))
		}
		c.Close()
		if releaseOnReturn {
			releasePushTokenForAgent(paths.Socket, w.AgentName, pushToken)
		}
	}()

	// Set deadline for the entire catch-up operation (handshake + inbox + disconnect).
	if err := c.SetDeadline(config.Defaults.ConnectTimeout); err != nil {
		return fmt.Errorf("set deadline: %w", err)
	}
	resp, err := c.Send(protocol.Request{
		Cmd:          protocol.CmdConnect,
		Name:         w.AgentName + "-push",
		PushListener: true,
		PushToken:    pushToken,
	})
	if err != nil {
		return err
	}
	if !resp.OK {
		// protocol.ErrAlreadyConnected is transient during restarts or duplicate workers;
		// Manager.runWatch treats the returned Listen error as retryable and reconnects.
		releaseOnReturn = false
		return fmt.Errorf("%s: %s", resp.Code, resp.Error)
	}
	connected = true

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
	agentName  string
	name       string
}

func (l *brokerListener) Listen(ctx context.Context, handler DeliveryHandler) error {
	pushToken, err := pushTokenForAgent(l.socketPath, l.agentName)
	if err != nil {
		return err
	}

	c, err := client.Connect(l.socketPath, config.Defaults.ConnectTimeout)
	if err != nil {
		releasePushTokenForAgent(l.socketPath, l.agentName, pushToken)
		return err
	}
	releaseOnReturn := true
	defer func() {
		c.Close()
		if releaseOnReturn {
			releasePushTokenForAgent(l.socketPath, l.agentName, pushToken)
		}
	}()

	if err := c.SetDeadline(config.Defaults.ConnectTimeout); err != nil {
		return fmt.Errorf("set handshake deadline: %w", err)
	}
	resp, err := c.Send(protocol.Request{
		Cmd:          protocol.CmdConnect,
		Name:         l.name,
		PushListener: true,
		PushToken:    pushToken,
	})
	if err != nil {
		return err
	}
	if !resp.OK {
		releaseOnReturn = false
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

func disconnectClient(c *client.Client) {
	if c == nil {
		return
	}
	sendDisconnect(c, "listener")
	c.Close()
}

func sendDisconnect(c *client.Client, context string) {
	if c == nil {
		return
	}
	if err := c.SetDeadline(config.Defaults.DisconnectTimeout); err != nil {
		log.Printf("warning: set disconnect deadline for %s: %v", context, err)
		return
	}
	if _, err := c.Send(protocol.Request{Cmd: protocol.CmdDisconnect}); err != nil {
		log.Printf("warning: disconnect %s: %v", context, err)
	}
}

func pushTokenForAgent(socketPath, agent string) (string, error) {
	c, err := client.Connect(socketPath, config.Defaults.ConnectTimeout)
	if err != nil {
		return "", err
	}
	defer c.Close()

	if err := c.SetDeadline(config.Defaults.ConnectTimeout); err != nil {
		return "", fmt.Errorf("set push reserve deadline: %w", err)
	}
	resp, err := c.Send(protocol.Request{
		Cmd:  protocol.CmdPushReserve,
		Name: agent,
	})
	if err != nil {
		return "", err
	}
	if !resp.OK {
		return "", fmt.Errorf("%s: %s", resp.Code, resp.Error)
	}

	var data struct {
		PushToken string `json:"push_token"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return "", fmt.Errorf("parse push reserve response: %w", err)
	}
	if data.PushToken == "" {
		return "", fmt.Errorf("push reserve response missing token")
	}
	return data.PushToken, nil
}

func releasePushTokenForAgent(socketPath, agent, pushToken string) {
	if socketPath == "" || agent == "" || pushToken == "" {
		return
	}
	c, err := client.Connect(socketPath, config.Defaults.ConnectTimeout)
	if err != nil {
		log.Printf("warning: release push token for %s: %v", agent, err)
		return
	}
	defer c.Close()

	if err := c.SetDeadline(config.Defaults.DisconnectTimeout); err != nil {
		log.Printf("warning: set push release deadline for %s: %v", agent, err)
		return
	}
	resp, err := c.Send(protocol.Request{
		Cmd:       protocol.CmdPushRelease,
		Name:      agent,
		PushToken: pushToken,
	})
	if err != nil {
		log.Printf("warning: release push token for %s: %v", agent, err)
		return
	}
	if !resp.OK {
		log.Printf("warning: release push token for %s: %s: %s", agent, resp.Code, resp.Error)
	}
}
