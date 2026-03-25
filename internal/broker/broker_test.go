package broker

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/client"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
)

func startTestBroker(t *testing.T) (string, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Use /tmp for socket to avoid path length issues
	sockPath := fmt.Sprintf("/tmp/waggle-test-%d.sock", time.Now().UnixNano())
	dbPath := fmt.Sprintf("%s/db", tmpDir)

	b, err := New(Config{SocketPath: sockPath, DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}

	go b.Serve()
	time.Sleep(100 * time.Millisecond)
	return sockPath, func() {
		b.Shutdown()
		os.Remove(sockPath)
	}
}

// Test 1: Full round trip — create, claim, complete
func TestBroker_FullRoundTrip_CreateClaimComplete(t *testing.T) {
	sockPath, cleanup := startTestBroker(t)
	defer cleanup()

	c, err := client.Connect(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Connect
	resp, _ := c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "worker-1"})
	if !resp.OK {
		t.Fatalf("connect failed: %s", resp.Error)
	}

	// Create task
	resp, _ = c.Send(protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(`{"desc":"test task"}`),
		Type:    "test",
	})
	if !resp.OK {
		t.Fatalf("create: %s", resp.Error)
	}

	// Claim task
	resp, _ = c.Send(protocol.Request{Cmd: protocol.CmdTaskClaim})
	if !resp.OK {
		t.Fatalf("claim: %s", resp.Error)
	}
	var claimData struct {
		ID         int64  `json:"ID"`
		ClaimToken string `json:"ClaimToken"`
	}
	json.Unmarshal(resp.Data, &claimData)
	t.Logf("Claimed task ID=%d, token=%s", claimData.ID, claimData.ClaimToken)

	// Complete task
	taskIDStr := fmt.Sprintf("%d", claimData.ID)
	resp, _ = c.Send(protocol.Request{
		Cmd:        protocol.CmdTaskComplete,
		TaskID:     taskIDStr,
		ClaimToken: claimData.ClaimToken,
		Result:     json.RawMessage(`{"done":true}`),
	})
	if !resp.OK {
		t.Fatalf("complete: %s (taskID=%s, token=%s)", resp.Error, taskIDStr, claimData.ClaimToken)
	}

	// Verify state
	resp, _ = c.Send(protocol.Request{
		Cmd:    protocol.CmdTaskGet,
		TaskID: taskIDStr,
	})
	var task struct{ State string `json:"State"` }
	json.Unmarshal(resp.Data, &task)
	if task.State != "completed" {
		t.Errorf("state = %q, want completed", task.State)
	}
}

// Test 2: Disconnect cleans up locks
func TestBroker_DisconnectCleansUpLocks(t *testing.T) {
	sockPath, cleanup := startTestBroker(t)
	defer cleanup()

	c, _ := client.Connect(sockPath)
	c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "w-1"})

	// Acquire lock
	resp, _ := c.Send(protocol.Request{Cmd: protocol.CmdLock, Resource: "file:main.go"})
	if !resp.OK {
		t.Fatalf("lock: %s", resp.Error)
	}

	// Disconnect
	c.Close()
	time.Sleep(50 * time.Millisecond)

	// Verify lock released
	c2, _ := client.Connect(sockPath)
	defer c2.Close()
	c2.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "w-2"})
	resp, _ = c2.Send(protocol.Request{Cmd: protocol.CmdLock, Resource: "file:main.go"})
	if !resp.OK {
		t.Errorf("lock should be available after disconnect: %s", resp.Error)
	}
}

// Test 3: Disconnect re-queues claimed tasks
func TestBroker_DisconnectRequeuesClaimedTasks(t *testing.T) {
	sockPath, cleanup := startTestBroker(t)
	defer cleanup()

	c, _ := client.Connect(sockPath)
	c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "w-1"})

	// Create and claim task
	c.Send(protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(`{"desc":"test"}`),
		Type:    "test",
	})
	resp, _ := c.Send(protocol.Request{Cmd: protocol.CmdTaskClaim})
	if !resp.OK {
		t.Fatalf("claim: %s", resp.Error)
	}

	// Disconnect
	c.Close()
	time.Sleep(50 * time.Millisecond)

	// Verify task re-queued
	c2, _ := client.Connect(sockPath)
	defer c2.Close()
	c2.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "w-2"})
	resp, _ = c2.Send(protocol.Request{Cmd: protocol.CmdTaskClaim})
	if !resp.OK {
		t.Errorf("task should be re-queued after disconnect: %s", resp.Error)
	}
}

