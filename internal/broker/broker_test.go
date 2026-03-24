package broker

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/client"
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


