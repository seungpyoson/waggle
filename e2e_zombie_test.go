package main

import (
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// computeHash computes the FNV-64a hash used by internal/config/config.go
// to derive socket and data directory paths from a project ID.
func computeHash(projectID string) string {
	h := fnv.New64a()
	h.Write([]byte(projectID))
	return fmt.Sprintf("%012x", h.Sum64()&0xffffffffffff)
}

// setupZombie creates a zombie broker: a process (the test itself) that
// listens on the correct socket path but never accepts connections.
// It writes the current PID to the PID file.
// Returns the socket listener (caller must close) and a cleanup func.
func setupZombie(t *testing.T, tmpHome, projectID string) (net.Listener, func()) {
	t.Helper()

	hash := computeHash(projectID)

	sockDir := filepath.Join(tmpHome, ".waggle", "sockets", hash)
	dataDir := filepath.Join(tmpHome, ".waggle", "data", hash)

	if err := os.MkdirAll(sockDir, 0700); err != nil {
		t.Fatalf("mkdir sockDir: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		t.Fatalf("mkdir dataDir: %v", err)
	}

	sockPath := filepath.Join(sockDir, "broker.sock")
	pidPath := filepath.Join(dataDir, "waggle.pid")

	// Listen but never accept — this is the zombie
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("zombie listen: %v", err)
	}

	// Write current test process PID as if this were the broker
	pid := os.Getpid()
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", pid)), 0600); err != nil {
		ln.Close()
		t.Fatalf("write pid: %v", err)
	}

	cleanup := func() {
		ln.Close()
		os.Remove(sockPath)
		os.Remove(pidPath)
	}
	return ln, cleanup
}

// buildBinary compiles the waggle binary into a temp dir and returns its path.
func buildBinary(t *testing.T) string {
	t.Helper()
	// Place binary in /tmp to avoid long path issues
	tmpBin, err := os.MkdirTemp("/tmp", "waggle-bin-*")
	if err != nil {
		t.Fatalf("mkdirtemp for bin: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpBin) })

	binPath := filepath.Join(tmpBin, "waggle")
	build := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s\n%s", err, out)
	}
	return binPath
}

// TestE2E_ZombieAutoRecovery verifies that when a zombie broker exists
// (listening but not accepting), waggle sessions detects it, warns on stderr,
// auto-recovers, and returns valid JSON.
func TestE2E_ZombieAutoRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e")
	}

	tmpBin := buildBinary(t)

	tmpHome, err := os.MkdirTemp("/tmp", "waggle-zombie-*")
	if err != nil {
		t.Fatalf("mkdirtemp home: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	const projectID = "zombie-recovery-test"

	ln, zombieCleanup := setupZombie(t, tmpHome, projectID)
	defer zombieCleanup()
	// Keep zombie open during the command — waggle must detect and recover
	_ = ln

	cmd := exec.Command(tmpBin, "sessions")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "WAGGLE_PROJECT_ID="+projectID)

	done := make(chan struct {
		out []byte
		err error
	}, 1)
	go func() {
		out, err := cmd.CombinedOutput()
		done <- struct {
			out []byte
			err error
		}{out, err}
	}()

	select {
	case result := <-done:
		// Must exit cleanly (waggle sessions prints JSON on success OR
		// exits non-zero with JSON error — either way must not hang)
		output := string(result.out)

		// Must contain "ok" in JSON output
		if !strings.Contains(output, `"ok"`) {
			t.Errorf("expected JSON with \"ok\", got:\n%s", output)
		}

		// Stderr must contain zombie warning
		// CombinedOutput merges stdout+stderr, so check full output
		if !strings.Contains(output, "unresponsive broker") {
			t.Errorf("expected zombie warning in output, got:\n%s", output)
		}

	case <-time.After(5 * time.Second):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		t.Fatal("waggle sessions did not complete within 5s (zombie hang?)")
	}
}