// Test 3b: Clean disconnect does NOT requeue claimed tasks
func TestBroker_CleanDisconnectDoesNotRequeue(t *testing.T) {
	sockPath, cleanup := startTestBroker(t)
	defer cleanup()

	c, _ := client.Connect(sockPath)
	c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "w-1"})

	// Create and claim task
	c.Send(protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(`{"desc":"test"}`),
		Type:    "test",
	})
	resp, _ := c.Send(protocol.Request{Cmd: protocol.CmdTaskClaim})
	if !resp.OK {
		t.Fatalf("claim: %s", resp.Error)
	}

	// Clean disconnect (send disconnect command)
	c.Send(protocol.Request{Cmd: protocol.CmdDisconnect})
	c.Close()
	time.Sleep(50 * time.Millisecond)

	// Verify task NOT re-queued (should still be claimed)
	c2, _ := client.Connect(sockPath)
	defer c2.Close()
	c2.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "w-2"})
	resp, _ = c2.Send(protocol.Request{Cmd: protocol.CmdTaskClaim})
	if resp.OK {
		t.Error("task should NOT be re-queued after clean disconnect")
	}
}

// Test 3c: Events subscribe returns raw event JSON (not wrapped in Response)
func TestBroker_EventsSubscribeFormat(t *testing.T) {
	sockPath, cleanup := startTestBroker(t)
	defer cleanup()

	c, _ := client.Connect(sockPath)
	c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "subscriber"})

	// Subscribe to task.events
	resp, _ := c.Send(protocol.Request{Cmd: protocol.CmdSubscribe, Topic: "task.events"})
	if !resp.OK {
		t.Fatalf("subscribe: %s", resp.Error)
	}

	// Start reading events
	eventCh, err := c.ReadStream()
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}

	// Create a task to trigger an event
	c2, _ := client.Connect(sockPath)
	defer c2.Close()
	c2.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "creator"})
	c2.Send(protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(`{"test":true}`),
		Type:    "test",
	})

	// Read the event with timeout
	select {
	case evt := <-eventCh:
		if evt.Topic != "task.events" {
			t.Errorf("expected topic=task.events, got %q", evt.Topic)
		}
		if evt.Event != "task.created" {
			t.Errorf("expected event=task.created, got %q", evt.Event)
		}
		if len(evt.Data) == 0 {
			t.Error("expected event data")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

// Test 3d: Status includes task counts by state
func TestBroker_StatusIncludesTaskCounts(t *testing.T) {
	sockPath, cleanup := startTestBroker(t)
	defer cleanup()

	c, _ := client.Connect(sockPath)
	c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "w-1"})

	// Create tasks in different states
	c.Send(protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(`{"a":1}`),
	})
	c.Send(protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(`{"b":2}`),
	})
	// Claim one task
	c.Send(protocol.Request{Cmd: protocol.CmdTaskClaim})

	// Get status
	resp, _ := c.Send(protocol.Request{Cmd: protocol.CmdStatus})
	if !resp.OK {
		t.Fatalf("status: %s", resp.Error)
	}

	var status map[string]interface{}
	json.Unmarshal(resp.Data, &status)

	// Check for task counts
	tasks, ok := status["tasks"].(map[string]interface{})
	if !ok {
		t.Fatal("status should include tasks map")
	}

	pending := int(tasks["pending"].(float64))
	claimed := int(tasks["claimed"].(float64))

	if pending != 1 {
		t.Errorf("expected 1 pending task, got %d", pending)
	}
	if claimed != 1 {
		t.Errorf("expected 1 claimed task, got %d", claimed)
	}
}

