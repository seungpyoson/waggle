package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/broker"
	"github.com/seungpyoson/waggle/internal/client"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
)

func startRuntimeTestBroker(t *testing.T, projectID string) (string, func()) {
	t.Helper()

	home, err := os.MkdirTemp("/tmp", "waggle-runtime-listener-")
	if err != nil {
		t.Fatalf("mktemp home: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(home)
	})
	t.Setenv("HOME", home)

	paths := config.NewPaths(projectID)
	if err := os.MkdirAll(filepath.Dir(paths.DB), 0o700); err != nil {
		t.Fatalf("mkdir db dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.Socket), 0o700); err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}

	b, err := broker.New(broker.Config{
		SocketPath: paths.Socket,
		DBPath:     paths.DB,
	})
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		_ = b.Serve()
	}()
	time.Sleep(100 * time.Millisecond)

	return paths.Socket, func() {
		_ = b.Shutdown()
		_ = os.Remove(paths.Socket)
	}
}

func connectRuntimeClient(t *testing.T, socketPath string) *client.Client {
	t.Helper()

	c, err := client.Connect(socketPath, 5*time.Second)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	return c
}

func TestBrokerListenerListenReceivesPushWithoutBaseConnection(t *testing.T) {
	socketPath, cleanup := startRuntimeTestBroker(t, "proj-listen")
	defer cleanup()

	listener, err := NewBrokerListenerFactory().NewListener(Watch{
		ProjectID: "proj-listen",
		AgentName: "alice",
		Source:    "hook",
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	deliveries := make(chan Delivery, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- listener.Listen(ctx, func(d Delivery) error {
			deliveries <- d
			cancel()
			return nil
		})
	}()

	sender := connectRuntimeClient(t, socketPath)
	defer sender.Close()
	resp, err := sender.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("sender connect failed: %s", resp.Error)
	}
	waitForRuntimePresence(t, sender, "alice")

	sendResp, err := sender.Send(protocol.Request{
		Cmd:     protocol.CmdSend,
		Name:    "alice",
		Message: "hello without base session",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sendResp.OK {
		t.Fatalf("send failed: %s", sendResp.Error)
	}

	select {
	case delivery := <-deliveries:
		if delivery.FromName != "sender" {
			t.Fatalf("delivery.FromName = %q, want sender", delivery.FromName)
		}
		if delivery.Body != "hello without base session" {
			t.Fatalf("delivery.Body = %q, want %q", delivery.Body, "hello without base session")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for pushed delivery")
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Listen() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for listener shutdown")
	}
}

func TestBrokerListenerListenReleasesPushTokenOnShutdown(t *testing.T) {
	socketPath, cleanup := startRuntimeTestBroker(t, "proj-listen-release")
	defer cleanup()

	token, err := pushTokenForAgent(socketPath, "alice")
	if err != nil {
		t.Fatal(err)
	}

	listener, err := NewBrokerListenerFactory().NewListener(Watch{
		ProjectID: "proj-listen-release",
		AgentName: "alice",
		Source:    "hook",
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- listener.Listen(ctx, func(d Delivery) error {
			cancel()
			return nil
		})
	}()

	sender := connectRuntimeClient(t, socketPath)
	defer sender.Close()
	resp, err := sender.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("sender connect failed: %s", resp.Error)
	}
	waitForRuntimePresence(t, sender, "alice")

	sendResp, err := sender.Send(protocol.Request{
		Cmd:     protocol.CmdSend,
		Name:    "alice",
		Message: "release after delivery",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sendResp.OK {
		t.Fatalf("send failed: %s", sendResp.Error)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Listen() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for listener shutdown")
	}

	waitForPushTokenRejected(t, socketPath, "alice-push", token)
}

func TestBrokerListenerFailedHandshakeDoesNotReleaseActiveListenerToken(t *testing.T) {
	socketPath, cleanup := startRuntimeTestBroker(t, "proj-listen-duplicate")
	defer cleanup()

	token, err := pushTokenForAgent(socketPath, "alice")
	if err != nil {
		t.Fatal(err)
	}

	active := connectRuntimeClient(t, socketPath)
	defer active.Close()
	resp, err := active.Send(protocol.Request{
		Cmd:          protocol.CmdConnect,
		Name:         "alice-push",
		PushListener: true,
		PushToken:    token,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("active listener connect failed: %s: %s", resp.Code, resp.Error)
	}

	listener, err := NewBrokerListenerFactory().NewListener(Watch{
		ProjectID: "proj-listen-duplicate",
		AgentName: "alice",
		Source:    "hook",
	})
	if err != nil {
		t.Fatal(err)
	}

	err = listener.Listen(context.Background(), func(d Delivery) error {
		t.Fatal("duplicate listener unexpectedly received delivery")
		return nil
	})
	if err == nil {
		t.Fatal("expected duplicate listener handshake to fail")
	}

	sender := connectRuntimeClient(t, socketPath)
	defer sender.Close()
	resp, err = sender.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("sender connect failed: %s: %s", resp.Code, resp.Error)
	}
	resp, err = sender.Send(protocol.Request{
		Cmd:     protocol.CmdSend,
		Name:    "alice",
		Message: "still active",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("send failed: %s: %s", resp.Code, resp.Error)
	}

	msg, err := active.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if !msg.OK {
		t.Fatalf("active listener receive failed: %s", msg.Error)
	}
}

func TestBrokerListenerCatchUpReadsInboxWithoutBaseConnection(t *testing.T) {
	socketPath, cleanup := startRuntimeTestBroker(t, "proj-catchup")
	defer cleanup()

	sender := connectRuntimeClient(t, socketPath)
	defer sender.Close()
	resp, err := sender.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("sender connect failed: %s", resp.Error)
	}

	sendResp, err := sender.Send(protocol.Request{
		Cmd:     protocol.CmdSend,
		Name:    "alice",
		Message: "catch-up delivery",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sendResp.OK {
		t.Fatalf("send failed: %s", sendResp.Error)
	}

	var got []Delivery
	if err := NewBrokerListenerFactory().CatchUp(Watch{
		ProjectID: "proj-catchup",
		AgentName: "alice",
		Source:    "hook",
	}, func(d Delivery) error {
		got = append(got, d)
		return nil
	}); err != nil {
		t.Fatalf("CatchUp() error = %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("CatchUp() deliveries = %d, want 1", len(got))
	}
	if got[0].FromName != "sender" {
		t.Fatalf("delivery.FromName = %q, want sender", got[0].FromName)
	}
	if got[0].Body != "catch-up delivery" {
		t.Fatalf("delivery.Body = %q, want %q", got[0].Body, "catch-up delivery")
	}
}

func TestBrokerListenerCatchUpReleasesPushToken(t *testing.T) {
	socketPath, cleanup := startRuntimeTestBroker(t, "proj-catchup-release")
	defer cleanup()

	token, err := pushTokenForAgent(socketPath, "alice")
	if err != nil {
		t.Fatal(err)
	}

	sender := connectRuntimeClient(t, socketPath)
	defer sender.Close()
	resp, err := sender.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("sender connect failed: %s", resp.Error)
	}

	sendResp, err := sender.Send(protocol.Request{
		Cmd:     protocol.CmdSend,
		Name:    "alice",
		Message: "catch-up release delivery",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sendResp.OK {
		t.Fatalf("send failed: %s", sendResp.Error)
	}

	var got []Delivery
	if err := NewBrokerListenerFactory().CatchUp(Watch{
		ProjectID: "proj-catchup-release",
		AgentName: "alice",
		Source:    "hook",
	}, func(d Delivery) error {
		got = append(got, d)
		return nil
	}); err != nil {
		t.Fatalf("CatchUp() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("CatchUp() deliveries = %d, want 1", len(got))
	}

	waitForPushTokenRejected(t, socketPath, "alice-push", token)
}

func waitForRuntimePresence(t *testing.T, c *client.Client, name string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := c.Send(protocol.Request{Cmd: protocol.CmdPresence})
		if err != nil {
			t.Fatalf("presence: %v", err)
		}
		if !resp.OK {
			t.Fatalf("presence failed: %s", resp.Error)
		}
		var agents []map[string]string
		if err := json.Unmarshal(resp.Data, &agents); err != nil {
			t.Fatalf("parse presence: %v", err)
		}
		for _, agent := range agents {
			if agent["name"] == name {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for runtime listener %q in presence", name)
}

func waitForPushTokenRejected(t *testing.T, socketPath, listenerName, token string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c := connectRuntimeClient(t, socketPath)
		resp, err := c.Send(protocol.Request{
			Cmd:          protocol.CmdConnect,
			Name:         listenerName,
			PushListener: true,
			PushToken:    token,
		})
		c.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !resp.OK && resp.Code == protocol.ErrForbidden {
			return
		}
		if resp.OK {
			t.Fatal("expected released push token to be rejected")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for released push token to be rejected for %q", listenerName)
}
