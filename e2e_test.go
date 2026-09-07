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

	"github.com/seungpyoson/waggle/cmd"
	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/client"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
)

// Every end-to-end broker runs under this project, so the canonical resolver
// produces the same socket for a predecessor and its successor.
const e2eProjectID = "e2e-test-project"

func TestE2E_TaskRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e in short mode")
	}

	env := newE2EEnv(t)
	broker := env.launch(t, "startup.log", "--initialize")

	assertCompetingStartsRejected(t, env.binary, broker.cmd, env.socket)
	socketPath := env.socket

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

// A signalled broker must release ownership cleanly, so the next incarnation
// of the same project can acquire the store it left behind.
func TestE2E_SigtermShutdownReleasesOwnership(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e in short mode")
	}

	env := newE2EEnv(t)
	first := env.launch(t, "first.log", "--initialize")
	firstExit := first.terminate(t)

	// The successor opens the released store; only a real release lets it serve.
	second := env.launch(t, "second.log")
	ctx, cancel := context.WithTimeout(t.Context(), config.Defaults.ShutdownTimeout+config.Defaults.StartupTimeout)
	defer cancel()
	stop := exec.CommandContext(ctx, env.binary, "stop")
	stop.Dir, stop.Env = env.project, second.cmd.Env
	out, err := stop.CombinedOutput()
	if err != nil || !strings.Contains(string(out), `"message": "broker stopped"`) {
		t.Fatalf("stop: %v\n%s", err, out)
	}
	// Success is checked at command return, before waiting for process exit.
	for _, path := range []string{env.socket, config.NewPaths(e2eProjectID).PID} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("stop claimed success before endpoint removal: %s %v", path, err)
		}
	}
	select {
	case <-second.exited:
	case <-ctx.Done():
		t.Fatal("stopped broker did not exit")
	}
	if second.err != nil {
		t.Fatalf("stopped broker exited with %v", second.err)
	}
	third := env.launch(t, "third.log")
	thirdExit := third.terminate(t)
	t.Logf("SIGTERM shutdown took %s and %s; stop returned after endpoint removal", firstExit, thirdExit)
}

// e2eEnv is a built entrypoint plus an isolated HOME whose canonical socket
// stays inside the 104-byte AF_UNIX path limit on macOS.
type e2eEnv struct {
	binary  string
	home    string
	project string
	socket  string
}

func newE2EEnv(t *testing.T) e2eEnv {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "waggle")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s\n%s", err, out)
	}
	// Keep the isolated HOME under the writable checkout, where the resolved
	// socket path is short enough for AF_UNIX.
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.MkdirTemp(root, ".h-")
	if err != nil {
		t.Fatalf("create temp home: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })

	// Use the canonical path resolver for the isolated project.
	t.Setenv("HOME", home)
	return e2eEnv{binary: binary, home: home, project: t.TempDir(), socket: config.NewPaths(e2eProjectID).Socket}
}

// launch starts the real foreground entrypoint, retains its failure
// diagnostics, and returns once the broker is serving.
func (e e2eEnv) launch(t *testing.T, logName string, args ...string) *e2eBroker {
	t.Helper()
	logPath := filepath.Join(e.home, logName)
	output, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { output.Close() })

	startCmd := exec.Command(e.binary, append([]string{"start", "--foreground"}, args...)...)
	startCmd.Dir = e.project
	startCmd.Env = append(os.Environ(), "HOME="+e.home, "WAGGLE_PROJECT_ID="+e2eProjectID)
	startCmd.Stdout, startCmd.Stderr = output, output
	if err := startCmd.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	broker := &e2eBroker{cmd: startCmd, logPath: logPath, exited: make(chan struct{})}
	go func() {
		broker.err = startCmd.Wait()
		close(broker.exited)
	}()
	t.Cleanup(func() {
		startCmd.Process.Kill()
		<-broker.exited
	})

	ticker := time.NewTicker(config.Defaults.ShutdownPollInterval)
	defer ticker.Stop()
	deadline := time.NewTimer(2 * config.Defaults.StartupTimeout)
	defer deadline.Stop()
	for {
		select {
		case <-broker.exited:
			t.Fatalf("foreground broker exited before readiness: %v\n%s", broker.err, broker.output())
		case <-deadline.C:
			t.Fatalf("foreground broker exceeded readiness deadline\n%s", broker.output())
		case <-ticker.C:
			conn, err := net.DialTimeout("unix", e.socket, config.Defaults.ConnectTimeout)
			if err == nil {
				conn.Close()
				return broker
			}
		}
	}
}

// e2eBroker is a running foreground broker. Reading err is safe only after
// exited closes.
type e2eBroker struct {
	cmd     *exec.Cmd
	logPath string
	exited  chan struct{}
	err     error
}

func (b *e2eBroker) output() string {
	data, _ := os.ReadFile(b.logPath)
	return string(data)
}

// terminate requires the first SIGTERM to drain and release within the
// configured shutdown budget, without reporting a missed deadline.
func (b *e2eBroker) terminate(t *testing.T) time.Duration {
	t.Helper()
	signalled := time.Now()
	if err := b.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal broker: %v", err)
	}
	budget := config.Defaults.ShutdownTimeout + 2*config.Defaults.StartupTimeout
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case <-b.exited:
	case <-timer.C:
		t.Fatalf("broker did not exit within %s of SIGTERM\n%s", budget, b.output())
	}
	elapsed := time.Since(signalled)
	if b.err != nil {
		t.Fatalf("broker exited %v after SIGTERM\n%s", b.err, b.output())
	}
	if out := b.output(); strings.Contains(out, cmd.ErrShutdownDeadlineMissed.Error()) {
		t.Fatalf("orderly shutdown reported a missed deadline\n%s", out)
	}
	return elapsed
}