// Test 4: Disconnect unsubscribes from events
func TestBroker_DisconnectUnsubscribesEvents(t *testing.T) {
	sockPath, cleanup := startTestBroker(t)
	defer cleanup()

	c, _ := client.Connect(sockPath)
	c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "w-1"})

	// Subscribe to topic
	resp, _ := c.Send(protocol.Request{Cmd: protocol.CmdSubscribe, Topic: "test.topic"})
	if !resp.OK {
		t.Fatalf("subscribe: %s", resp.Error)
	}

	// Disconnect
	c.Close()
	time.Sleep(50 * time.Millisecond)

	// Verify subscription removed (check via status or another mechanism)
	c2, _ := client.Connect(sockPath)
	defer c2.Close()
	c2.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "w-2"})
	resp, _ = c2.Send(protocol.Request{Cmd: protocol.CmdStatus})
	if !resp.OK {
		t.Fatalf("status: %s", resp.Error)
	}
	// Status should show 0 subscribers after disconnect
	var status struct{ Subscribers int `json:"subscribers"` }
	json.Unmarshal(resp.Data, &status)
	if status.Subscribers != 0 {
		t.Errorf("subscribers = %d, want 0 after disconnect", status.Subscribers)
	}
}

// Test 5: Task events auto-published on state transitions
func TestBroker_PublishesTaskEventsOnStateTransitions(t *testing.T) {
	sockPath, cleanup := startTestBroker(t)
	defer cleanup()

	c, _ := client.Connect(sockPath)
	c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "w-1"})

	// Subscribe to task.events
	resp, _ := c.Send(protocol.Request{Cmd: protocol.CmdSubscribe, Topic: "task.events"})
	if !resp.OK {
		t.Fatalf("subscribe: %s", resp.Error)
	}

	// Create task (should publish event)
	c2, _ := client.Connect(sockPath)
	defer c2.Close()
	c2.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "w-2"})
	c2.Send(protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(`{"desc":"test"}`),
		Type:    "test",
	})

	// Read event stream (should receive task.created event)
	eventChan, err := c.ReadStream()
	if err != nil {
		t.Fatalf("ReadStream: %v", err)
	}

	select {
	case evt := <-eventChan:
		if evt.Topic != "task.events" {
			t.Errorf("event topic = %q, want task.events", evt.Topic)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("expected task.created event, got none")
	}
}

// Test 6: Invalid request returns error
func TestBroker_InvalidJSONReturnsError(t *testing.T) {
	sockPath, cleanup := startTestBroker(t)
	defer cleanup()

	c, _ := client.Connect(sockPath)
	defer c.Close()
	c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "w-1"})

	// Send request with missing required field (name for connect)
	c2, _ := client.Connect(sockPath)
	defer c2.Close()
	resp, err := c2.Send(protocol.Request{Cmd: protocol.CmdConnect})
	if err != nil {
		t.Fatalf("send error: %v", err)
	}
	if resp.OK {
		t.Error("expected error for missing name, got OK")
	}
	if resp.Code != protocol.ErrInvalidRequest {
		t.Errorf("error code = %q, want %q", resp.Code, protocol.ErrInvalidRequest)
	}
}


