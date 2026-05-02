package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
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

	assertPushTokenStillAccepted(t, socketPath, active, "alice-push", token, "failed Listen handshake")
}

func TestBrokerListenerCatchUpReadsWhileActiveListenerConnected(t *testing.T) {
	socketPath, cleanup := startRuntimeTestBroker(t, "proj-catchup-duplicate")
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

	err = NewBrokerListenerFactory().CatchUp(Watch{
		ProjectID: "proj-catchup-duplicate",
		AgentName: "alice",
		Source:    "hook",
	}, func(d Delivery) error {
		t.Fatal("empty catch-up unexpectedly received delivery")
		return nil
	})
	if err != nil {
		t.Fatalf("CatchUp() error = %v", err)
	}

	assertPushTokenStillAccepted(t, socketPath, active, "alice-push", token, "CatchUp replay")
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

	got = nil
	if err := NewBrokerListenerFactory().CatchUp(Watch{
		ProjectID: "proj-catchup",
		AgentName: "alice",
		Source:    "hook",
	}, func(d Delivery) error {
		got = append(got, d)
		return nil
	}); err != nil {
		t.Fatalf("second CatchUp() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("second CatchUp() deliveries = %d, want 0 after ack", len(got))
	}
}

func TestBrokerListenerCatchUpClearsReplayDeadlineBeforeAck(t *testing.T) {
	originalTimeout := config.Defaults.ConnectTimeout
	shortTimeout := 25 * time.Millisecond
	config.Defaults.ConnectTimeout = shortTimeout
	t.Cleanup(func() {
		config.Defaults.ConnectTimeout = originalTimeout
	})

	socketPath, cleanup := startRuntimeTestBroker(t, "proj-catchup-deadline")
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
		Message: "slow catch-up delivery",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sendResp.OK {
		t.Fatalf("send failed: %s", sendResp.Error)
	}

	var got []Delivery
	if err := NewBrokerListenerFactory().CatchUp(Watch{
		ProjectID: "proj-catchup-deadline",
		AgentName: "alice",
		Source:    "hook",
	}, func(d Delivery) error {
		time.Sleep(2 * shortTimeout)
		got = append(got, d)
		return nil
	}); err != nil {
		t.Fatalf("CatchUp() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("CatchUp() deliveries = %d, want 1", len(got))
	}
}

func TestBrokerListenerCatchUpAcksPriorMessagesBeforeHandlerError(t *testing.T) {
	socketPath, cleanup := startRuntimeTestBroker(t, "proj-catchup-handler-error")
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
	for _, body := range []string{"first", "second"} {
		sendResp, err := sender.Send(protocol.Request{
			Cmd:     protocol.CmdSend,
			Name:    "alice",
			Message: body,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !sendResp.OK {
			t.Fatalf("send %q failed: %s", body, sendResp.Error)
		}
	}

	var firstAttempt []string
	err = NewBrokerListenerFactory().CatchUp(Watch{
		ProjectID: "proj-catchup-handler-error",
		AgentName: "alice",
		Source:    "hook",
	}, func(d Delivery) error {
		firstAttempt = append(firstAttempt, d.Body)
		if d.Body == "second" {
			return context.Canceled
		}
		return nil
	})
	if err == nil {
		t.Fatal("CatchUp() error = nil, want handler error")
	}
	if strings.Join(firstAttempt, ",") != "first,second" {
		t.Fatalf("first attempt deliveries = %v, want first, second", firstAttempt)
	}

	var secondAttempt []string
	if err := NewBrokerListenerFactory().CatchUp(Watch{
		ProjectID: "proj-catchup-handler-error",
		AgentName: "alice",
		Source:    "hook",
	}, func(d Delivery) error {
		secondAttempt = append(secondAttempt, d.Body)
		return nil
	}); err != nil {
		t.Fatalf("second CatchUp() error = %v", err)
	}
	if strings.Join(secondAttempt, ",") != "second" {
		t.Fatalf("second attempt deliveries = %v, want only second", secondAttempt)
	}
}

func TestBrokerListenerCatchUpDoesNotReleaseReservedPushToken(t *testing.T) {
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

	active := connectRuntimeClient(t, socketPath)
	assertPushTokenStillAccepted(t, socketPath, active, "alice-push", token, "CatchUp replay")
}

func TestReleasePushTokenForAgentSuppressesAlreadyRevokedTokenWarning(t *testing.T) {
	socketPath, cleanup := startRuntimeTestBroker(t, "proj-release-warning")
	defer cleanup()

	token, err := pushTokenForAgent(socketPath, "alice")
	if err != nil {
		t.Fatal(err)
	}

	releaser := connectRuntimeClient(t, socketPath)
	resp, err := releaser.Send(protocol.Request{
		Cmd:       protocol.CmdPushRelease,
		Name:      "alice",
		PushToken: token,
	})
	releaser.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("push release failed: %s: %s", resp.Code, resp.Error)
	}

	var logs bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previousOutput)

	releasePushTokenForAgent(socketPath, "alice", token)

	if strings.Contains(logs.String(), "warning: release push token") {
		t.Fatalf("unexpected warning for already-revoked token: %s", logs.String())
	}
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

func assertPushTokenStillAccepted(t *testing.T, socketPath string, active *client.Client, listenerName, token, context string) {
	t.Helper()

	active.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		reconnect := connectRuntimeClient(t, socketPath)
		resp, err := reconnect.Send(protocol.Request{
			Cmd:          protocol.CmdConnect,
			Name:         listenerName,
			PushListener: true,
			PushToken:    token,
		})
		reconnect.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.OK {
			return
		}
		if resp.Code == protocol.ErrForbidden {
			t.Fatalf("token was incorrectly released after %s: %s", context, resp.Error)
		}
		if resp.Code != protocol.ErrAlreadyConnected {
			t.Fatalf("reconnect response code = %q, want OK or %q", resp.Code, protocol.ErrAlreadyConnected)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting to reconnect with token preserved after %s", context)
}
