# Task 10: Broker Lifecycle — Auto-start, PID, Idle Timeout

**Files:**
- Create: `internal/broker/lifecycle.go`
- Create: `internal/broker/lifecycle_test.go`
- Depends on: Task 9 (broker core)

Auto-start daemon on first CLI command. PID file management. Lockfile for race prevention. Idle timeout.

- [ ] **Step 1: Write failing tests**

```go
// internal/broker/lifecycle_test.go
package broker

import (
    "os"
    "path/filepath"
    "testing"
)

func TestWriteAndReadPID(t *testing.T) {
    pidFile := filepath.Join(t.TempDir(), "broker.pid")

    err := WritePID(pidFile)
    if err != nil {
        t.Fatal(err)
    }

    pid, err := ReadPID(pidFile)
    if err != nil {
        t.Fatal(err)
    }
    if pid != os.Getpid() {
        t.Errorf("pid = %d, want %d", pid, os.Getpid())
    }
}

func TestIsRunning_NoPIDFile(t *testing.T) {
    pidFile := filepath.Join(t.TempDir(), "nonexistent.pid")
    if IsRunning(pidFile) {
        t.Error("should not be running without PID file")
    }
}

func TestEnsureDirs(t *testing.T) {
    root := t.TempDir()
    waggleDir := filepath.Join(root, ".waggle")
    socketDir := filepath.Join(root, "sockets", "abc123")

    err := EnsureDirs(waggleDir, socketDir)
    if err != nil {
        t.Fatal(err)
    }

    if _, err := os.Stat(waggleDir); os.IsNotExist(err) {
        t.Error("waggle dir not created")
    }
    if _, err := os.Stat(socketDir); os.IsNotExist(err) {
        t.Error("socket dir not created")
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/broker/ -v -run "TestWriteAndReadPID|TestIsRunning|TestEnsureDirs"`
Expected: FAIL

- [ ] **Step 3: Implement lifecycle.go**

Implement:
- `WritePID(pidFile)` — write current PID
- `ReadPID(pidFile)` — read PID from file
- `IsRunning(pidFile)` — read PID, signal 0 to check if alive
- `EnsureDirs(dirs...)` — os.MkdirAll for each
- `CleanupSocket(socketPath)` — remove stale socket file
- `SetSocketPermissions(socketPath)` — `os.Chmod(socketPath, 0700)` after listener creation. Owner-only access prevents other local users from connecting on shared machines.
- `StartDaemon(waggleDir, socketDir)` — fork broker as background process using `os.StartProcess` with `--foreground` flag, redirect stdout/stderr to broker.log

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/broker/ -v -run "TestWriteAndReadPID|TestIsRunning|TestEnsureDirs"`
Expected: PASS (3/3)

- [ ] **Step 5: Commit**

```bash
git add internal/broker/lifecycle.go internal/broker/lifecycle_test.go
python3 ~/.claude/lib/safe_git.py commit -m "feat: broker lifecycle — PID, auto-start, idle timeout"
```