// Test: Worker A disconnects, Worker B's claimed task should NOT be re-queued
func TestBroker_DisconnectOnlyRequeuesOwnTasks(t *testing.T) {
	sockPath, cleanup := startTestBroker(t)
	defer cleanup()

	// Worker A connects and claims task 1
	c1, _ := client.Connect(sockPath)
	c1.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "worker-a"})
	c1.Send(protocol.Request{Cmd: protocol.CmdTaskCreate, Payload: json.RawMessage(`{"desc":"task1"}`), Type: "test"})
	resp1, _ := c1.Send(protocol.Request{Cmd: protocol.CmdTaskClaim})
	var claim1 struct{ ID int64 `json:"ID"` }
	json.Unmarshal(resp1.Data, &claim1)

	// Worker B connects and claims task 2
	c2, _ := client.Connect(sockPath)
	c2.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "worker-b"})
	c2.Send(protocol.Request{Cmd: protocol.CmdTaskCreate, Payload: json.RawMessage(`{"desc":"task2"}`), Type: "test"})
	resp2, _ := c2.Send(protocol.Request{Cmd: protocol.CmdTaskClaim})
	var claim2 struct{ ID int64 `json:"ID"` }
	json.Unmarshal(resp2.Data, &claim2)

	// Worker A disconnects
	c1.Close()
	time.Sleep(50 * time.Millisecond)

	// Verify Worker B's task is still claimed
	resp, _ := c2.Send(protocol.Request{Cmd: protocol.CmdTaskGet, TaskID: fmt.Sprintf("%d", claim2.ID)})
	var task struct{ State string `json:"State"` }
	json.Unmarshal(resp.Data, &task)
	if task.State != "claimed" {
		t.Errorf("Worker B's task state = %q, want claimed (should NOT be re-queued when Worker A disconnects)", task.State)
	}

	c2.Close()
}

// Test 6: Input validation rejects invalid values
func TestBroker_InputValidation(t *testing.T) {
	sockPath, cleanup := startTestBroker(t)
	defer cleanup()

	c, _ := client.Connect(sockPath)
	c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "w-1"})

	// Test negative priority
	resp, _ := c.Send(protocol.Request{
		Cmd:      protocol.CmdTaskCreate,
		Payload:  json.RawMessage(`{"test":true}`),
		Priority: -1,
	})
	if resp.OK {
		t.Error("expected error for negative priority")
	}
	if resp.Code != protocol.ErrInvalidRequest {
		t.Errorf("expected code=%s, got %s", protocol.ErrInvalidRequest, resp.Code)
	}

	// Test priority > 100
	resp, _ = c.Send(protocol.Request{
		Cmd:      protocol.CmdTaskCreate,
		Payload:  json.RawMessage(`{"test":true}`),
		Priority: 101,
	})
	if resp.OK {
		t.Error("expected error for priority > 100")
	}

	// Test name too long (> 256 chars)
	longName := string(make([]byte, 257))
	for i := range longName {
		longName = longName[:i] + "a"
	}
	resp, _ = c.Send(protocol.Request{
		Cmd:  protocol.CmdConnect,
		Name: longName,
	})
	if resp.OK {
		t.Error("expected error for name > 256 chars")
	}
}

// === Buffer config tests ===

// Verify the broker session scanner uses config.Defaults.MaxMessageSize,
// not a hardcoded constant. This test sends a payload larger than Go's
// default bufio.Scanner limit (64KB) but within config.MaxMessageSize.
func TestBroker_LargePayloadRoundTrip(t *testing.T) {
	sockPath, cleanup := startTestBroker(t)
	defer cleanup()

	c, _ := client.Connect(sockPath)
	defer c.Close()
	c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "w-large"})

	payloadSize := 100 * 1024 // 100KB — above 64KB default, below 1MB config max
	bigPayload := strings.Repeat("x", payloadSize)
	resp, err := c.Send(protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(fmt.Sprintf(`{"data":"%s"}`, bigPayload)),
		Type:    "large",
	})
	if err != nil {
		t.Fatalf("send failed (buffer too small?): %v", err)
	}
	if !resp.OK {
		t.Fatalf("create with large payload should succeed: %s", resp.Error)
	}
}

// Verify session scanner buffer matches config — not a different hardcoded value.
func TestBroker_ScannerBufferMatchesConfig(t *testing.T) {
	expected := int64(1024 * 1024)
	if config.Defaults.MaxMessageSize != expected {
		t.Fatalf("config.Defaults.MaxMessageSize = %d, want %d", config.Defaults.MaxMessageSize, expected)
	}
}

// === Class B: Double cleanup — cleanup must be idempotent ===

