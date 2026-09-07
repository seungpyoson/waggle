package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/brokerstate/statetest"
	"github.com/seungpyoson/waggle/internal/tasks"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/client"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
)

func shortBrokerSocketPath(t *testing.T, pattern string) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(cwd, pattern) // Keep sockets under the writable checkout and below the Unix path limit.
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Logf("cleanup temp socket dir: %v", err)
		}
	})
	return filepath.Join(dir, "broker.sock")
}

func startTestBroker(t *testing.T) (string, *Broker, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	sockPath := shortBrokerSocketPath(t, "waggle-test-*")
	dbPath := fmt.Sprintf("%s/db", tmpDir)

	cfg := config.NewBrokerConfig(config.BrokerEndpoints{Socket: sockPath, PID: sockPath + ".pid"})
	// These lifecycle fixtures must re-observe and recover lost wakes within
	// the startup test budget. Scheduling-default tests construct their own cfg.
	cfg.Messaging.IdleCheckInterval = cfg.Messaging.DiscoveryInterval
	cfg.Messaging.ProbeInterval = cfg.Messaging.DiscoveryInterval
	b, err := newOwnedTestBroker(t, dbPath, config.CreateStore, cfg)
	if err != nil {
		t.Fatal(err)
	}

	return sockPath, b, serveTestBroker(t, b)
}

func serveTestBroker(t *testing.T, b *Broker) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	serving := make(chan error, 1)
	go func() { serving <- b.Serve(ctx) }()
	probe := connectClient(t, b.config.Endpoints.Socket)
	defer probe.Close()
	if response := sendRequest(t, probe, protocol.Request{Cmd: protocol.CmdStatus}); !response.OK {
		t.Fatal(response.Error)
	}
	return func() {
		defer cancel()
		if err := b.Shutdown(context.Background()); err != nil {
			t.Error(err)
			return
		}
		if err := <-serving; err != nil {
			t.Error(err)
		}
	}
}

// native exposes the fixture mechanics this broker was constructed with.
func (b *Broker) native() *nativeFixture { return b.enrollment.Connector().(*nativeFixture) }

func connectClient(t *testing.T, sockPath string) *client.Client {
	t.Helper()
	c, err := client.Connect(sockPath, 5*time.Second)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	return c
}

func TestMustMarshal_PanicsOnMarshalError(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected mustMarshal to panic")
		}
		if !strings.Contains(fmt.Sprint(r), "mustMarshal:") {
			t.Fatalf("panic = %v, want mustMarshal prefix", r)
		}
	}()

	_ = mustMarshal(func() {})
}

// readStream starts reading events and fatals on error.
func readStream(t *testing.T, c *client.Client) <-chan protocol.Event {
	t.Helper()
	ch, err := c.ReadStream()
	if err != nil {
		t.Fatalf("ReadStream: %v", err)
	}
	return ch
}

// sendRequest sends a broker request and fatals on transport errors.
func sendRequest(t *testing.T, c *client.Client, req protocol.Request) *protocol.Response {
	t.Helper()
	resp, err := c.Send(req)
	if err != nil {
		t.Fatalf("Send(%s): %v", req.Cmd, err)
	}
	return resp
}

func receiveResponse(t *testing.T, c *client.Client) *protocol.Response {
	t.Helper()
	resp, err := c.Receive()
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	return &resp
}

func marshalJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return data
}

func unmarshalJSON(t *testing.T, data json.RawMessage, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
}

func unmarshalResponse(t *testing.T, resp any, v any) {
	t.Helper()
	var data json.RawMessage
	switch r := resp.(type) {
	case *protocol.Response:
		if r == nil {
			t.Fatal("expected non-nil response")
		}
		data = r.Data
	case protocol.Response:
		data = r.Data
	default:
		t.Fatalf("unsupported response type %T", resp)
	}
	if data == nil {
		t.Fatal("expected non-nil response data")
	}
	unmarshalJSON(t, data, v)
}

