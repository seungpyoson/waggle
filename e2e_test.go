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
	c, err := client.Connect(socketPath)
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