func TestBroker_CleanDisconnectCleansUpOnce(t *testing.T) {
	sockPath, cleanup := startTestBroker(t)
	defer cleanup()

	c, _ := client.Connect(sockPath)
	c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "w-once"})

	// Acquire a lock, create+claim a task
	c.Send(protocol.Request{Cmd: protocol.CmdLock, Resource: "file:once.go"})
	c.Send(protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(`{"desc":"once-test"}`),
		Type:    "test",
	})
	c.Send(protocol.Request{Cmd: protocol.CmdTaskClaim})

	// Clean disconnect — triggers cleanup from deferred readLoop.
	// Both should complete without panic or double-close errors.
	resp, err := c.Send(protocol.Request{Cmd: protocol.CmdDisconnect})
	if err != nil {
		t.Fatalf("disconnect send failed: %v", err)
	}
	if !resp.OK {
		t.Fatalf("disconnect failed: %s", resp.Error)
	}
	c.Close()
	time.Sleep(100 * time.Millisecond)

	// Verify lock was released (cleanup ran at least once)
	c2, _ := client.Connect(sockPath)
	defer c2.Close()
	c2.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "w-verify"})
	resp, _ = c2.Send(protocol.Request{Cmd: protocol.CmdLock, Resource: "file:once.go"})
	if !resp.OK {
		t.Errorf("lock should be available after clean disconnect: %s", resp.Error)
	}
}

// === Class C: Typed error detection — isConnectionClosed should use errors.Is ===

func TestIsConnectionClosed_ClosedConn(t *testing.T) {
	err := &net.OpError{Op: "read", Err: net.ErrClosed}
	if !isConnectionClosed(err) {
		t.Error("should detect net.ErrClosed wrapped in OpError")
	}
}

func TestIsConnectionClosed_ConnReset(t *testing.T) {
	err := &net.OpError{Op: "read", Err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET}}
	if !isConnectionClosed(err) {
		t.Error("should detect ECONNRESET")
	}
}

func TestIsConnectionClosed_BrokenPipe(t *testing.T) {
	err := &net.OpError{Op: "write", Err: &os.SyscallError{Syscall: "write", Err: syscall.EPIPE}}
	if !isConnectionClosed(err) {
		t.Error("should detect EPIPE (broken pipe)")
	}
}

func TestIsConnectionClosed_NilError(t *testing.T) {
	if isConnectionClosed(nil) {
		t.Error("nil error should return false")
	}
}

func TestIsConnectionClosed_UnrelatedError(t *testing.T) {
	err := errors.New("something completely different")
	if isConnectionClosed(err) {
		t.Error("unrelated error should return false")
	}
}


// ========== Direct Messaging Tests (Task 43) ==========

// TestBroker_SendMessage — client1 sends to client2, client2 inbox returns it
func TestBroker_SendMessage(t *testing.T) {
	sockPath, cleanup := startTestBroker(t)
	defer cleanup()

	c1, _ := client.Connect(sockPath)
	defer c1.Close()
	c1.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "alice"})

	c2, _ := client.Connect(sockPath)
	defer c2.Close()
	c2.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "bob"})

	// Alice sends to Bob
	resp, _ := c1.Send(protocol.Request{
		Cmd:     protocol.CmdSend,
		Name:    "bob",
		Message: "hello from alice",
	})
	if !resp.OK {
		t.Fatalf("send failed: %s", resp.Error)
	}

	// Bob receives the pushed message (since Bob is online)
	pushResp, err := c2.Receive()
	if err != nil {
		t.Fatalf("failed to receive push: %v", err)
	}
	if !pushResp.OK {
		t.Fatalf("push response not OK: %s", pushResp.Error)
	}

	// Bob checks inbox
	resp, _ = c2.Send(protocol.Request{Cmd: protocol.CmdInbox})
	if !resp.OK {
		t.Fatalf("inbox failed: %s", resp.Error)
	}

	var messages []map[string]interface{}
	json.Unmarshal(resp.Data, &messages)
	if len(messages) != 1 {
		t.Fatalf("inbox len = %d, want 1", len(messages))
	}
	if messages[0]["body"] != "hello from alice" {
		t.Errorf("message body = %q, want 'hello from alice'", messages[0]["body"])
	}
	if messages[0]["from"] != "alice" {
		t.Errorf("message from = %q, want 'alice'", messages[0]["from"])
	}
}