// TestE2E_ZombieFailFast_NoAutoStart verifies that --no-auto-start fails fast
// when a zombie broker exists — must not hang indefinitely.
func TestE2E_ZombieFailFast_NoAutoStart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e")
	}

	tmpBin := buildBinary(t)

	tmpHome, err := os.MkdirTemp("/tmp", "waggle-zombie-noauto-*")
	if err != nil {
		t.Fatalf("mkdirtemp home: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	const projectID = "zombie-noauto-test"

	ln, zombieCleanup := setupZombie(t, tmpHome, projectID)
	defer zombieCleanup()
	_ = ln

	cmd := exec.Command(tmpBin, "--no-auto-start", "status")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "WAGGLE_PROJECT_ID="+projectID)

	done := make(chan struct {
		out []byte
		err error
	}, 1)
	go func() {
		out, err := cmd.CombinedOutput()
		done <- struct {
			out []byte
			err error
		}{out, err}
	}()

	select {
	case result := <-done:
		output := string(result.out)
		if result.err == nil {
			t.Errorf("expected exit 1 with zombie + --no-auto-start, got exit 0\noutput: %s", result.out)
		}
		if !strings.Contains(output, `"running": false`) {
			t.Errorf("expected broker.running=false in output, got:\n%s", output)
		}
		if !strings.Contains(output, `"ok": false`) {
			t.Errorf("expected ok=false in output, got:\n%s", output)
		}
		if !strings.Contains(output, `"BROKER_UNRESPONSIVE"`) {
			t.Errorf("expected BROKER_UNRESPONSIVE code in output, got:\n%s", output)
		}
		if !strings.Contains(output, `"adapters"`) {
			t.Errorf("expected adapters in output, got:\n%s", output)
		}
		if !strings.Contains(output, `"augment":`) {
			t.Errorf("expected augment adapter status in output, got:\n%s", output)
		}
	case <-time.After(10 * time.Second):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		t.Fatal("waggle --no-auto-start status did not complete within 10s (infinite hang?)")
	}
}

// TestE2E_HealthyBrokerUnaffected verifies that a healthy broker is not
// disturbed by the zombie detection path — sessions succeeds quickly.
func TestE2E_HealthyBrokerUnaffected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e")
	}

	tmpBin := buildBinary(t)

	tmpHome, err := os.MkdirTemp("/tmp", "waggle-healthy-*")
	if err != nil {
		t.Fatalf("mkdirtemp home: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	const projectID = "healthy-broker-test"

	// Start a real broker in the background
	startCmd := exec.Command(tmpBin, "start", "--foreground")
	startCmd.Env = append(os.Environ(), "HOME="+tmpHome, "WAGGLE_PROJECT_ID="+projectID)
	if err := startCmd.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	t.Cleanup(func() {
		if startCmd.Process != nil {
			startCmd.Process.Kill()
			startCmd.Wait()
		}
	})

	// Poll until socket appears and is connectable
	socketDir := filepath.Join(tmpHome, ".waggle", "sockets")
	deadline := time.Now().Add(10 * time.Second)
	var socketPath string
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(socketDir)
		if err == nil && len(entries) > 0 {
			sp := filepath.Join(socketDir, entries[0].Name(), "broker.sock")
			conn, connErr := net.Dial("unix", sp)
			if connErr == nil {
				conn.Close()
				socketPath = sp
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if socketPath == "" {
		t.Fatalf("broker did not become ready within 10s")
	}

	// Run waggle sessions — should succeed quickly
	start := time.Now()
	sessCmd := exec.Command(tmpBin, "sessions")
	sessCmd.Env = append(os.Environ(), "HOME="+tmpHome, "WAGGLE_PROJECT_ID="+projectID)

	out, err := sessCmd.Output()
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("waggle sessions failed on healthy broker: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), `"ok"`) {
		t.Errorf("expected JSON with \"ok\", got:\n%s", out)
	}
	if elapsed > 2*time.Second {
		t.Errorf("waggle sessions took %v on healthy broker (must be < 2s)", elapsed)
	}
}

// TestE2E_HelpFromNonGitDir verifies that every subcommand's --help exits 0
// and prints "Usage:" when run from a non-git directory with no broker.
func TestE2E_HelpFromNonGitDir(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e")
	}

	tmpBin := buildBinary(t)

	nonGitDir, err := os.MkdirTemp("/tmp", "waggle-nongit-*")
	if err != nil {
		t.Fatalf("mkdirtemp nongit: %v", err)
	}
	defer os.RemoveAll(nonGitDir)

	fakeHome, err := os.MkdirTemp("/tmp", "waggle-fakehome-*")
	if err != nil {
		t.Fatalf("mkdirtemp fakehome: %v", err)
	}
	defer os.RemoveAll(fakeHome)

	subcommands := [][]string{
		{"listen", "--help"},
		{"sessions", "--help"},
		{"status", "--help"},
		{"task", "create", "--help"},
		{"send", "--help"},
		{"stop", "--help"},
		{"start", "--help"},
		{"task", "--help"},
		{"lock", "--help"},
		{"events", "--help"},
		{"install", "--help"},
		{"listen", "-h"},
	}

	for _, args := range subcommands {
		args := args // capture
		name := strings.Join(args, "_")
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(tmpBin, args...)
			cmd.Dir = nonGitDir
			cmd.Env = append(os.Environ(), "HOME="+fakeHome)

			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("exit error: %v\noutput: %s", err, out)
			}
			if !strings.Contains(string(out), "Usage:") {
				t.Errorf("expected 'Usage:' in output, got:\n%s", out)
			}
		})
	}
}
