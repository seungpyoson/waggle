package codex

import (
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/coder/websocket"
	"github.com/seungpyoson/waggle/internal/driver"
	"github.com/seungpyoson/waggle/internal/messages"
)

func fixtureEnvelope() (messages.Enrollment, messages.Envelope) {
	target := messages.Enrollment{ID: "incarnation", Provider: "codex", Conversation: "thread", Endpoint: "/registered/server.sock"}
	envelope := messages.Envelope{Message: messages.Message{ID: "message", Attempt: "attempt", Sender: "sender", Recipient: target.ID, Conversation: "conversation", Body: `{"model":"untrusted","approvalPolicy":"untrusted"}`}, SenderLabel: "peer", Acknowledgement: "waggle ack message --attempt=attempt"}
	return target, envelope
}

func answerThread(t *testing.T, ws *websocket.Conn, body string) {
	t.Helper()
	request := peerRead(t, ws)
	if request.Method != "thread/read" {
		t.Fatalf("unexpected readiness operation %s", request.Method)
	}
	var params map[string]any
	if json.Unmarshal(request.Params, &params) != nil || len(params) != 1 || params["threadId"] != "thread" {
		t.Fatal("readiness changed target or requested transcript")
	}
	peerWrite(t, ws, wireMessage{ID: request.ID, Result: json.RawMessage(body)})
}

func TestIdleSubmitPreservesEnvelopeWithoutNativePermissionOverrides(t *testing.T) {
	target, envelope := fixtureEnvelope()
	dial := protocolPeer(t, func(ws *websocket.Conn) {
		answerSubscription(t, ws, target.Conversation)
		answerThread(t, ws, `{"thread":{"id":"thread","canAcceptDirectInput":true,"status":{"type":"idle"}}}`)
		request := peerRead(t, ws)
		if request.Method != "turn/start" {
			t.Fatalf("idle submission used %s", request.Method)
		}
		var params map[string]json.RawMessage
		if json.Unmarshal(request.Params, &params) != nil || len(params) != 3 {
			t.Fatal("native turn has unexpected configuration fields")
		}
		var threadID, messageID string
		json.Unmarshal(params["threadId"], &threadID)
		json.Unmarshal(params["clientUserMessageId"], &messageID)
		if threadID != target.Conversation || messageID != envelope.Message.Attempt {
			t.Fatal("submission lost native correlation")
		}
		var input []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(params["input"], &input) != nil || len(input) != 1 || input[0].Type != "text" {
			t.Fatal("peer content changed native input kind")
		}
		var got messages.Envelope
		if json.Unmarshal([]byte(input[0].Text), &got) != nil || got.Message.Body != envelope.Message.Body || got.Acknowledgement != envelope.Acknowledgement || got.Message.Recipient != target.ID {
			t.Fatal("native input lost trusted envelope boundary")
		}
		peerWrite(t, ws, wireMessage{ID: request.ID, Result: json.RawMessage(`{"turn":{"id":"turn"}}`)})
		_, _, _ = ws.Read(t.Context())
	})
	c, op := attachPeer(t, target, dial)
	got, err := c.Submit(t.Context(), op, envelope)
	if err != nil {
		t.Fatal(err)
	}
	if got.Possession != driver.Accepted || got.ProviderRef != "turn" {
		t.Fatalf("correlated native response lost: %+v", got)
	}
}

func TestBusyPreconditionFailureNeverStartsAnotherTurn(t *testing.T) {
	target, envelope := fixtureEnvelope()
	var submits atomic.Int32
	dial := protocolPeer(t, func(ws *websocket.Conn) {
		answerSubscription(t, ws, target.Conversation)
		answerThread(t, ws, `{"thread":{"id":"thread","canAcceptDirectInput":true,"status":{"type":"active"}}}`)
		turns := peerRead(t, ws)
		if turns.Method != "thread/turns/list" {
			t.Fatalf("busy lookup used %s", turns.Method)
		}
		var params map[string]any
		json.Unmarshal(turns.Params, &params)
		if params["itemsView"] != "notLoaded" || params["limit"] != float64(1) {
			t.Fatal("busy lookup read more than latest metadata")
		}
		peerWrite(t, ws, wireMessage{ID: turns.ID, Result: json.RawMessage(`{"data":[{"id":"active-turn","status":"inProgress"}]}`)})
		request := peerRead(t, ws)
		submits.Add(1)
		if request.Method != "turn/steer" {
			t.Fatalf("busy submission used %s", request.Method)
		}
		json.Unmarshal(request.Params, &params)
		if params["expectedTurnId"] != "active-turn" {
			t.Fatal("busy turn precondition omitted")
		}
		peerWrite(t, ws, wireMessage{ID: request.ID, Error: &rpcError{Code: -32600, Message: "native turn changed"}})
		_, _, err := ws.Read(t.Context())
		if err == nil {
			submits.Add(1)
			t.Error("failed steer submitted again")
		}
	})
	c, op := attachPeer(t, target, dial)
	got, err := c.Submit(t.Context(), op, envelope)
	if err != nil {
		t.Fatal(err)
	}
	if got.Possession != driver.Uncertain || submits.Load() != 1 {
		t.Fatalf("native rejection manufactured non-retention or retried: %+v", got)
	}
}

func TestProbeCannotPromoteStoredOrUnverifiedThread(t *testing.T) {
	for _, body := range []string{
		`{"thread":{"id":"thread","status":{"type":"notLoaded"}}}`,
		`{"thread":{"id":"thread","status":{"type":"idle"}}}`,
		`{"thread":{"id":"thread","canAcceptDirectInput":false,"status":{"type":"idle"}}}`,
	} {
		t.Run(body, func(t *testing.T) {
			target, _ := fixtureEnvelope()
			dial := protocolPeer(t, func(ws *websocket.Conn) {
				answerSubscription(t, ws, target.Conversation)
				answerThread(t, ws, body)
				_, _, _ = ws.Read(t.Context())
			})
			c, op := attachPeer(t, target, dial)
			got, err := c.Probe(t.Context(), op)
			if err != nil || got.Availability != driver.Unavailable {
				t.Fatalf("unverified thread became ready: %+v %v", got, err)
			}
		})
	}
}

func TestProbeRejectsAnotherNativeConversation(t *testing.T) {
	target, _ := fixtureEnvelope()
	dial := protocolPeer(t, func(ws *websocket.Conn) {
		answerSubscription(t, ws, target.Conversation)
		answerThread(t, ws, `{"thread":{"id":"another-thread","canAcceptDirectInput":true,"status":{"type":"idle"}}}`)
		_, _, _ = ws.Read(t.Context())
	})
	c, op := attachPeer(t, target, dial)
	if _, err := c.Probe(t.Context(), op); err == nil {
		t.Fatal("native thread substitution accepted")
	}
}