// TestBroker_SendPushDelivery — client2 connected, receives push immediately on send
func TestBroker_SendPushDelivery(t *testing.T) {
	sockPath, cleanup := startTestBroker(t)
	defer cleanup()

	c1, _ := client.Connect(sockPath)
	defer c1.Close()
	c1.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "alice"})

	c2, _ := client.Connect(sockPath)
	defer c2.Close()
	c2.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "bob"})

	// Alice sends to Bob (who is online)
	// This happens in a goroutine to allow Bob to receive the push concurrently
	sendDone := make(chan bool)
	go func() {
		resp, _ := c1.Send(protocol.Request{
			Cmd:     protocol.CmdSend,
			Name:    "bob",
			Message: "hello",
		})
		if !resp.OK {
			t.Errorf("send failed: %s", resp.Error)
		}
		sendDone <- true
	}()

	// Bob should receive the pushed message
	// The push is an unsolicited Response that Bob needs to read
	pushResp, err := c2.Receive()
	if err != nil {
		t.Fatalf("failed to receive push: %v", err)
	}
	if !pushResp.OK {
		t.Fatalf("push response not OK: %s", pushResp.Error)
	}

	// Verify the pushed message has the right structure
	var pushData map[string]interface{}
	json.Unmarshal(pushResp.Data, &pushData)
	if pushData["type"] != "message" {
		t.Errorf("push type = %q, want 'message'", pushData["type"])
	}
	if pushData["from"] != "alice" {
		t.Errorf("push from = %q, want 'alice'", pushData["from"])
	}
	if pushData["body"] != "hello" {
		t.Errorf("push body = %q, want 'hello'", pushData["body"])
	}

	// Wait for send to complete
	<-sendDone

	// Exercise writeMu with concurrent operations:
	// Bob sends an inbox command while Alice sends another message
	// This creates concurrent writes to Bob's connection (push + response)
	go func() {
		c1.Send(protocol.Request{
			Cmd:     protocol.CmdSend,
			Name:    "bob",
			Message: "second message",
		})
	}()

	// Bob checks inbox concurrently
	resp, _ := c2.Send(protocol.Request{Cmd: protocol.CmdInbox})
	if !resp.OK {
		t.Fatalf("inbox failed: %s", resp.Error)
	}

	// Read the second push
	pushResp2, err := c2.Receive()
	if err != nil {
		t.Fatalf("failed to receive second push: %v", err)
	}
	if !pushResp2.OK {
		t.Fatalf("second push response not OK: %s", pushResp2.Error)
	}

	// Verify via inbox that messages have state 'pushed'
	resp, _ = c2.Send(protocol.Request{Cmd: protocol.CmdInbox})
	if !resp.OK {
		t.Fatalf("inbox failed: %s", resp.Error)
	}

	var messages []map[string]interface{}
	json.Unmarshal(resp.Data, &messages)
	if len(messages) != 2 {
		t.Fatalf("inbox len = %d, want 2", len(messages))
	}
	// Verify both messages have state 'pushed'
	for i, msg := range messages {
		if msg["state"] != "pushed" {
			t.Errorf("message[%d] state = %q, want 'pushed'", i, msg["state"])
		}
	}
}

// TestBroker_SendOfflineDelivery — send to offline name, name connects later, inbox has message
func TestBroker_SendOfflineDelivery(t *testing.T) {
	sockPath, cleanup := startTestBroker(t)
	defer cleanup()

	c1, _ := client.Connect(sockPath)
	defer c1.Close()
	c1.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "alice"})

	// Alice sends to Bob (who is offline)
	resp, _ := c1.Send(protocol.Request{
		Cmd:     protocol.CmdSend,
		Name:    "bob",
		Message: "hello offline",
	})
	if !resp.OK {
		t.Fatalf("send to offline should succeed: %s", resp.Error)
	}

	// Bob connects later
	c2, _ := client.Connect(sockPath)
	defer c2.Close()
	c2.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "bob"})

	// Bob checks inbox
	resp, _ = c2.Send(protocol.Request{Cmd: protocol.CmdInbox})
	if !resp.OK {
		t.Fatalf("inbox failed: %s", resp.Error)
	}

	var messages []map[string]interface{}
	json.Unmarshal(resp.Data, &messages)
	if len(messages) != 1 {
		t.Fatalf("inbox len = %d, want 1", len(messages))
	}
	if messages[0]["body"] != "hello offline" {
		t.Errorf("message body = %q, want 'hello offline'", messages[0]["body"])
	}
	// State should be 'queued' (not pushed)
	if messages[0]["state"] != "queued" {
		t.Errorf("message state = %q, want 'queued'", messages[0]["state"])
	}
}

