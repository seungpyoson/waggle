# Task 4: Lock Manager — Advisory Coordination

**Files:**
- Create: `internal/locks/manager.go`
- Create: `internal/locks/manager_test.go`

In-memory lock table tied to session identity. Acquire, release, release-all-for-session.

- [ ] **Step 1: Write failing tests**

```go
// internal/locks/manager_test.go
package locks

import "testing"

func TestManager_AcquireAndRelease(t *testing.T) {
    m := NewManager()
    err := m.Acquire("file:src/main.go", "worker-1")
    if err != nil {
        t.Fatal(err)
    }

    locks := m.List()
    if len(locks) != 1 {
        t.Fatalf("expected 1 lock, got %d", len(locks))
    }
    if locks[0].Resource != "file:src/main.go" || locks[0].Owner != "worker-1" {
        t.Errorf("unexpected lock: %+v", locks[0])
    }

    m.Release("file:src/main.go", "worker-1")
    if len(m.List()) != 0 {
        t.Error("lock not released")
    }
}

func TestManager_AcquireConflict(t *testing.T) {
    m := NewManager()
    _ = m.Acquire("res", "worker-1")
    err := m.Acquire("res", "worker-2")
    if err == nil {
        t.Fatal("expected conflict error")
    }
}

func TestManager_SameOwnerReacquire(t *testing.T) {
    m := NewManager()
    _ = m.Acquire("res", "worker-1")
    err := m.Acquire("res", "worker-1")
    if err != nil {
        t.Fatalf("same owner reacquire should succeed: %v", err)
    }
}

func TestManager_ReleaseAll(t *testing.T) {
    m := NewManager()
    _ = m.Acquire("res-a", "worker-1")
    _ = m.Acquire("res-b", "worker-1")
    _ = m.Acquire("res-c", "worker-2")

    m.ReleaseAll("worker-1")

    locks := m.List()
    if len(locks) != 1 {
        t.Fatalf("expected 1 lock remaining, got %d", len(locks))
    }
    if locks[0].Owner != "worker-2" {
        t.Errorf("wrong remaining lock owner: %q", locks[0].Owner)
    }
}

func TestManager_ReleaseWrongOwner(t *testing.T) {
    m := NewManager()
    _ = m.Acquire("res", "worker-1")
    m.Release("res", "worker-2")
    if len(m.List()) != 1 {
        t.Error("lock released by wrong owner")
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/locks/ -v`
Expected: FAIL — `NewManager` not defined

- [ ] **Step 3: Implement Manager**

```go
// internal/locks/manager.go
package locks

import (
    "fmt"
    "sync"
    "time"
)

// Lock represents an advisory lock on a resource.
type Lock struct {
    Resource   string `json:"resource"`
    Owner      string `json:"owner"`
    AcquiredAt string `json:"acquired_at"`
}

// Manager is an in-memory advisory lock table. Thread-safe.
type Manager struct {
    mu    sync.RWMutex
    locks map[string]Lock // resource -> Lock
}

func NewManager() *Manager {
    return &Manager{locks: make(map[string]Lock)}
}

// Acquire attempts to lock a resource for an owner. Returns error if held by another.
func (m *Manager) Acquire(resource, owner string) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    if existing, ok := m.locks[resource]; ok {
        if existing.Owner == owner {
            return nil
        }
        return fmt.Errorf("resource %q locked by %q", resource, existing.Owner)
    }

    m.locks[resource] = Lock{
        Resource:   resource,
        Owner:      owner,
        AcquiredAt: time.Now().UTC().Format(time.RFC3339),
    }
    return nil
}

// Release removes a lock if held by the specified owner.
func (m *Manager) Release(resource, owner string) {
    m.mu.Lock()
    defer m.mu.Unlock()

    if existing, ok := m.locks[resource]; ok && existing.Owner == owner {
        delete(m.locks, resource)
    }
}

// ReleaseAll removes all locks held by an owner. Used on session disconnect.
func (m *Manager) ReleaseAll(owner string) {
    m.mu.Lock()
    defer m.mu.Unlock()

    for resource, lock := range m.locks {
        if lock.Owner == owner {
            delete(m.locks, resource)
        }
    }
}

// List returns all active locks.
func (m *Manager) List() []Lock {
    m.mu.RLock()
    defer m.mu.RUnlock()

    result := make([]Lock, 0, len(m.locks))
    for _, lock := range m.locks {
        result = append(result, lock)
    }
    return result
}

// Count returns the number of active locks.
func (m *Manager) Count() int {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return len(m.locks)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/locks/ -v`
Expected: PASS (5/5)

- [ ] **Step 5: Commit**

```bash
git add internal/locks/
python3 ~/.claude/lib/safe_git.py commit -m "feat: lock manager — advisory coordination"
```
