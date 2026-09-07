package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/brokerstate"
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

	// Keep the isolated HOME under the writable checkout.
	// Unix domain sockets have a 104-byte path limit on macOS
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpHome, err := os.MkdirTemp(root, ".h-")
	if err != nil {
		t.Fatalf("create temp home: %v", err)
	}
	t.Cleanup(func() {
		os.RemoveAll(tmpHome)
	})

	// Use the canonical path resolver for the isolated project.
	t.Setenv("HOME", tmpHome)
	socketPath := config.NewPaths("e2e-test-project").Socket
	outputPath := filepath.Join(tmpHome, "startup.log")
	output, err := os.Create(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { output.Close() })

	// Start the real foreground entrypoint and retain its failure diagnostics.
	startCmd := exec.Command(tmpBin, "start", "--foreground", "--initialize")
	startCmd.Dir = project
	startCmd.Env = append(os.Environ(), "HOME="+tmpHome, "WAGGLE_PROJECT_ID=e2e-test-project")
	startCmd.Stdout, startCmd.Stderr = output, output
	if err := startCmd.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}

	exited := make(chan struct{})
	var processErr error
	go func() {
		processErr = startCmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		startCmd.Process.Kill()
		<-exited
	})
	ticker := time.NewTicker(config.Defaults.StartupPollInterval)
	defer ticker.Stop()
	deadline := time.NewTimer(2 * config.Defaults.StartupTimeout)
	defer deadline.Stop()
ready:
	for {
		select {
		case <-exited:
			data, _ := os.ReadFile(outputPath)
			t.Fatalf("foreground broker exited before readiness: %v\n%s", processErr, data)
		case <-deadline.C:
			data, _ := os.ReadFile(outputPath)
			t.Fatalf("foreground broker exceeded readiness deadline\n%s", data)
		case <-ticker.C:
			conn, err := net.DialTimeout("unix", socketPath, config.Defaults.ConnectTimeout)
			if err == nil {
				conn.Close()
				break ready
			}
		}
	}

	assertCompetingStartsRejected(t, tmpBin, startCmd, socketPath)

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

// Exercise the built foreground entrypoint while its predecessor is healthy
// and while SIGSTOP makes every health probe unresponsive.
func assertCompetingStartsRejected(t *testing.T, binary string, running *exec.Cmd, socket string) {
	t.Helper()
	original, err := os.Lstat(socket)
	if err != nil {
		t.Fatal(err)
	}
	start := func() {
		ctx, cancel := context.WithTimeout(t.Context(), 2*config.Defaults.StartupTimeout)
		defer cancel()
		contender := exec.CommandContext(ctx, binary, "start", "--foreground")
		contender.Env, contender.Dir = running.Env, running.Dir
		output, err := contender.CombinedOutput()
		if ctx.Err() != nil {
			t.Fatalf("competing startup exceeded its deadline: %s", output)
		}
		if err == nil {
			t.Fatal("second broker acquired a live owner's database")
		}
		if !strings.Contains(string(output), brokerstate.ErrOwnerAlive.Error()) {
			t.Fatalf("unexpected startup failure: %s", output)
		}
		current, err := os.Lstat(socket)
		if err != nil || !os.SameFile(original, current) {
			t.Fatalf("contender changed the live socket: %v", err)
		}
	}
	start()
	if err := running.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	defer running.Process.Signal(syscall.SIGCONT)
	start()
}
