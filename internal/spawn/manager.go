package spawn

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"syscall"
	"time"
)

type Agent struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	PID       int    `json:"pid"`
	Alive     bool   `json:"alive"`
	SpawnedAt string `json:"spawned_at"`
}

type Manager struct {
	mu     sync.RWMutex
	agents map[string]*Agent
}

func NewManager() *Manager {
	return &Manager{agents: make(map[string]*Agent)}
}

func (m *Manager) Add(name, agentType string, pid int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check for duplicate name
	if _, exists := m.agents[name]; exists {
		return fmt.Errorf("agent with name %q already exists", name)
	}

	// Create agent
	agent := &Agent{
		Name:      name,
		Type:      agentType,
		PID:       pid,
		SpawnedAt: time.Now().UTC().Format(time.RFC3339),
	}

	m.agents[name] = agent
	return nil
}

func (m *Manager) Remove(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.agents, name)
}

func (m *Manager) List() []*Agent {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return empty slice (not nil) if no agents
	if len(m.agents) == 0 {
		return []*Agent{}
	}

	// Build slice from map
	agents := make([]*Agent, 0, len(m.agents))
	for _, agent := range m.agents {
		// Create a copy to avoid race conditions
		agentCopy := &Agent{
			Name:      agent.Name,
			Type:      agent.Type,
			PID:       agent.PID,
			SpawnedAt: agent.SpawnedAt,
		}

		// Check if PID is alive
		agentCopy.Alive = isPIDAlive(agent.PID)

		agents = append(agents, agentCopy)
	}

	// Sort by name for deterministic output
	sort.Slice(agents, func(i, j int) bool {
		return agents[i].Name < agents[j].Name
	})

	return agents
}

func (m *Manager) StopAll() error {
	m.mu.Lock()
	pids := make([]int, 0, len(m.agents))
	for _, agent := range m.agents {
		pids = append(pids, agent.PID)
	}
	m.mu.Unlock()

	// Kill all agents
	for _, pid := range pids {
		// Try to find the process
		process, err := os.FindProcess(pid)
		if err != nil {
			// Process doesn't exist, skip
			continue
		}

		// Send SIGTERM
		if err := process.Signal(syscall.SIGTERM); err != nil {
			// Process might already be dead, ignore error
			continue
		}

		// Wait up to 5 seconds for process to exit
		deadline := time.Now().Add(5 * time.Second)
		terminated := false
		for time.Now().Before(deadline) {
			// Try to reap the process
			var ws syscall.WaitStatus
			wpid, err := syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)
			if wpid == pid || err != nil {
				// Process has been reaped or doesn't exist
				terminated = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}

		// If still alive after SIGTERM, send SIGKILL
		if !terminated {
			process.Signal(syscall.SIGKILL)
			// Wait for SIGKILL to take effect and try to reap
			for i := 0; i < 10; i++ {
				var ws syscall.WaitStatus
				wpid, err := syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)
				if wpid == pid || err != nil {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
	}

	// Clear the agents map
	m.mu.Lock()
	m.agents = make(map[string]*Agent)
	m.mu.Unlock()

	return nil
}

// isPIDAlive checks if a PID is still running
func isPIDAlive(pid int) bool {
	// Use syscall.Kill with signal 0 to check if process exists
	// This works correctly on both macOS and Linux
	err := syscall.Kill(pid, syscall.Signal(0))
	return err == nil
}