func TestBroker_FullRoundTrip_CreateClaimComplete(t *testing.T) {
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	c := connectClient(t, sockPath)
	defer c.Close()

	// Connect
	resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdConnect, Name: "worker-1"})
	if !resp.OK {
		t.Fatalf("connect failed: %s", resp.Error)
	}

	// Create task
	resp = sendRequest(t, c, protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(`{"desc":"test task"}`),
		Type:    "test",
	})
	if !resp.OK {
		t.Fatalf("create: %s", resp.Error)
	}

	// Claim task
	resp = sendRequest(t, c, protocol.Request{Cmd: protocol.CmdTaskClaim})
	if !resp.OK {
		t.Fatalf("claim: %s", resp.Error)
	}
	var claimData struct {
		ID         int64  `json:"ID"`
		ClaimToken string `json:"ClaimToken"`
	}
	unmarshalResponse(t, resp, &claimData)
	t.Logf("Claimed task ID=%d, token=%s", claimData.ID, claimData.ClaimToken)

	// Complete task
	taskIDStr := fmt.Sprintf("%d", claimData.ID)
	resp = sendRequest(t, c, protocol.Request{
		Cmd:        protocol.CmdTaskComplete,
		TaskID:     taskIDStr,
		ClaimToken: claimData.ClaimToken,
		Result:     json.RawMessage(`{"done":true}`),
	})
	if !resp.OK {
		t.Fatalf("complete: %s (taskID=%s, token=%s)", resp.Error, taskIDStr, claimData.ClaimToken)
	}

	// Verify state
	resp = sendRequest(t, c, protocol.Request{
		Cmd:    protocol.CmdTaskGet,
		TaskID: taskIDStr,
	})
	var task struct {
		State string `json:"State"`
	}
	unmarshalResponse(t, resp, &task)
	if task.State != "completed" {
		t.Errorf("state = %q, want completed", task.State)
	}
}

// Test 2: Disconnect cleans up locks
func TestBroker_DisconnectCleansUpLocks(t *testing.T) {
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	c := connectClient(t, sockPath)
	sendRequest(t, c, protocol.Request{Cmd: protocol.CmdConnect, Name: "w-1"})

	// Acquire lock
	resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdLock, Resource: "file:main.go"})
	if !resp.OK {
		t.Fatalf("lock: %s", resp.Error)
	}

	// Disconnect
	c.Close()
	time.Sleep(50 * time.Millisecond)

	// Verify lock released
	c2 := connectClient(t, sockPath)
	defer c2.Close()
	sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdConnect, Name: "w-2"})
	resp = sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdLock, Resource: "file:main.go"})
	if !resp.OK {
		t.Errorf("lock should be available after disconnect: %s", resp.Error)
	}
}

// Test 3: Disconnect re-queues claimed tasks
func TestBroker_DisconnectRequeuesClaimedTasks(t *testing.T) {
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	c := connectClient(t, sockPath)
	sendRequest(t, c, protocol.Request{Cmd: protocol.CmdConnect, Name: "w-1"})

	// Create and claim task
	sendRequest(t, c, protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(`{"desc":"test"}`),
		Type:    "test",
	})
	resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdTaskClaim})
	if !resp.OK {
		t.Fatalf("claim: %s", resp.Error)
	}

	// Disconnect
	c.Close()
	time.Sleep(50 * time.Millisecond)

	// Verify task re-queued
	c2 := connectClient(t, sockPath)
	defer c2.Close()
	sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdConnect, Name: "w-2"})
	resp = sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdTaskClaim})
	if !resp.OK {
		t.Errorf("task should be re-queued after disconnect: %s", resp.Error)
	}
}

// Test 3b: Clean disconnect does NOT requeue claimed tasks
func TestBroker_CleanDisconnectDoesNotRequeue(t *testing.T) {
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	c := connectClient(t, sockPath)
	sendRequest(t, c, protocol.Request{Cmd: protocol.CmdConnect, Name: "w-1"})

	// Create and claim task
	sendRequest(t, c, protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(`{"desc":"test"}`),
		Type:    "test",
	})
	resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdTaskClaim})
	if !resp.OK {
		t.Fatalf("claim: %s", resp.Error)
	}

	// Clean disconnect (send disconnect command)
	sendRequest(t, c, protocol.Request{Cmd: protocol.CmdDisconnect})
	c.Close()
	time.Sleep(50 * time.Millisecond)

	// Verify task NOT re-queued (should still be claimed)
	c2 := connectClient(t, sockPath)
	defer c2.Close()
	sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdConnect, Name: "w-2"})
	resp = sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdTaskClaim})
	if resp.OK {
		t.Error("task should NOT be re-queued after clean disconnect")
	}
}

