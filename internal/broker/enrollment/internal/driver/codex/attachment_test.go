package codex

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync/atomic"
	"testing"

	"github.com/coder/websocket"
	"github.com/seungpyoson/waggle/internal/broker/enrollment/internal/driver"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/messages"
)

func readSubscription(t *testing.T, ws *websocket.Conn, threadID string) wireMessage {
	t.Helper()
	request := peerRead(t, ws)
	var params map[string]string
	if request.Method != "thread/subscribe" || json.Unmarshal(request.Params, &params) != nil || len(params) != 1 || params["threadId"] != threadID {
		t.Fatalf("attachment did not subscribe to the exact thread: method=%s params=%s", request.Method, request.Params)
	}
	return request
}

func answerSubscription(t *testing.T, ws *websocket.Conn, threadID string) {
	t.Helper()
	request := readSubscription(t, ws, threadID)
	result, err := json.Marshal(map[string]string{"threadId": threadID})
	if err != nil {
		t.Fatal(err)
	}
	peerWrite(t, ws, wireMessage{ID: request.ID, Result: result})
}

func attachPeer(t *testing.T, target messages.Enrollment, dial func(context.Context, string, string) (net.Conn, error)) driver.Driver {
	t.Helper()
	c, err := Attach(t.Context(), target, "test-build", config.NewNativeConfig(), dial, func(context.Context, Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return c
}

func TestAttachmentFailureClosesWithoutAlternateRequestOrRedial(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result json.RawMessage
		err    *rpcError
	}{
		{name: "unsupported subscription", err: &rpcError{Code: -32601, Message: "unsupported"}},
		{name: "missing confirmation", result: json.RawMessage(`{}`)},
		{name: "wrong thread", result: json.RawMessage(`{"threadId":"another-thread"}`)},
		{name: "invalid confirmation", result: json.RawMessage(`{"threadId":123}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target, _ := fixtureEnvelope()
			closed := make(chan struct{})
			dial := protocolPeer(t, func(ws *websocket.Conn) {
				defer close(closed)
				request := readSubscription(t, ws, target.Conversation)
				peerWrite(t, ws, wireMessage{ID: request.ID, Result: tc.result, Error: tc.err})
				if _, _, err := ws.Read(t.Context()); err == nil {
					t.Error("failed attachment issued another native request")
				}
			})
			var dials atomic.Int32
			counted := func(ctx context.Context, network, address string) (net.Conn, error) {
				dials.Add(1)
				return dial(ctx, network, address)
			}
			c, err := Attach(t.Context(), target, "test-build", config.NewNativeConfig(), counted, func(context.Context, Event) error { return nil })
			if c != nil {
				c.Close()
				t.Fatal("failed subscription returned a usable driver")
			}
			if err == nil || dials.Load() != 1 {
				t.Fatalf("failed subscription was accepted or redialed: dials=%d err=%v", dials.Load(), err)
			}
			<-closed
		})
	}
}

func TestAttachmentCancellationJoinsObserverWithoutRetry(t *testing.T) {
	target, _ := fixtureEnvelope()
	entered, finished := make(chan struct{}), make(chan struct{})
	dial := protocolPeer(t, func(ws *websocket.Conn) {
		readSubscription(t, ws, target.Conversation)
		peerWrite(t, ws, wireMessage{Method: "thread/status/changed", Params: json.RawMessage(`{"threadId":"thread"}`)})
		if _, _, err := ws.Read(t.Context()); err == nil {
			t.Error("canceled attachment issued another native request")
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		c, err := Attach(ctx, target, "test-build", config.NewNativeConfig(), dial, func(ctx context.Context, _ Event) error {
			close(entered)
			<-ctx.Done()
			close(finished)
			return ctx.Err()
		})
		if c != nil {
			c.Close()
			t.Error("canceled attachment returned a usable driver")
		}
		done <- err
	}()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("attachment lost cancellation: %v", err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("attachment returned before joining its observer")
	}
}

func TestAttachmentKeepsIdentityAndRejectsAnotherIncarnation(t *testing.T) {
	target, envelope := fixtureEnvelope()
	dial := protocolPeer(t, func(ws *websocket.Conn) {
		answerSubscription(t, ws, "thread")
		answerThread(t, ws, `{"thread":{"id":"thread","canAcceptDirectInput":true,"status":{"type":"idle"}}}`)
		if _, _, err := ws.Read(t.Context()); err == nil {
			t.Error("another incarnation reached the native transport")
		}
	})
	c := attachPeer(t, target, dial)
	target.Conversation = "another-thread"
	target.ID = "another-incarnation"
	view, err := c.Probe(t.Context())
	if err != nil || view.Conversation != "thread" || view.Availability != driver.Idle {
		t.Fatalf("caller changed attached identity: %+v %v", view, err)
	}
	envelope.Message.Recipient = target.ID
	if outcome := c.Submit(t.Context(), envelope); outcome.Possession != driver.NotSubmitted {
		t.Fatalf("another incarnation was submitted: %+v", outcome)
	}
}
