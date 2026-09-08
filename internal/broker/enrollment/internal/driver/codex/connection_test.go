package codex

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/seungpyoson/waggle/internal/config"
)

func TestNativeCloseReasonCannotEnterBrokerDiagnostics(t *testing.T) {
	const private = "provider-private-response"
	dial := protocolPeer(t, func(ws *websocket.Conn) {
		peerRead(t, ws)
		_ = ws.Close(websocket.StatusPolicyViolation, private)
	})
	c := openPeer(t, dial, func(context.Context, Event) error { return nil })
	_, _, err := c.call(t.Context(), "thread/read", map[string]string{"threadId": "thread"})
	if err == nil || strings.Contains(err.Error(), private) {
		t.Fatalf("native response entered diagnostics: %v", err)
	}
}

// These are protocol fakes on net.Pipe. Both ends use the maintained WebSocket
// library and the production transport handshake. They do not prove OS socket access
// or live App Server semantics.
type pipeListener struct {
	connections chan net.Conn
	closed      chan struct{}
	once        sync.Once
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.connections:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}
func (l *pipeListener) Close() error { l.once.Do(func() { close(l.closed) }); return nil }
func (l *pipeListener) Addr() net.Addr {
	return &net.UnixAddr{Name: "/registered/server.sock", Net: "unix"}
}

func protocolPeer(t *testing.T, body func(*websocket.Conn)) func(context.Context, string, string) (net.Conn, error) {
	t.Helper()
	l := &pipeListener{connections: make(chan net.Conn), closed: make(chan struct{})}
	var handlers sync.WaitGroup
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlers.Add(1)
		defer handlers.Done()
		if r.Header.Get("Authorization") != "" {
			t.Error("unexpected native authentication")
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer ws.CloseNow()
		request := peerRead(t, ws)
		if request.Method != "initialize" {
			t.Error("first request was not initialize")
			return
		}
		peerWrite(t, ws, wireMessage{ID: request.ID, Result: json.RawMessage(`{"codexHome":"/native/config","platformFamily":"unix","platformOs":"macos","userAgent":"test-peer"}`)})
		if peerRead(t, ws).Method != "initialized" {
			t.Error("missing initialized notification")
			return
		}
		body(ws)
	})}
	served := make(chan struct{})
	go func() { defer close(served); server.Serve(l) }()
	t.Cleanup(func() { server.Close(); <-served; handlers.Wait() })
	return func(ctx context.Context, network, path string) (net.Conn, error) {
		if network != "unix" || path != "/registered/server.sock" {
			t.Error("connection switched endpoint or transport")
		}
		client, server := net.Pipe()
		select {
		case l.connections <- server:
			return client, nil
		case <-ctx.Done():
			client.Close()
			server.Close()
			return nil, ctx.Err()
		}
	}
}

func peerRead(t *testing.T, ws *websocket.Conn) wireMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	kind, data, err := ws.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var msg wireMessage
	if kind != websocket.MessageText || json.Unmarshal(data, &msg) != nil {
		t.Fatal("invalid test request")
	}
	return msg
}
func peerWrite(t *testing.T, ws *websocket.Conn, msg wireMessage) {
	t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := ws.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}
func openPeer(t *testing.T, dial func(context.Context, string, string) (net.Conn, error), observe func(context.Context, Event) error) *connection {
	t.Helper()
	c, err := openConnection(t.Context(), "/registered/server.sock", "test-build", config.NewNativeConfig(), dial, observe)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestOutOfOrderResponsesAndServerRequestsUseSeparateIdentitySpaces(t *testing.T) {
	events := make(chan Event, 1)
	dial := protocolPeer(t, func(ws *websocket.Conn) {
		a, b := peerRead(t, ws), peerRead(t, ws)
		peerWrite(t, ws, wireMessage{ID: a.ID, Method: "item/commandExecution/requestApproval", Params: json.RawMessage(`{"threadId":"recipient"}`)})
		peerWrite(t, ws, wireMessage{ID: b.ID, Result: b.Params})
		peerWrite(t, ws, wireMessage{ID: a.ID, Result: a.Params})
		_, _, _ = ws.Read(t.Context())
	})
	c := openPeer(t, dial, func(_ context.Context, e Event) error { events <- e; return nil })
	var wg sync.WaitGroup
	for _, value := range []string{"first", "second"} {
		wg.Go(func() {
			result, written, err := c.call(t.Context(), "thread/read", map[string]string{"threadId": value})
			if err != nil || !written {
				t.Errorf("request failed: %v", err)
				return
			}
			var got map[string]string
			if json.Unmarshal(result, &got) != nil || got["threadId"] != value {
				t.Error("response assigned to competing request")
			}
		})
	}
	wg.Wait()
	select {
	case e := <-events:
		if e.Method != "item/commandExecution/requestApproval" {
			t.Error("server request lost")
		}
	default:
		t.Error("server request treated as response")
	}
}

func TestCancellationAfterWriteClosesConnectionWithoutResubmission(t *testing.T) {
	submitted := make(chan struct{})
	var writes atomic.Int32
	dial := protocolPeer(t, func(ws *websocket.Conn) {
		peerRead(t, ws)
		writes.Add(1)
		close(submitted)
		_, _, _ = ws.Read(t.Context())
	})
	c := openPeer(t, dial, func(context.Context, Event) error { return nil })
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, written, err := c.call(ctx, "turn/start", map[string]string{"threadId": "recipient"})
		if !written || !errors.Is(err, context.Canceled) {
			t.Errorf("canceled submission lost uncertainty: written=%v err=%v", written, err)
		}
	}()
	<-submitted
	cancel()
	<-done
	_, written, err := c.call(t.Context(), "turn/start", map[string]string{"threadId": "recipient"})
	if err == nil || written || writes.Load() != 1 {
		t.Fatal("failed connection admitted another submission")
	}
}

func TestUnmatchedResponseFailsPendingRequest(t *testing.T) {
	dial := protocolPeer(t, func(ws *websocket.Conn) {
		peerRead(t, ws)
		peerWrite(t, ws, wireMessage{ID: json.RawMessage(`"unissued"`), Result: json.RawMessage(`{}`)})
		_, _, _ = ws.Read(t.Context())
	})
	c := openPeer(t, dial, func(context.Context, Event) error { return nil })
	_, written, err := c.call(t.Context(), "thread/read", map[string]string{"threadId": "recipient"})
	if !written || !errors.Is(err, ErrProtocol) {
		t.Fatalf("unmatched response accepted: %v", err)
	}
}

func TestCloseCancelsAndJoinsEventObserver(t *testing.T) {
	entered, finished := make(chan struct{}), make(chan struct{})
	dial := protocolPeer(t, func(ws *websocket.Conn) {
		peerWrite(t, ws, wireMessage{Method: "thread/status/changed", Params: json.RawMessage(`{}`)})
		_, _, _ = ws.Read(t.Context())
	})
	c := openPeer(t, dial, func(ctx context.Context, _ Event) error {
		close(entered)
		<-ctx.Done()
		close(finished)
		return ctx.Err()
	})
	<-entered
	c.Close()
	select {
	case <-finished:
	default:
		t.Fatal("close returned while observer remained active")
	}
}