// Test 3c: Events subscribe returns raw event JSON (not wrapped in Response)
func TestBroker_EventsSubscribeFormat(t *testing.T) {
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	c := connectClient(t, sockPath)
	sendRequest(t, c, protocol.Request{Cmd: protocol.CmdConnect, Name: "subscriber"})

	// Subscribe to task.events
	resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdSubscribe, Topic: "task.events"})
	if !resp.OK {
		t.Fatalf("subscribe: %s", resp.Error)
	}

	// Start reading events
	eventCh := readStream(t, c)

	// Create a task to trigger an event
	c2 := connectClient(t, sockPath)
	defer c2.Close()
	sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdConnect, Name: "creator"})
	sendRequest(t, c2, protocol.Request{
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
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	c := connectClient(t, sockPath)
	sendRequest(t, c, protocol.Request{Cmd: protocol.CmdConnect, Name: "w-1"})

	// Create tasks in different states
	sendRequest(t, c, protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(`{"a":1}`),
	})
	sendRequest(t, c, protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(`{"b":2}`),
	})
	// Claim one task
	sendRequest(t, c, protocol.Request{Cmd: protocol.CmdTaskClaim})

	// Get status
	resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdStatus})
	if !resp.OK {
		t.Fatalf("status: %s", resp.Error)
	}

	var status map[string]interface{}
	unmarshalResponse(t, resp, &status)

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
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	c := connectClient(t, sockPath)
	sendRequest(t, c, protocol.Request{Cmd: protocol.CmdConnect, Name: "w-1"})

	// Subscribe to topic
	resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdSubscribe, Topic: "test.topic"})
	if !resp.OK {
		t.Fatalf("subscribe: %s", resp.Error)
	}

	// Disconnect
	c.Close()
	time.Sleep(50 * time.Millisecond)

	// Verify subscription removed (check via status or another mechanism)
	c2 := connectClient(t, sockPath)
	defer c2.Close()
	sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdConnect, Name: "w-2"})
	resp = sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdStatus})
	if !resp.OK {
		t.Fatalf("status: %s", resp.Error)
	}
	// Status should show 0 subscribers after disconnect
	var status struct {
		Subscribers int `json:"subscribers"`
	}
	unmarshalResponse(t, resp, &status)
	if status.Subscribers != 0 {
		t.Errorf("subscribers = %d, want 0 after disconnect", status.Subscribers)
	}
}