// TestBroker_SendRequiresName — send with empty name returns INVALID_REQUEST
func TestBroker_SendRequiresName(t *testing.T) {
	sockPath, cleanup := startTestBroker(t)
	defer cleanup()

	c, _ := client.Connect(sockPath)
	defer c.Close()
	c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "alice"})

	resp, _ := c.Send(protocol.Request{
		Cmd:     protocol.CmdSend,
		Name:    "",
		Message: "hello",
	})
	if resp.OK {
		t.Fatal("send with empty name should fail")
	}
	if resp.Code != protocol.ErrInvalidRequest {
		t.Errorf("error code = %q, want %q", resp.Code, protocol.ErrInvalidRequest)
	}
}

// TestBroker_SendRequiresMessage — send with empty message returns INVALID_REQUEST
func TestBroker_SendRequiresMessage(t *testing.T) {
	sockPath, cleanup := startTestBroker(t)
	defer cleanup()

	c, _ := client.Connect(sockPath)
	defer c.Close()
	c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "alice"})

	resp, _ := c.Send(protocol.Request{
		Cmd:     protocol.CmdSend,
		Name:    "bob",
		Message: "",
	})
	if resp.OK {
		t.Fatal("send with empty message should fail")
	}
	if resp.Code != protocol.ErrInvalidRequest {
		t.Errorf("error code = %q, want %q", resp.Code, protocol.ErrInvalidRequest)
	}
}

// TestBroker_SendRequiresSession — send without connect returns NOT_CONNECTED
func TestBroker_SendRequiresSession(t *testing.T) {
	sockPath, cleanup := startTestBroker(t)
	defer cleanup()

	c, _ := client.Connect(sockPath)
	defer c.Close()

	// Try to send without connecting
	resp, _ := c.Send(protocol.Request{
		Cmd:     protocol.CmdSend,
		Name:    "bob",
		Message: "hello",
	})
	if resp.OK {
		t.Fatal("send without session should fail")
	}
	if resp.Code != protocol.ErrNotConnected {
		t.Errorf("error code = %q, want %q", resp.Code, protocol.ErrNotConnected)
	}
}

// TestBroker_MessagesSurviveRestart — send message, stop broker, start new broker (same DB), inbox returns message
func TestBroker_MessagesSurviveRestart(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	sockPath := fmt.Sprintf("/tmp/waggle-test-%d.sock", time.Now().UnixNano())
	dbPath := fmt.Sprintf("%s/db", tmpDir)

	// Start first broker
	b1, err := New(Config{SocketPath: sockPath, DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	go b1.Serve()
	time.Sleep(100 * time.Millisecond)

	// Send message
	c1, _ := client.Connect(sockPath)
	c1.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "alice"})
	c1.Send(protocol.Request{
		Cmd:     protocol.CmdSend,
		Name:    "bob",
		Message: "persistent message",
	})
	c1.Close()

	// Stop broker
	b1.Shutdown()
	time.Sleep(100 * time.Millisecond)

	// Start second broker (same DB)
	b2, err := New(Config{SocketPath: sockPath, DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	go b2.Serve()
	time.Sleep(100 * time.Millisecond)
	defer func() {
		b2.Shutdown()
		os.Remove(sockPath)
	}()

	// Check inbox
	c2, _ := client.Connect(sockPath)
	defer c2.Close()
	c2.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "bob"})
	resp, _ := c2.Send(protocol.Request{Cmd: protocol.CmdInbox})
	if !resp.OK {
		t.Fatalf("inbox failed: %s", resp.Error)
	}

	var messages []map[string]interface{}
	json.Unmarshal(resp.Data, &messages)
	if len(messages) != 1 {
		t.Fatalf("inbox len = %d, want 1", len(messages))
	}
	if messages[0]["body"] != "persistent message" {
		t.Errorf("message body = %q, want 'persistent message'", messages[0]["body"])
	}
}

