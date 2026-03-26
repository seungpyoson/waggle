package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/client"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
)

func TestE2E_TaskRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e in short mode")
	}

	// Build binary
	tmpBin := filepath.Join(t.TempDir(), "waggle")
	build := exec.Command("go", "build", "-o", tmpBin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s\n%s", err, out)
	}

	// Create project directory
	project := t.TempDir()

	// Create temp HOME in /tmp to avoid socket path length issues
	// Unix domain sockets have a 104-byte path limit on macOS
	tmpHome, err := os.MkdirTemp("/tmp", "waggle-test-*")
	if err != nil {
		t.Fatalf("create temp home: %v", err)
	}
	t.Cleanup(func() {
		os.RemoveAll(tmpHome)
	})

	// Start broker in background
	startCmd := exec.Command(tmpBin, "start", "--foreground")
	startCmd.Dir = project
	startCmd.Env = append(os.Environ(), "HOME="+tmpHome, "WAGGLE_PROJECT_ID=e2e-test-project")
	if err := startCmd.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}

	// Cleanup: stop broker
	t.Cleanup(func() {
		if startCmd.Process != nil {
			startCmd.Process.Kill()
			startCmd.Wait()
		}
	})

	// Poll socket readiness
	// Socket is at ~/waggle/sockets/<hash>/broker.sock where hash is based on project path
	// We need to compute the hash to find the socket
	socketDir := filepath.Join(tmpHome, ".waggle", "sockets")
	var socketPath string

	// Wait for socket directory to be created
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(socketDir)
		if err == nil && len(entries) > 0 {
			// Found the hash directory
			socketPath = filepath.Join(socketDir, entries[0].Name(), "broker.sock")
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if socketPath == "" {
		t.Fatalf("socket directory not created after 5s")
	}

	// Now poll for socket readiness
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Verify socket is ready
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("socket not ready after 5s: %v", err)
	}
	conn.Close()

	// Connect to broker and create session
	c, err := client.Connect(socketPath, config.Defaults.ConnectTimeout)
	if err != nil {
		t.Fatalf("connect to broker: %v", err)
	}
	defer c.Close()

	// Establish session
	resp, err := c.Send(protocol.Request{
		Cmd:  protocol.CmdConnect,
		Name: "e2e-test",
	})
	if err != nil {
		t.Fatalf("connect request: %v", err)
	}
	if !resp.OK {
		t.Fatalf("connect failed: %s", resp.Error)
	}

	// Create task
	resp, err = c.Send(protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(`{"desc":"e2e test"}`),
		Type:    "test",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if !resp.OK {
		t.Fatalf("create failed: %s", resp.Error)
	}

	// Extract task ID
	var createData struct {
		ID int64 `json:"ID"`
	}
	if err := json.Unmarshal(resp.Data, &createData); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	taskID := fmt.Sprintf("%d", createData.ID)

	// Claim task
	resp, err = c.Send(protocol.Request{
		Cmd:  protocol.CmdTaskClaim,
		Type: "test",
	})
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}
	if !resp.OK {
		t.Fatalf("claim failed: %s", resp.Error)
	}

	// Extract claim token
	var claimData struct {
		ID         int64  `json:"ID"`
		ClaimToken string `json:"ClaimToken"`
	}
	if err := json.Unmarshal(resp.Data, &claimData); err != nil {
		t.Fatalf("unmarshal claim response: %v", err)
	}

	// Complete task
	resp, err = c.Send(protocol.Request{
		Cmd:        protocol.CmdTaskComplete,
		TaskID:     taskID,
		ClaimToken: claimData.ClaimToken,
		Result:     json.RawMessage(`{"status":"done"}`),
	})
	if err != nil {
		t.Fatalf("complete task: %v", err)
	}
	if !resp.OK {
		t.Fatalf("complete failed: %s", resp.Error)
	}

	// Verify task is completed
	resp, err = c.Send(protocol.Request{
		Cmd:    protocol.CmdTaskGet,
		TaskID: taskID,
	})
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if !resp.OK {
		t.Fatalf("get failed: %s", resp.Error)
	}

	var getData struct {
		State  string `json:"State"`
		Result string `json:"Result"`
	}
	if err := json.Unmarshal(resp.Data, &getData); err != nil {
		t.Fatalf("unmarshal get response: %v", err)
	}

	if getData.State != "completed" {
		t.Fatalf("expected State=completed, got %s", getData.State)
	}

	// Verify result was stored
	var resultData struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(getData.Result), &resultData); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if resultData.Status != "done" {
		t.Fatalf("expected status=done, got %s", resultData.Status)
	}
}