// Test 5: Task events auto-published on state transitions
func TestBroker_PublishesTaskEventsOnStateTransitions(t *testing.T) {
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	c := connectClient(t, sockPath)
	sendRequest(t, c, protocol.Request{Cmd: protocol.CmdConnect, Name: "w-1"})

	// Subscribe to task.events
	resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdSubscribe, Topic: "task.events"})
	if !resp.OK {
		t.Fatalf("subscribe: %s", resp.Error)
	}

	// Create task (should publish event)
	c2 := connectClient(t, sockPath)
	defer c2.Close()
	sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdConnect, Name: "w-2"})
	sendRequest(t, c2, protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(`{"desc":"test"}`),
		Type:    "test",
	})

	// Read event stream (should receive task.created event)
	eventChan := readStream(t, c)

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
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	c := connectClient(t, sockPath)
	defer c.Close()
	sendRequest(t, c, protocol.Request{Cmd: protocol.CmdConnect, Name: "w-1"})

	// Send request with missing required field (name for connect)
	c2 := connectClient(t, sockPath)
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
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	// Worker A connects and claims task 1
	c1 := connectClient(t, sockPath)
	sendRequest(t, c1, protocol.Request{Cmd: protocol.CmdConnect, Name: "worker-a"})
	sendRequest(t, c1, protocol.Request{Cmd: protocol.CmdTaskCreate, Payload: json.RawMessage(`{"desc":"task1"}`), Type: "test"})
	resp1 := sendRequest(t, c1, protocol.Request{Cmd: protocol.CmdTaskClaim})
	var claim1 struct {
		ID int64 `json:"ID"`
	}
	unmarshalResponse(t, resp1, &claim1)

	// Worker B connects and claims task 2
	c2 := connectClient(t, sockPath)
	sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdConnect, Name: "worker-b"})
	sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdTaskCreate, Payload: json.RawMessage(`{"desc":"task2"}`), Type: "test"})
	resp2 := sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdTaskClaim})
	var claim2 struct {
		ID int64 `json:"ID"`
	}
	unmarshalResponse(t, resp2, &claim2)

	// Worker A disconnects
	c1.Close()
	time.Sleep(50 * time.Millisecond)

	// Verify Worker B's task is still claimed
	resp := sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdTaskGet, TaskID: fmt.Sprintf("%d", claim2.ID)})
	var task struct {
		State string `json:"State"`
	}
	unmarshalResponse(t, resp, &task)
	if task.State != "claimed" {
		t.Errorf("Worker B's task state = %q, want claimed (should NOT be re-queued when Worker A disconnects)", task.State)
	}

	c2.Close()
}

// Test 6: Input validation rejects invalid values
func TestBroker_InputValidation(t *testing.T) {
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	c := connectClient(t, sockPath)
	sendRequest(t, c, protocol.Request{Cmd: protocol.CmdConnect, Name: "w-1"})

	// Test negative priority
	resp := sendRequest(t, c, protocol.Request{
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
	resp = sendRequest(t, c, protocol.Request{
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
	resp = sendRequest(t, c, protocol.Request{
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
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	c := connectClient(t, sockPath)
	defer c.Close()
	sendRequest(t, c, protocol.Request{Cmd: protocol.CmdConnect, Name: "w-large"})

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
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	c := connectClient(t, sockPath)
	sendRequest(t, c, protocol.Request{Cmd: protocol.CmdConnect, Name: "w-once"})

	// Acquire a lock, create+claim a task
	sendRequest(t, c, protocol.Request{Cmd: protocol.CmdLock, Resource: "file:once.go"})
	sendRequest(t, c, protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(`{"desc":"once-test"}`),
		Type:    "test",
	})
	sendRequest(t, c, protocol.Request{Cmd: protocol.CmdTaskClaim})

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
	c2 := connectClient(t, sockPath)
	defer c2.Close()
	sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdConnect, Name: "w-verify"})
	resp = sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdLock, Resource: "file:once.go"})
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

func TestBroker_DuplicateNameRejected(t *testing.T) {
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	// Client A connects as "agent-dup"
	cA := connectClient(t, sockPath)
	defer cA.Close()
	respA := sendRequest(t, cA, protocol.Request{Cmd: protocol.CmdConnect, Name: "agent-dup"})
	if !respA.OK {
		t.Fatalf("client A connect failed: %s", respA.Error)
	}

	// Client B tries to connect as "agent-dup" — must be rejected
	cB := connectClient(t, sockPath)
	defer cB.Close()
	respB := sendRequest(t, cB, protocol.Request{Cmd: protocol.CmdConnect, Name: "agent-dup"})
	if respB.OK {
		t.Fatal("expected client B connect to fail, but got OK")
	}
	if respB.Code != protocol.ErrAlreadyConnected {
		t.Fatalf("expected code %s, got %s", protocol.ErrAlreadyConnected, respB.Code)
	}

	// A rejected connection cannot acquire the original worker's locks or
	// remove its registration during teardown.
	cB.Close()
	if resp := sendRequest(t, cA, protocol.Request{Cmd: protocol.CmdLock, Resource: "owned-file"}); !resp.OK {
		t.Fatal(resp.Error)
	}
	if resp := sendRequest(t, cA, protocol.Request{Cmd: protocol.CmdTaskCreate, Payload: json.RawMessage("{}")}); !resp.OK {
		t.Fatal(resp.Error)
	}
}

func TestBroker_CreateTaskWithTTL(t *testing.T) {
	sockPath, b, cleanup := startTestBroker(t)
	defer cleanup()

	c := connectClient(t, sockPath)
	defer c.Close()

	// Connect
	resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdConnect, Name: "cli"})
	if !resp.OK {
		t.Fatalf("connect failed: %s", resp.Error)
	}

	// Create task with TTL=60
	resp = sendRequest(t, c, protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(`{"desc":"ttl test"}`),
		Type:    "test",
		TTL:     60,
	})
	if !resp.OK {
		t.Fatalf("task create failed: %s", resp.Error)
	}

	// Verify TTL is stored
	var taskData struct {
		ID int64 `json:"id"`
	}
	unmarshalResponse(t, resp, &taskData)
	task, err := readTask(b, taskData.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.TTL != 60 {
		t.Errorf("expected TTL=60, got %d", task.TTL)
	}
}

