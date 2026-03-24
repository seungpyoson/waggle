package locks

import (
	"fmt"
	"sync"
	"time"
)

type Lock struct {
	Resource   string `json:"resource"`
	Owner      string `json:"owner"`
	AcquiredAt string `json:"acquired_at"`
}

type Manager struct {
	mu    sync.RWMutex
	locks map[string]Lock
}

func NewManager() *Manager {
	return &Manager{locks: make(map[string]Lock)}
}

func (m *Manager) Acquire(resource, owner string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, exists := m.locks[resource]; exists {
		if existing.Owner != owner {
			return fmt.Errorf("resource %q is locked by %q", resource, existing.Owner)
		}
		return nil
	}
	m.locks[resource] = Lock{Resource: resource, Owner: owner, AcquiredAt: time.Now().UTC().Format(time.RFC3339)}
	return nil
}

func (m *Manager) Release(resource, owner string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, exists := m.locks[resource]; exists && existing.Owner == owner {
		delete(m.locks, resource)
	}
}

func (m *Manager) ReleaseAll(owner string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for resource, lock := range m.locks {
		if lock.Owner == owner {
			delete(m.locks, resource)
		}
	}
}

func (m *Manager) List() []Lock {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Lock, 0, len(m.locks))
	for _, lock := range m.locks {
		result = append(result, lock)
	}
	return result
}

func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.locks)
}