// TestE2E_DirectMessaging — full flow: alice and bob exchange messages
func TestE2E_DirectMessaging(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e in short mode")
	}

	// Build binary
	tmpBin := filepath.Join(t.TempDir(), "waggle")
	build := exec.Command("go", "build", "-o", tmpBin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s\n%s", err, out)
	}

	// Create project directory
	project := t.TempDir()

	// Create temp HOME in /tmp to avoid socket path length issues
	tmpHome, err := os.MkdirTemp("/tmp", "waggle-test-*")
	if err != nil {
		t.Fatalf("create temp home: %v", err)
	}
	t.Cleanup(func() {
		os.RemoveAll(tmpHome)
	})

	// Start broker in background
	startCmd := exec.Command(tmpBin, "start", "--foreground")
	startCmd.Dir = project
	startCmd.Env = append(os.Environ(), "HOME="+tmpHome, "WAGGLE_PROJECT_ID=e2e-test-messaging")
	if err := startCmd.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}

	// Cleanup: stop broker
	t.Cleanup(func() {
		if startCmd.Process != nil {
			startCmd.Process.Kill()
			startCmd.Wait()
		}
	})

	// Poll socket readiness
	socketDir := filepath.Join(tmpHome, ".waggle", "sockets")
	var socketPath string

	// Wait for socket directory to be created
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(socketDir)
		if err == nil && len(entries) > 0 {
			socketPath = filepath.Join(socketDir, entries[0].Name(), "broker.sock")
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if socketPath == "" {
		t.Fatalf("socket directory not created after 5s")
	}

	// Now poll for socket readiness
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Connect as alice
	alice, err := client.Connect(socketPath, config.Defaults.ConnectTimeout)
	if err != nil {
		t.Fatalf("alice connect: %v", err)
	}
	defer alice.Close()

	resp, _ := alice.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "alice"})
	if !resp.OK {
		t.Fatalf("alice connect failed: %s", resp.Error)
	}

	// Connect as bob
	bob, err := client.Connect(socketPath, config.Defaults.ConnectTimeout)
	if err != nil {
		t.Fatalf("bob connect: %v", err)
	}
	defer bob.Close()

	resp, _ = bob.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "bob"})
	if !resp.OK {
		t.Fatalf("bob connect failed: %s", resp.Error)
	}

	// Bob sends to alice
	resp, _ = bob.Send(protocol.Request{
		Cmd:     protocol.CmdSend,
		Name:    "alice",
		Message: "hello",
	})
	if !resp.OK {
		t.Fatalf("bob send failed: %s", resp.Error)
	}

	// Alice receives the pushed message (since alice is online)
	pushResp, err := alice.Receive()
	if err != nil {
		t.Fatalf("alice failed to receive push: %v", err)
	}
	if !pushResp.OK {
		t.Fatalf("alice push response not OK: %s", pushResp.Error)
	}

	// Alice checks inbox
	resp, _ = alice.Send(protocol.Request{Cmd: protocol.CmdInbox})
	if !resp.OK {
		t.Fatalf("alice inbox failed: %s", resp.Error)
	}

	var aliceMessages []struct {
		From string `json:"from"`
		Body string `json:"body"`
	}
	json.Unmarshal(resp.Data, &aliceMessages)
	if len(aliceMessages) != 1 {
		t.Fatalf("alice inbox len = %d, want 1", len(aliceMessages))
	}
	if aliceMessages[0].From != "bob" {
		t.Errorf("message from = %q, want bob", aliceMessages[0].From)
	}
	if aliceMessages[0].Body != "hello" {
		t.Errorf("message body = %q, want hello", aliceMessages[0].Body)
	}

	// Alice sends to bob
	resp, _ = alice.Send(protocol.Request{
		Cmd:     protocol.CmdSend,
		Name:    "bob",
		Message: "got it",
	})
	if !resp.OK {
		t.Fatalf("alice send failed: %s", resp.Error)
	}

	// Bob receives the pushed message (since bob is online)
	pushResp, err = bob.Receive()
	if err != nil {
		t.Fatalf("bob failed to receive push: %v", err)
	}
	if !pushResp.OK {
		t.Fatalf("bob push response not OK: %s", pushResp.Error)
	}

	// Bob checks inbox
	resp, _ = bob.Send(protocol.Request{Cmd: protocol.CmdInbox})
	if !resp.OK {
		t.Fatalf("bob inbox failed: %s", resp.Error)
	}

	var bobMessages []struct {
		From string `json:"from"`
		Body string `json:"body"`
	}
	json.Unmarshal(resp.Data, &bobMessages)
	if len(bobMessages) != 1 {
		t.Fatalf("bob inbox len = %d, want 1", len(bobMessages))
	}
	if bobMessages[0].From != "alice" {
		t.Errorf("message from = %q, want alice", bobMessages[0].From)
	}
	if bobMessages[0].Body != "got it" {
		t.Errorf("message body = %q, want 'got it'", bobMessages[0].Body)
	}
}

