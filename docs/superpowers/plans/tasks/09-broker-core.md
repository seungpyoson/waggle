# Task 9: Broker Core — Socket Listener, Session, Router

**Files:**
- Create: `internal/broker/broker.go`
- Create: `internal/broker/session.go`
- Create: `internal/broker/router.go`
- Create: `internal/broker/broker_test.go`
- Depends on: Tasks 3 (events), 4 (locks), 5-7 (tasks), 8 (client)

The central orchestrator. Accepts connections, dispatches commands to modules, manages sessions.

- [ ] **Step 1: Write failing integration tests**

```go
// internal/broker/broker_test.go
package broker

import (
    "encoding/json"
    "path/filepath"
    "testing"
    "time"

    "github.com/seungpyoson/waggle/internal/client"
    "github.com/seungpyoson/waggle/internal/protocol"
)

func startTestBroker(t *testing.T) (string, func()) {
    t.Helper()
    sockPath := filepath.Join(t.TempDir(), "broker.sock")
    dbPath := filepath.Join(t.TempDir(), "state.db")

    b, err := New(Config{SocketPath: sockPath, DBPath: dbPath})
    if err != nil {
        t.Fatal(err)
    }

    go b.Serve()
    time.Sleep(100 * time.Millisecond)
    return sockPath, func() { b.Shutdown() }
}

func TestBroker_ConnectAndStatus(t *testing.T) {
    sockPath, cleanup := startTestBroker(t)
    defer cleanup()

    c, err := client.Connect(sockPath)
    if err != nil {
        t.Fatal(err)
    }
    defer c.Close()

    resp, _ := c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "test-1"})
    if !resp.OK {
        t.Fatalf("connect failed: %s", resp.Error)
    }

    resp, _ = c.Send(protocol.Request{Cmd: protocol.CmdStatus})
    if !resp.OK {
        t.Fatalf("status failed: %s", resp.Error)
    }
}

func TestBroker_TaskRoundTrip(t *testing.T) {
    sockPath, cleanup := startTestBroker(t)
    defer cleanup()

    c, _ := client.Connect(sockPath)
    defer c.Close()
    c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "worker-1"})

    // Create
    resp, _ := c.Send(protocol.Request{
        Cmd:     protocol.CmdTaskCreate,
        Payload: json.RawMessage(`{"desc":"test task"}`),
        Type:    "test",
    })
    if !resp.OK {
        t.Fatalf("create: %s", resp.Error)
    }

    // Claim
    resp, _ = c.Send(protocol.Request{Cmd: protocol.CmdTaskClaim})
    if !resp.OK {
        t.Fatalf("claim: %s", resp.Error)
    }
    var claimData struct {
        ID         int64  `json:"id"`
        ClaimToken string `json:"claim_token"`
    }
    json.Unmarshal(resp.Data, &claimData)

    // Complete
    resp, _ = c.Send(protocol.Request{
        Cmd:        protocol.CmdTaskComplete,
        TaskID:     claimData.ID,
        ClaimToken: claimData.ClaimToken,
        Result:     `{"done":true}`,
    })
    if !resp.OK {
        t.Fatalf("complete: %s", resp.Error)
    }

    // Verify
    resp, _ = c.Send(protocol.Request{Cmd: protocol.CmdTaskGet, TaskID: claimData.ID})
    var task struct{ State string `json:"state"` }
    json.Unmarshal(resp.Data, &task)
    if task.State != "completed" {
        t.Errorf("state = %q", task.State)
    }
}

func TestBroker_LockRoundTrip(t *testing.T) {
    sockPath, cleanup := startTestBroker(t)
    defer cleanup()

    c, _ := client.Connect(sockPath)
    defer c.Close()
    c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "w-1"})

    resp, _ := c.Send(protocol.Request{Cmd: protocol.CmdLock, Resource: "file:main.go"})
    if !resp.OK {
        t.Fatalf("lock: %s", resp.Error)
    }

    resp, _ = c.Send(protocol.Request{Cmd: protocol.CmdLocks})
    if !resp.OK {
        t.Fatalf("locks: %s", resp.Error)
    }

    resp, _ = c.Send(protocol.Request{Cmd: protocol.CmdUnlock, Resource: "file:main.go"})
    if !resp.OK {
        t.Fatalf("unlock: %s", resp.Error)
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/broker/ -v`
Expected: FAIL — `New` not defined

- [ ] **Step 3: Implement broker.go**

Create `internal/broker/broker.go` with:
- `Config` struct: SocketPath, DBPath, LeaseCheckPeriod, IdleTimeout
- `Broker` struct: events.Hub, tasks.Store, locks.Manager, net.Listener, sessions map, mutex
- `New(Config)` — open Store, create Hub and Manager, clean up stale socket (`CleanupSocket`), listen on socket, set socket permissions to 0700
- `Serve()` — on startup: re-queue all claimed tasks in SQLite (crash recovery per spec), then start accept loop (goroutine per connection) + lease checker goroutine
- `Shutdown()` — close listener, close all sessions, close Store, remove socket file and PID file
- Lease checker: periodic goroutine calling `tasks.RequeueExpired` + `tasks.ResolveDeps`/`FailDependents` for affected tasks, publishes events for state changes

- [ ] **Step 4: Implement session.go**

Create `internal/broker/session.go` with:
- `Session` struct: name, conn, json encoder, bufio scanner, broker reference
- `newSession(conn, broker)` — wraps the connection
- `readLoop()` — reads NDJSON lines, parses as protocol.Request, calls router, writes protocol.Response
- `cleanup()` — on disconnect: release all locks via `manager.ReleaseAll(name)`, re-queue claimed tasks, unsubscribe from all events, remove from broker session map

- [ ] **Step 5: Implement router.go**

Create `internal/broker/router.go` with:
- `route(session, request) protocol.Response` function
- Switch on `req.Cmd`:
  - `connect` → register session name
  - `disconnect` → trigger cleanup
  - `publish` → hub.Publish
  - `subscribe` → hub.Subscribe, switch to streaming mode
  - `task.*` → dispatch to Store methods, auto-publish to `task.events` topic on state changes
  - `lock/unlock/locks` → dispatch to Manager methods
  - `status` → return session count, task stats, lock count, topic count
  - `stop` → trigger broker shutdown

- [ ] **Step 6: Run integration tests**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/broker/ -v`
Expected: PASS (3/3)

- [ ] **Step 7: Commit**

```bash
git add internal/broker/
python3 ~/.claude/lib/safe_git.py commit -m "feat: broker core — socket listener, session management, command routing"
```