// TestBroker_InboxPersistsAcrossReconnect — send, recipient disconnects, reconnects, inbox still has message
func TestBroker_InboxPersistsAcrossReconnect(t *testing.T) {
	sockPath, cleanup := startTestBroker(t)
	defer cleanup()

	c1, _ := client.Connect(sockPath)
	defer c1.Close()
	c1.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "alice"})

	c2, _ := client.Connect(sockPath)
	c2.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "bob"})

	// Alice sends to Bob
	c1.Send(protocol.Request{
		Cmd:     protocol.CmdSend,
		Name:    "bob",
		Message: "reconnect test",
	})

	// Bob disconnects
	c2.Close()
	time.Sleep(50 * time.Millisecond)

	// Bob reconnects
	c3, _ := client.Connect(sockPath)
	defer c3.Close()
	c3.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "bob"})

	// Check inbox
	resp, _ := c3.Send(protocol.Request{Cmd: protocol.CmdInbox})
	if !resp.OK {
		t.Fatalf("inbox failed: %s", resp.Error)
	}

	var messages []map[string]interface{}
	json.Unmarshal(resp.Data, &messages)
	if len(messages) != 1 {
		t.Fatalf("inbox len = %d, want 1", len(messages))
	}
	if messages[0]["body"] != "reconnect test" {
		t.Errorf("message body = %q, want 'reconnect test'", messages[0]["body"])
	}
}

// TestBroker_MultipleSendersOrdering — A sends to C, B sends to C, C's inbox has both in order
func TestBroker_MultipleSendersOrdering(t *testing.T) {
	sockPath, cleanup := startTestBroker(t)
	defer cleanup()

	c1, _ := client.Connect(sockPath)
	defer c1.Close()
	c1.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "alice"})

	c2, _ := client.Connect(sockPath)
	defer c2.Close()
	c2.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "bob"})

	c3, _ := client.Connect(sockPath)
	defer c3.Close()
	c3.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "charlie"})

	// Alice sends to Charlie
	c1.Send(protocol.Request{
		Cmd:     protocol.CmdSend,
		Name:    "charlie",
		Message: "from alice",
	})

	// Charlie receives the first pushed message
	pushResp1, err := c3.Receive()
	if err != nil {
		t.Fatalf("charlie failed to receive first push: %v", err)
	}
	if !pushResp1.OK {
		t.Fatalf("first push response not OK: %s", pushResp1.Error)
	}

	time.Sleep(10 * time.Millisecond)

	// Bob sends to Charlie
	c2.Send(protocol.Request{
		Cmd:     protocol.CmdSend,
		Name:    "charlie",
		Message: "from bob",
	})

	// Charlie receives the second pushed message
	pushResp2, err := c3.Receive()
	if err != nil {
		t.Fatalf("charlie failed to receive second push: %v", err)
	}
	if !pushResp2.OK {
		t.Fatalf("second push response not OK: %s", pushResp2.Error)
	}

	// Charlie checks inbox
	resp, _ := c3.Send(protocol.Request{Cmd: protocol.CmdInbox})
	if !resp.OK {
		t.Fatalf("inbox failed: %s", resp.Error)
	}

	var messages []map[string]interface{}
	json.Unmarshal(resp.Data, &messages)
	if len(messages) != 2 {
		t.Fatalf("inbox len = %d, want 2", len(messages))
	}
	if messages[0]["body"] != "from alice" {
		t.Errorf("messages[0] body = %q, want 'from alice'", messages[0]["body"])
	}
	if messages[1]["body"] != "from bob" {
		t.Errorf("messages[1] body = %q, want 'from bob'", messages[1]["body"])
	}
}

