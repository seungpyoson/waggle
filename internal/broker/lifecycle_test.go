package broker

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLifecycle_StartFailsIfPIDIsLive verifies that starting a broker fails
// if a PID file exists and the process is still running.
func TestLifecycle_StartFailsIfPIDIsLive(t *testing.T) {
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "broker.pid")

	// Write current process PID (simulating a running broker)
	if err := WritePID(pidFile); err != nil {
		t.Fatal(err)
	}

	// Verify the PID is considered running
	if !IsRunning(pidFile) {
		t.Fatal("expected IsRunning to return true for current process")
	}

	// EnsureNotRunning should return error if broker is running
	if err := EnsureNotRunning(pidFile); err == nil {
		t.Fatal("expected EnsureNotRunning to fail when PID is live")
	}
}

// TestLifecycle_StartCleansStalePIDAndSocket verifies that starting a broker
// cleans up stale PID files and socket files from crashed processes.
func TestLifecycle_StartCleansStalePIDAndSocket(t *testing.T) {
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "broker.pid")
	sockPath := filepath.Join(tmpDir, "broker.sock")

	// Write a stale PID (process that doesn't exist)
	stalePID := 99999
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", stalePID)), 0600); err != nil {
		t.Fatal(err)
	}

	// Create a stale socket file
	if err := os.WriteFile(sockPath, []byte{}, 0600); err != nil {
		t.Fatal(err)
	}

	// Verify stale PID is not running
	if IsRunning(pidFile) {
		t.Fatal("stale PID should not be running")
	}

	// CleanupStale should remove both files
	if err := CleanupStale(pidFile, sockPath); err != nil {
		t.Fatal(err)
	}

	// Verify files are removed
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("stale PID file should be removed")
	}
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Error("stale socket file should be removed")
	}
}

// TestLifecycle_StopRemovesPIDAndSocket verifies that stopping a broker
// removes the PID file and socket file.
func TestLifecycle_StopRemovesPIDAndSocket(t *testing.T) {
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "broker.pid")
	// Use /tmp for socket to avoid path length issues
	sockPath := fmt.Sprintf("/tmp/waggle-lifecycle-test-%d.sock", time.Now().UnixNano())
	dbPath := filepath.Join(tmpDir, "state.db")
	defer os.Remove(sockPath)

	// Create broker
	b, err := New(Config{SocketPath: sockPath, DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}

	// Write PID file
	if err := WritePID(pidFile); err != nil {
		t.Fatal(err)
	}

	// Start broker in background
	go b.Serve()
	time.Sleep(100 * time.Millisecond)

	// Verify files exist
	if _, err := os.Stat(pidFile); err != nil {
		t.Errorf("PID file should exist: %v", err)
	}
	if _, err := os.Stat(sockPath); err != nil {
		t.Errorf("socket file should exist: %v", err)
	}

	// Shutdown broker
	if err := b.Shutdown(); err != nil {
		t.Fatal(err)
	}

	// Remove PID file (broker.Shutdown already removes socket)
	if err := RemovePID(pidFile); err != nil {
		t.Fatal(err)
	}

	// Verify files are removed
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("PID file should be removed after shutdown")
	}
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Error("socket file should be removed after shutdown")
	}
}

// TestLifecycle_SocketPermissions0700 verifies that socket files are created
// with 0700 permissions (owner-only access).
func TestLifecycle_SocketPermissions0700(t *testing.T) {
	tmpDir := t.TempDir()
	// Use /tmp for socket to avoid path length issues
	sockPath := fmt.Sprintf("/tmp/waggle-lifecycle-perm-test-%d.sock", time.Now().UnixNano())
	dbPath := filepath.Join(tmpDir, "state.db")
	defer os.Remove(sockPath)

	// Create broker (which creates socket with 0700 permissions)
	b, err := New(Config{SocketPath: sockPath, DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Shutdown()

	// Check socket permissions
	info, err := os.Stat(sockPath)
	if err != nil {
		t.Fatal(err)
	}

	perm := info.Mode().Perm()
	if perm != 0700 {
		t.Errorf("socket permissions = %o, want 0700", perm)
	}
}

