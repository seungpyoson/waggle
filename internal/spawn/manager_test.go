package spawn

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestManager_AddAndList — add agent, list returns it with correct fields
func TestManager_AddAndList(t *testing.T) {
	m := NewManager()

	err := m.Add("worker-1", "claude", 12345)
	if err != nil {
		t.Fatalf("Add() error = %v, want nil", err)
	}

	agents := m.List()
	if len(agents) != 1 {
		t.Fatalf("List() len = %d, want 1", len(agents))
	}

	agent := agents[0]
	if agent.Name != "worker-1" {
		t.Errorf("agent.Name = %q, want 'worker-1'", agent.Name)
	}
	if agent.Type != "claude" {
		t.Errorf("agent.Type = %q, want 'claude'", agent.Type)
	}
	if agent.PID != 12345 {
		t.Errorf("agent.PID = %d, want 12345", agent.PID)
	}
	if agent.SpawnedAt == "" {
		t.Error("agent.SpawnedAt should be set")
	}
}

// TestManager_Remove — remove agent, list excludes it
func TestManager_Remove(t *testing.T) {
	m := NewManager()

	m.Add("worker-1", "claude", 12345)
	m.Add("worker-2", "codex", 12346)

	m.Remove("worker-1")

	agents := m.List()
	if len(agents) != 1 {
		t.Fatalf("List() len = %d, want 1 after remove", len(agents))
	}

	if agents[0].Name != "worker-2" {
		t.Errorf("remaining agent.Name = %q, want 'worker-2'", agents[0].Name)
	}
}

// TestManager_AddDuplicate — add same name twice, second returns error
func TestManager_AddDuplicate(t *testing.T) {
	m := NewManager()

	err := m.Add("worker-1", "claude", 12345)
	if err != nil {
		t.Fatalf("first Add() error = %v, want nil", err)
	}

	err = m.Add("worker-1", "codex", 12346)
	if err == nil {
		t.Error("second Add() with duplicate name should return error")
	}
}

// TestManager_AliveCheck — start a real subprocess (sleep 60), add its PID, verify alive=true; kill it, verify alive=false
func TestManager_AliveCheck(t *testing.T) {
	m := NewManager()

	// Start a real subprocess
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})

	pid := cmd.Process.Pid

	err := m.Add("worker-1", "claude", pid)
	if err != nil {
		t.Fatalf("Add() error = %v, want nil", err)
	}

	// Check alive status
	agents := m.List()
	if len(agents) != 1 {
		t.Fatalf("List() len = %d, want 1", len(agents))
	}

	if !agents[0].Alive {
		t.Error("agent.Alive should be true for running process")
	}

	// Kill the process
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()

	// Wait a bit for the process to fully exit
	time.Sleep(100 * time.Millisecond)

	// Check alive status again
	agents = m.List()
	if len(agents) != 1 {
		t.Fatalf("List() len = %d, want 1", len(agents))
	}

	if agents[0].Alive {
		t.Error("agent.Alive should be false for killed process")
	}
}

// TestManager_StopAll — start 2 real subprocesses, add PIDs, StopAll kills them
func TestManager_StopAll(t *testing.T) {
	m := NewManager()

	// Start two real subprocesses
	cmd1 := exec.Command("sleep", "60")
	if err := cmd1.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd1.Process.Kill()
		cmd1.Wait()
	})

	cmd2 := exec.Command("sleep", "60")
	if err := cmd2.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd2.Process.Kill()
		cmd2.Wait()
	})

	m.Add("worker-1", "claude", cmd1.Process.Pid)
	m.Add("worker-2", "codex", cmd2.Process.Pid)

	// StopAll should kill both processes
	err := m.StopAll()
	if err != nil {
		t.Fatalf("StopAll() error = %v, want nil", err)
	}

	// Reap the processes (since they're our children, they become zombies until we Wait)
	cmd1.Wait()
	cmd2.Wait()

	// Verify both processes are dead
	// Try to signal them — should fail
	if err := cmd1.Process.Signal(syscall.Signal(0)); err == nil {
		t.Error("process 1 should be dead after StopAll")
	}
	if err := cmd2.Process.Signal(syscall.Signal(0)); err == nil {
		t.Error("process 2 should be dead after StopAll")
	}
}

// TestManager_StopAllWithDeadPID — start subprocess, kill it manually, StopAll doesn't crash
func TestManager_StopAllWithDeadPID(t *testing.T) {
	m := NewManager()

	// Start a subprocess
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})

	pid := cmd.Process.Pid
	m.Add("worker-1", "claude", pid)

	// Kill the process manually
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()

	// Wait for process to fully exit
	time.Sleep(100 * time.Millisecond)

	// StopAll should not crash when trying to kill already-dead PID
	err := m.StopAll()
	if err != nil {
		t.Fatalf("StopAll() with dead PID error = %v, want nil", err)
	}
}

// TestManager_ListEmpty — empty manager returns empty slice (not nil)
func TestManager_ListEmpty(t *testing.T) {
	m := NewManager()

	agents := m.List()
	if agents == nil {
		t.Error("List() should return empty slice, not nil")
	}
	if len(agents) != 0 {
		t.Errorf("List() len = %d, want 0", len(agents))
	}
}

// TestManager_IsPIDAliveZero — PID 0 is not alive
func TestManager_IsPIDAliveZero(t *testing.T) {
	m := NewManager()
	m.Add("test", "claude", 0)
	agents := m.List()
	if len(agents) != 1 {
		t.Fatalf("List() len = %d, want 1", len(agents))
	}
	if agents[0].Alive {
		t.Error("PID 0 should not be reported as alive")
	}
}

// TestManager_UpdatePID — update PID of existing agent
func TestManager_UpdatePID(t *testing.T) {
	m := NewManager()
	m.Add("worker", "claude", 0)

	err := m.UpdatePID("worker", 12345)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	agents := m.List()
	if len(agents) != 1 {
		t.Fatalf("List() len = %d, want 1", len(agents))
	}
	if agents[0].PID != 12345 {
		t.Errorf("PID = %d, want 12345", agents[0].PID)
	}
}

// TestManager_UpdatePID_NotFound — update PID of nonexistent agent returns error
func TestManager_UpdatePID_NotFound(t *testing.T) {
	m := NewManager()
	err := m.UpdatePID("nonexistent", 12345)
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
}