// TestBroker_TaskTTLCheckerRuns verifies the TTL checker goroutine runs
func TestBroker_TaskTTLCheckerRuns(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	sockPath := shortBrokerSocketPath(t, "waggle-test-*")
	dbPath := fmt.Sprintf("%s/db", tmpDir)

	// Create broker with short task TTL check period
	brokerConfig := config.NewBrokerConfig(config.BrokerEndpoints{Socket: sockPath, PID: sockPath + ".pid"})
	brokerConfig.TaskTTLCheckPeriod = 500 * time.Millisecond
	b, err := newOwnedTestBroker(t, dbPath, config.CreateStore, brokerConfig)
	if err != nil {
		t.Fatal(err)
	}

	go b.Serve(t.Context())
	time.Sleep(100 * time.Millisecond)
	defer func() {
		b.Shutdown(context.Background())
	}()

	c := connectClient(t, sockPath)
	defer c.Close()

	// Connect
	resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdConnect, Name: "cli"})
	if !resp.OK {
		t.Fatalf("connect failed: %s", resp.Error)
	}

	// Create task with TTL=1 second
	resp = sendRequest(t, c, protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(`{"desc":"expires soon"}`),
		Type:    "test",
		TTL:     1,
	})
	if !resp.OK {
		t.Fatalf("task create failed: %s", resp.Error)
	}

	var taskData struct {
		ID int64 `json:"id"`
	}
	unmarshalResponse(t, resp, &taskData)
	taskID := taskData.ID

	// Wait for TTL to expire + checker to run
	time.Sleep(3 * time.Second)

	// Query state directly to prove goroutine ran
	task, err := readTask(b, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != "canceled" {
		t.Errorf("expected state=canceled, got %s", task.State)
	}
	if task.FailureReason != "ttl_expired" {
		t.Errorf("expected failure_reason=ttl_expired, got %s", task.FailureReason)
	}
}

// TestBroker_StatusQueueHealth verifies status includes queue health
func TestBroker_StatusQueueHealth(t *testing.T) {
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	c := connectClient(t, sockPath)
	defer c.Close()

	// Connect
	resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdConnect, Name: "cli"})
	if !resp.OK {
		t.Fatalf("connect failed: %s", resp.Error)
	}

	// Create 2 tasks
	for i := 0; i < 2; i++ {
		resp = sendRequest(t, c, protocol.Request{
			Cmd:     protocol.CmdTaskCreate,
			Payload: json.RawMessage(fmt.Sprintf(`{"desc":"task %d"}`, i)),
			Type:    "test",
		})
		if !resp.OK {
			t.Fatalf("task create failed: %s", resp.Error)
		}
	}

	// Get status
	resp = sendRequest(t, c, protocol.Request{Cmd: protocol.CmdStatus})
	if !resp.OK {
		t.Fatalf("status failed: %s", resp.Error)
	}

	// Verify queue_health is present
	var statusData map[string]interface{}
	unmarshalResponse(t, resp, &statusData)
	queueHealth, ok := statusData["queue_health"]
	if !ok {
		t.Fatal("expected queue_health in status")
	}

	health := queueHealth.(map[string]interface{})
	if health["pending_count"].(float64) != 2 {
		t.Errorf("expected pending_count=2, got %v", health["pending_count"])
	}
}

