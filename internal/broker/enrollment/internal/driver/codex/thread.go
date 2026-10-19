package codex

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/seungpyoson/waggle/internal/broker/enrollment/internal/driver"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/messages"
)

var _ driver.Driver = (*thread)(nil)

// Probe reads native metadata; it never loads, resumes, forks or starts a
// thread. A stored ID without canAcceptDirectInput cannot become ready.
func (c *thread) Probe(ctx context.Context) (driver.Readiness, error) {
	target := c.target
	result, _, err := c.connection.call(ctx, "thread/read", struct {
		ThreadID string `json:"threadId"`
	}{target.Conversation})
	if err != nil {
		return driver.Readiness{}, err
	}
	var response struct {
		Thread struct {
			ID        string `json:"id"`
			CanAccept *bool  `json:"canAcceptDirectInput"`
			Status    struct {
				Type string `json:"type"`
			} `json:"status"`
		} `json:"thread"`
	}
	if json.Unmarshal(result, &response) != nil || response.Thread.ID != target.Conversation {
		return driver.Readiness{}, fmt.Errorf("App Server did not verify the exact enrolled thread")
	}
	thread := response.Thread
	view := driver.Readiness{Conversation: thread.ID, Evidence: "thread/read: " + thread.Status.Type, Availability: driver.Unavailable}
	switch thread.Status.Type {
	case "notLoaded", "systemError":
		return view, nil
	case "idle", "active":
	default:
		return driver.Readiness{}, fmt.Errorf("unsupported native thread availability")
	}
	if thread.CanAccept == nil || !*thread.CanAccept {
		view.Evidence = "thread/read: direct input unavailable"
		return view, nil
	}
	if thread.Status.Type == "idle" {
		view.Availability = driver.Idle
		view.Evidence = "thread/read: exact loaded thread permits direct input and is idle"
		return view, nil
	}
	// Read one latest turn's metadata, never transcript content.
	result, _, err = c.connection.call(ctx, "thread/turns/list", struct {
		ThreadID  string `json:"threadId"`
		Limit     int    `json:"limit"`
		Direction string `json:"sortDirection"`
		ItemsView string `json:"itemsView"`
	}{target.Conversation, config.AppServerActiveTurnPageSize, "desc", "notLoaded"})
	if err != nil {
		return driver.Readiness{}, err
	}
	var turns struct {
		Data []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if json.Unmarshal(result, &turns) != nil || len(turns.Data) != 1 || turns.Data[0].ID == "" || turns.Data[0].Status != "inProgress" {
		return driver.Readiness{}, fmt.Errorf("native activity changed or active turn identity is unavailable")
	}
	view.Availability, view.TurnID = driver.Busy, turns.Data[0].ID
	view.Evidence = "thread/read and thread/turns/list: exact loaded thread permits direct input with an identified active turn"
	return view, nil
}

// Submit chooses one operation from observed native state. A stale busy-turn
// precondition or lost response never switches to turn/start.
func (c *thread) Submit(ctx context.Context, envelope messages.Envelope) driver.Outcome {
	target := c.target
	if envelope.Message.Attempt == "" || envelope.Message.ID == "" || envelope.Message.Recipient != target.ID || envelope.Acknowledgement == "" {
		return driver.Outcome{Possession: driver.NotSubmitted, Evidence: "native submission requires the exact broker-authored attempt envelope"}
	}
	view, err := c.Probe(ctx)
	if err != nil {
		return driver.Outcome{Possession: driver.NotSubmitted, Evidence: "native metadata verification failed before input submission"}
	}
	if view.Availability == driver.Unavailable {
		return driver.Outcome{Possession: driver.NotSubmitted, Evidence: view.Evidence}
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return driver.Outcome{Possession: driver.NotSubmitted, Evidence: "broker envelope cannot be encoded"}
	}
	type textInput struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	params := struct {
		ThreadID       string      `json:"threadId"`
		Input          []textInput `json:"input"`
		MessageID      string      `json:"clientUserMessageId"`
		ExpectedTurnID string      `json:"expectedTurnId,omitempty"`
	}{ThreadID: target.Conversation, Input: []textInput{{Type: "text", Text: string(body)}}, MessageID: envelope.Message.Attempt}
	var method string
	switch view.Availability {
	case driver.Idle:
		method = "turn/start"
	case driver.Busy:
		method = "turn/steer"
		params.ExpectedTurnID = view.TurnID
	default:
		return driver.Outcome{Possession: driver.NotSubmitted, Evidence: "unsupported native readiness state"}
	}
	result, written, err := c.connection.call(ctx, method, params)
	if err != nil {
		if !written {
			return driver.Outcome{Possession: driver.NotSubmitted, Evidence: "input request was not admitted to the native transport"}
		}
		return driver.Outcome{Possession: driver.Uncertain, Evidence: "input write was attempted but no valid correlated native acceptance was observed"}
	}
	var turnID string
	switch view.Availability {
	case driver.Idle:
		var response struct {
			Turn struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		if json.Unmarshal(result, &response) == nil {
			turnID = response.Turn.ID
		}
	case driver.Busy:
		var response struct {
			TurnID string `json:"turnId"`
		}
		if json.Unmarshal(result, &response) == nil && response.TurnID == view.TurnID {
			turnID = response.TurnID
		}
	}
	if turnID == "" {
		return driver.Outcome{Possession: driver.Uncertain, Evidence: "native response lacks the required turn correlation"}
	}
	return driver.Outcome{Possession: driver.Accepted, ProviderRef: turnID, Evidence: method + ": correlated native input response; consumption remains unconfirmed"}
}
