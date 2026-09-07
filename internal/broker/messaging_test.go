package broker

import (
	"context"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/brokerstate/statetest"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/messages"
	"github.com/seungpyoson/waggle/internal/protocol"
)

// Only native mechanics are simulated. Enrollment, receive-binding, selection,
// submission and authenticated receipts run through the production broker.
func boundFixture(t *testing.T, b *Broker, label string) messages.Result {
	t.Helper()
	e := enrollFixture(t, b, label)
	e.Enrollment = waitEnrollment(t, b, e.Enrollment.ID, "bound")
	return e
}

func enrollFixture(t *testing.T, b *Broker, label string) messages.Result {
	t.Helper()
	c := connectClient(t, b.config.Endpoints.Socket)
	defer c.Close()
	resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdEnroll, Enrollment: &protocol.EnrollmentInput{Provider: "codex", Conversation: label, Endpoint: "/registered/app-server.sock", Label: label}})
	if !resp.OK {
		t.Fatal(resp.Error)
	}
	var enrolled protocol.EnrollmentResponse
	unmarshalResponse(t, resp, &enrolled)
	return messages.Result{Enrollment: messages.Enrollment{ID: enrolled.Incarnation, State: enrolled.State}, Credential: enrolled.Credential}
}

func waitEnrollment(t *testing.T, b *Broker, id, state string) messages.Enrollment {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), config.Defaults.StartupTimeout)
	defer cancel()
	tick := time.NewTicker(config.Defaults.StartupPollInterval)
	defer tick.Stop()
	for {
		var e messages.Enrollment
		err := statetest.Write(b.owner, func(tx *brokerstate.WriteTx) error {
			var err error
			e, err = messages.NewStore(tx, b.config.Messaging, time.Now()).Enrollment(id)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if e.State == state {
			return e
		}
		select {
		case <-ctx.Done():
			t.Fatalf("enrollment state=%s, want %s; evidence=%s", e.State, state, e.Evidence)
		case <-tick.C:
		}
	}
}

func TestMessagingRPCAuthenticatesEveryCallAndDoesNotUseConnectionNames(t *testing.T) {
	socket, b, shutdown := startTestBroker(t)
	defer shutdown()
	a := boundFixture(t, b, "a")
	recipient := boundFixture(t, b, "recipient")
	c := connectClient(t, socket)
	defer c.Close()
	request := protocol.Request{Cmd: protocol.CmdSend, Recipient: recipient.Enrollment.ID, Credential: a.Credential, Message: "first", IdempotencyKey: "request", Within: time.Minute, Hops: 2}
	response := sendRequest(t, c, request)
	if !response.OK {
		t.Fatal(response.Error)
	}
	var stored messages.Message
	unmarshalResponse(t, response, &stored)
	if stored.Possession != "stored" || stored.Sender != a.Enrollment.ID || stored.Attempt != "" {
		t.Fatalf("enqueue claimed delivery or guessed sender: %+v", stored)
	}
	request.Credential = ""
	request.Name = "a"
	request.IdempotencyKey = "forgery"
	if sendRequest(t, c, request).OK {
		t.Fatal("name substituted for enrollment credential")
	}
	d := b.native().receive(t)
	if d.Message.ID != stored.ID || d.Acknowledgement == "" {
		t.Fatal("first native input lost message identity or acknowledgement instruction")
	}
	if sendRequest(t, c, protocol.Request{Cmd: protocol.CmdAck, Name: "recipient", MessageID: stored.ID, AttemptID: d.Message.Attempt}).OK {
		t.Fatal("caller-named acknowledgement accepted")
	}
	if sendRequest(t, c, protocol.Request{Cmd: protocol.CmdAck, Credential: a.Credential, MessageID: stored.ID, AttemptID: d.Message.Attempt}).OK {
		t.Fatal("sender forged recipient receipt")
	}
	if response = sendRequest(t, c, protocol.Request{Cmd: protocol.CmdInbox, Credential: recipient.Credential}); !response.OK {
		t.Fatal(response.Error)
	}
	var inbox []messages.Message
	unmarshalResponse(t, response, &inbox)
	if len(inbox) != 1 || inbox[0].Possession == "consumed" {
		t.Fatal("inbox manufactured consumption")
	}
	if response = sendRequest(t, c, protocol.Request{Cmd: protocol.CmdAck, Credential: recipient.Credential, MessageID: stored.ID, AttemptID: d.Message.Attempt}); !response.OK {
		t.Fatal(response.Error)
	}
	var consumed messages.Message
	unmarshalResponse(t, response, &consumed)
	if consumed.Possession != "consumed" {
		t.Fatal("authenticated receipt was not recorded")
	}
}

func TestMessagingRPCPendingDirectFailureAndExplicitOfflineEnqueue(t *testing.T) {
	socket, b, shutdown := startTestBroker(t)
	defer shutdown()
	a := boundFixture(t, b, "sender")
	b.native().block("pending", true)
	pending := enrollFixture(t, b, "pending")
	c := connectClient(t, socket)
	defer c.Close()
	req := protocol.Request{Cmd: protocol.CmdSend, Recipient: pending.Enrollment.ID, Credential: a.Credential, Message: "body", IdempotencyKey: "pending", Within: time.Minute, Hops: 2}
	if sendRequest(t, c, req).OK {
		t.Fatal("direct send accepted an unverified target")
	}
	b.native().block("pending", false)
	waitEnrollment(t, b, pending.Enrollment.ID, "bound")
	b.native().block("pending", true)
	waitEnrollment(t, b, pending.Enrollment.ID, "disconnected")
	if sendRequest(t, c, req).OK {
		t.Fatal("direct send silently queued offline")
	}
	req.Cmd = protocol.CmdEnqueue
	resp := sendRequest(t, c, req)
	if !resp.OK {
		t.Fatal(resp.Error)
	}
	var queued messages.Message
	unmarshalResponse(t, resp, &queued)
	if queued.Possession != "stored" || queued.Attempt != "" {
		t.Fatal("offline enqueue reported native acceptance")
	}
	b.native().block("pending", false)
	waitEnrollment(t, b, pending.Enrollment.ID, "bound")
	if received := b.native().receive(t); received.Message.ID != queued.ID {
		t.Fatal("canonical queue did not resume the stored message")
	}
}