// TestBroker_TaskStaleEvent verifies task.stale event is published
func TestBroker_TaskStaleEvent(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	sockPath := shortBrokerSocketPath(t, "waggle-test-*")
	dbPath := fmt.Sprintf("%s/db", tmpDir)

	// Create broker with short task TTL check period and stale threshold
	brokerConfig := config.NewBrokerConfig(config.BrokerEndpoints{Socket: sockPath, PID: sockPath + ".pid"})
	brokerConfig.TaskTTLCheckPeriod = 500 * time.Millisecond
	brokerConfig.TaskStaleThreshold = 1 * time.Second
	b, err := newOwnedTestBroker(t, dbPath, config.CreateStore, brokerConfig)
	if err != nil {
		t.Fatal(err)
	}

	go b.Serve(t.Context())
	time.Sleep(100 * time.Millisecond)
	defer func() {
		b.Shutdown(context.Background())
	}()

	// Subscriber client
	c1 := connectClient(t, sockPath)
	defer c1.Close()

	// Connect subscriber
	resp := sendRequest(t, c1, protocol.Request{Cmd: protocol.CmdConnect, Name: "subscriber"})
	if !resp.OK {
		t.Fatalf("connect failed: %s", resp.Error)
	}

	// Subscribe to task.events
	resp = sendRequest(t, c1, protocol.Request{
		Cmd:   protocol.CmdSubscribe,
		Topic: "task.events",
	})
	if !resp.OK {
		t.Fatalf("subscribe failed: %s", resp.Error)
	}

	// Start reading events
	eventCh := readStream(t, c1)

	// Creator client (separate connection to avoid protocol race)
	c2 := connectClient(t, sockPath)
	defer c2.Close()

	sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdConnect, Name: "creator"})

	// Create task (will become stale after 1 second)
	resp = sendRequest(t, c2, protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(`{"desc":"stale task"}`),
		Type:    "test",
	})
	if !resp.OK {
		t.Fatalf("task create failed: %s", resp.Error)
	}

	// Wait for task.stale event with timeout
	timeout := time.After(5 * time.Second)
	var staleEvent protocol.Event
	foundStale := false

	for !foundStale {
		select {
		case evt := <-eventCh:
			if evt.Event == "task.stale" {
				staleEvent = evt
				foundStale = true
			}
		case <-timeout:
			t.Fatal("timeout waiting for task.stale event")
		}
	}

	// Verify the stale event
	if staleEvent.Event != "task.stale" {
		t.Errorf("expected event=task.stale, got %s", staleEvent.Event)
	}

	// Parse the event data
	var data map[string]interface{}
	unmarshalJSON(t, staleEvent.Data, &data)

	// Verify stale_count > 0
	staleCount, ok := data["stale_count"].(float64)
	if !ok || staleCount <= 0 {
		t.Errorf("expected stale_count > 0, got %v", data["stale_count"])
	}

	// Verify oldest_age_seconds > 0
	oldestAge, ok := data["oldest_age_seconds"].(float64)
	if !ok || oldestAge <= 0 {
		t.Errorf("expected oldest_age_seconds > 0, got %v", data["oldest_age_seconds"])
	}
}

func TestBroker_CreateTaskWithInvalidTTL(t *testing.T) {
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	c := connectClient(t, sockPath)
	defer c.Close()
	sendRequest(t, c, protocol.Request{Cmd: protocol.CmdConnect, Name: "cli"})

	// Negative TTL
	resp := sendRequest(t, c, protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(`{"desc":"neg ttl"}`),
		Type:    "test",
		TTL:     -1,
	})
	if resp.OK {
		t.Fatal("expected error for negative TTL")
	}
	if resp.Code != protocol.ErrInvalidRequest {
		t.Errorf("expected code=%s, got %s", protocol.ErrInvalidRequest, resp.Code)
	}

	// Exceeds max TTL
	resp = sendRequest(t, c, protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(`{"desc":"big ttl"}`),
		Type:    "test",
		TTL:     config.Defaults.MaxTaskTTL + 1,
	})
	if resp.OK {
		t.Fatal("expected error for TTL exceeding max")
	}
	if resp.Code != protocol.ErrInvalidRequest {
		t.Errorf("expected code=%s, got %s", protocol.ErrInvalidRequest, resp.Code)
	}
}