// TestE2E_AckLifecycle — full flow with broker binary
func TestE2E_AckLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e in short mode")
	}

	// Build binary
	tmpBin := filepath.Join(t.TempDir(), "waggle")
	build := exec.Command("go", "build", "-o", tmpBin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s\n%s", err, out)
	}

	// Create project directory
	project := t.TempDir()

	// Create temp HOME in /tmp to avoid socket path length issues
	tmpHome, err := os.MkdirTemp("/tmp", "waggle-test-*")
	if err != nil {
		t.Fatalf("create temp home: %v", err)
	}
	t.Cleanup(func() {
		os.RemoveAll(tmpHome)
	})

	// Start broker in background
	startCmd := exec.Command(tmpBin, "start", "--foreground")
	startCmd.Dir = project
	startCmd.Env = append(os.Environ(), "HOME="+tmpHome, "WAGGLE_PROJECT_ID=e2e-test-ack")
	if err := startCmd.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}

	// Cleanup: stop broker
	t.Cleanup(func() {
		if startCmd.Process != nil {
			startCmd.Process.Kill()
			startCmd.Wait()
		}
	})

	// Poll socket readiness
	socketDir := filepath.Join(tmpHome, ".waggle", "sockets")
	var socketPath string

	// Wait for socket directory to be created
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(socketDir)
		if err == nil && len(entries) > 0 {
			socketPath = filepath.Join(socketDir, entries[0].Name(), "broker.sock")
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if socketPath == "" {
		t.Fatalf("socket directory not created after 5s")
	}

	// Now poll for socket readiness
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Connect as alice
	alice, err := client.Connect(socketPath, config.Defaults.ConnectTimeout)
	if err != nil {
		t.Fatalf("alice connect: %v", err)
	}
	defer alice.Close()

	resp, _ := alice.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "alice"})
	if !resp.OK {
		t.Fatalf("alice connect failed: %s", resp.Error)
	}

	// Connect as bob
	bob, err := client.Connect(socketPath, config.Defaults.ConnectTimeout)
	if err != nil {
		t.Fatalf("bob connect: %v", err)
	}
	defer bob.Close()

	resp, _ = bob.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "bob"})
	if !resp.OK {
		t.Fatalf("bob connect failed: %s", resp.Error)
	}

	// Alice sends to bob with --await-ack (runs in background goroutine)
	sendDone := make(chan *protocol.Response)
	go func() {
		resp, _ := alice.Send(protocol.Request{
			Cmd:      protocol.CmdSend,
			Name:     "bob",
			Message:  "start",
			AwaitAck: true,
			Timeout:  30,
		})
		sendDone <- resp
	}()

	// Bob receives push
	pushResp, err := bob.Receive()
	if err != nil {
		t.Fatalf("bob failed to receive push: %v", err)
	}
	if !pushResp.OK {
		t.Fatalf("bob push response not OK: %s", pushResp.Error)
	}

	var pushData struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(pushResp.Data, &pushData)

	// Bob checks inbox (message appears; state = seen)
	resp, _ = bob.Send(protocol.Request{Cmd: protocol.CmdInbox})
	if !resp.OK {
		t.Fatalf("bob inbox failed: %s", resp.Error)
	}

	var messages []struct {
		ID    int64  `json:"id"`
		From  string `json:"from"`
		Body  string `json:"body"`
		State string `json:"state"`
	}
	json.Unmarshal(resp.Data, &messages)
	if len(messages) != 1 {
		t.Fatalf("inbox len = %d, want 1", len(messages))
	}
	if messages[0].Body != "start" {
		t.Errorf("message body = %q, want 'start'", messages[0].Body)
	}

	// Bob acks the message
	resp, _ = bob.Send(protocol.Request{
		Cmd:       protocol.CmdAck,
		MessageID: pushData.ID,
	})
	if !resp.OK {
		t.Fatalf("bob ack failed: %s", resp.Error)
	}

	// Alice's send should return OK
	select {
	case sendResp := <-sendDone:
		if !sendResp.OK {
			t.Errorf("alice send should succeed after ack: %s", sendResp.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("alice send did not unblock after ack")
	}

	// Presence shows alice and bob
	resp, _ = alice.Send(protocol.Request{Cmd: protocol.CmdPresence})
	if !resp.OK {
		t.Fatalf("presence failed: %s", resp.Error)
	}

	var agents []map[string]string
	json.Unmarshal(resp.Data, &agents)
	if len(agents) != 2 {
		t.Fatalf("presence len = %d, want 2", len(agents))
	}

	names := make(map[string]bool)
	for _, agent := range agents {
		names[agent["name"]] = true
	}
	if !names["alice"] || !names["bob"] {
		t.Error("presence should include alice and bob")
	}
}

