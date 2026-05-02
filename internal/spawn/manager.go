package spawn

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
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

func (m *Manager) UpdatePID(name string, pid int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, exists := m.agents[name]
	if !exists {
		return fmt.Errorf("agent %q not found", name)
	}
	agent.PID = pid
	return nil
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
	// Clear agents map while holding lock to prevent races
	m.agents = make(map[string]*Agent)
	m.mu.Unlock()

	for _, pid := range pids {
		if pid <= 0 {
			continue
		}

		process, err := os.FindProcess(pid)
		if err != nil {
			continue
		}

		// Send SIGTERM
		if err := process.Signal(syscall.SIGTERM); err != nil {
			continue // already dead
		}

		// Poll with kill(pid, 0) to check if process exited
		terminated := false
		deadline := time.Now().Add(config.Defaults.SpawnStopTimeout)
		for time.Now().Before(deadline) {
			if !isPIDAlive(pid) {
				terminated = true
				break
			}
			time.Sleep(config.Defaults.SpawnStopPollInterval)
		}

		// Escalate to SIGKILL if still alive
		if !terminated {
			process.Signal(syscall.SIGKILL)
			// Brief wait for SIGKILL to take effect
			for i := 0; i < 10; i++ {
				if !isPIDAlive(pid) {
					break
				}
				time.Sleep(config.Defaults.SpawnKillPollInterval)
			}
		}
	}

	return nil
}

func (m *Manager) ForgetAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agents = make(map[string]*Agent)
}

// isPIDAlive checks if a PID is still running
func isPIDAlive(pid int) bool {
	if pid <= 0 {
		return false // PID unknown or invalid
	}
	// Use syscall.Kill with signal 0 to check if process exists
	// This works correctly on both macOS and Linux
	err := syscall.Kill(pid, syscall.Signal(0))
	return err == nil
}