// ========== Issue #56: Custom Event Payload Wrapping Tests ==========

// TestBroker_CustomEventHasTopic — subscribe to "chat.demo", publish {"msg":"hi"}, verify event.Topic == "chat.demo"
func TestBroker_CustomEventHasTopic(t *testing.T) {
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	c1 := connectClient(t, sockPath)
	defer c1.Close()
	sendRequest(t, c1, protocol.Request{Cmd: protocol.CmdConnect, Name: "subscriber"})
	resp := sendRequest(t, c1, protocol.Request{Cmd: protocol.CmdSubscribe, Topic: "chat.demo"})
	if !resp.OK {
		t.Fatalf("subscribe: %s", resp.Error)
	}
	eventCh := readStream(t, c1)

	c2 := connectClient(t, sockPath)
	defer c2.Close()
	sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdConnect, Name: "publisher"})
	sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdPublish, Topic: "chat.demo", Message: `{"msg":"hi"}`})

	select {
	case evt := <-eventCh:
		if evt.Topic != "chat.demo" {
			t.Errorf("event.Topic = %q, want %q", evt.Topic, "chat.demo")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

// TestBroker_CustomEventHasTimestamp — verify event.TS is non-empty RFC3339
func TestBroker_CustomEventHasTimestamp(t *testing.T) {
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	c1 := connectClient(t, sockPath)
	defer c1.Close()
	sendRequest(t, c1, protocol.Request{Cmd: protocol.CmdConnect, Name: "subscriber"})
	sendRequest(t, c1, protocol.Request{Cmd: protocol.CmdSubscribe, Topic: "chat.demo"})
	eventCh := readStream(t, c1)

	c2 := connectClient(t, sockPath)
	defer c2.Close()
	sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdConnect, Name: "publisher"})
	sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdPublish, Topic: "chat.demo", Message: `{"msg":"hi"}`})

	select {
	case evt := <-eventCh:
		if evt.TS == "" {
			t.Error("event.TS should be non-empty")
		}
		if _, err := time.Parse(time.RFC3339, evt.TS); err != nil {
			t.Errorf("event.TS = %q is not valid RFC3339: %v", evt.TS, err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

// TestBroker_CustomEventHasData — publish {"key":"value"}, verify it appears in event.Data
func TestBroker_CustomEventHasData(t *testing.T) {
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	c1 := connectClient(t, sockPath)
	defer c1.Close()
	sendRequest(t, c1, protocol.Request{Cmd: protocol.CmdConnect, Name: "subscriber"})
	sendRequest(t, c1, protocol.Request{Cmd: protocol.CmdSubscribe, Topic: "chat.demo"})
	eventCh := readStream(t, c1)

	c2 := connectClient(t, sockPath)
	defer c2.Close()
	sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdConnect, Name: "publisher"})
	sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdPublish, Topic: "chat.demo", Message: `{"key":"value"}`})

	select {
	case evt := <-eventCh:
		if len(evt.Data) == 0 {
			t.Error("event.Data should be non-empty")
		}
		var data map[string]string
		unmarshalJSON(t, evt.Data, &data)
		if data["key"] != "value" {
			t.Errorf("event.Data[key] = %q, want %q", data["key"], "value")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

// TestBroker_CustomEventName — verify event.Event == "custom"
func TestBroker_CustomEventName(t *testing.T) {
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	c1 := connectClient(t, sockPath)
	defer c1.Close()
	sendRequest(t, c1, protocol.Request{Cmd: protocol.CmdConnect, Name: "subscriber"})
	sendRequest(t, c1, protocol.Request{Cmd: protocol.CmdSubscribe, Topic: "chat.demo"})
	eventCh := readStream(t, c1)

	c2 := connectClient(t, sockPath)
	defer c2.Close()
	sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdConnect, Name: "publisher"})
	sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdPublish, Topic: "chat.demo", Message: `{"msg":"hi"}`})

	select {
	case evt := <-eventCh:
		if evt.Event != "custom" {
			t.Errorf("event.Event = %q, want %q", evt.Event, "custom")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

// TestBroker_TaskEventsRegression — create a task, subscriber gets full task.created event
func TestBroker_TaskEventsRegression(t *testing.T) {
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	c1 := connectClient(t, sockPath)
	defer c1.Close()
	sendRequest(t, c1, protocol.Request{Cmd: protocol.CmdConnect, Name: "subscriber"})
	resp := sendRequest(t, c1, protocol.Request{Cmd: protocol.CmdSubscribe, Topic: "task.events"})
	if !resp.OK {
		t.Fatalf("subscribe: %s", resp.Error)
	}
	eventCh := readStream(t, c1)

	c2 := connectClient(t, sockPath)
	defer c2.Close()
	sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdConnect, Name: "creator"})
	sendRequest(t, c2, protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(`{"desc":"regression"}`),
		Type:    "test",
	})

	select {
	case evt := <-eventCh:
		if evt.Topic != "task.events" {
			t.Errorf("event.Topic = %q, want task.events", evt.Topic)
		}
		if evt.Event != "task.created" {
			t.Errorf("event.Event = %q, want task.created", evt.Event)
		}
		if len(evt.Data) == 0 {
			t.Error("event.Data should be non-empty for task events")
		}
		if evt.TS == "" {
			t.Error("event.TS should be non-empty")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestBroker_CustomEventInvalidJSON(t *testing.T) {
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	c := connectClient(t, sockPath)
	defer c.Close()
	sendRequest(t, c, protocol.Request{Cmd: protocol.CmdConnect, Name: "publisher"})

	resp := sendRequest(t, c, protocol.Request{
		Cmd:     protocol.CmdPublish,
		Topic:   "chat.demo",
		Message: "not json",
	})
	if resp.OK {
		t.Fatal("publish with invalid JSON should fail")
	}
	if resp.Code != protocol.ErrInvalidRequest {
		t.Errorf("error code = %q, want %q", resp.Code, protocol.ErrInvalidRequest)
	}
}

// TestBroker_CustomEventEmptyMessage — publish with empty message, event has null data
func TestBroker_CustomEventEmptyMessage(t *testing.T) {
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	c1 := connectClient(t, sockPath)
	defer c1.Close()
	sendRequest(t, c1, protocol.Request{Cmd: protocol.CmdConnect, Name: "subscriber"})
	sendRequest(t, c1, protocol.Request{Cmd: protocol.CmdSubscribe, Topic: "chat.demo"})
	eventCh := readStream(t, c1)

	c2 := connectClient(t, sockPath)
	defer c2.Close()
	sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdConnect, Name: "publisher"})
	resp := sendRequest(t, c2, protocol.Request{Cmd: protocol.CmdPublish, Topic: "chat.demo", Message: ""})
	if !resp.OK {
		t.Fatalf("publish with empty message should succeed: %s", resp.Error)
	}

	select {
	case evt := <-eventCh:
		if len(evt.Data) != 0 {
			t.Errorf("event.Data should be empty for empty message, got %s", evt.Data)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func newOwnedTestBroker(t *testing.T, database string, action config.StoreAction, cfg config.BrokerConfig) (*Broker, error) {
	t.Helper()
	owner, err := brokerstate.Acquire(t.Context(), config.NewOwnershipConfig(database, action), statetest.Process{})
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() {
		owner.BeginShutdown(nil)
		if err := owner.Wait(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if action == config.CreateStore {
		if err := Initialize(t.Context(), owner); err != nil {
			return nil, err
		}
	}
	return newWithConnector(t.Context(), owner, cfg, config.NewNativeConfig(), newNativeFixture())
}
func readTask(b *Broker, id int64) (task *tasks.Task, err error) {
	err = statetest.Write(b.owner, func(tx *brokerstate.WriteTx) error { task, err = tasks.NewStore(tx).Get(id); return err })
	return
}
