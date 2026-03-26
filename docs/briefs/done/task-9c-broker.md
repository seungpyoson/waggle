# Task 9c: Broker — Socket Listener, Accept Loop, Integration Tests

**Branch:** `feat/9-broker-core` (same branch as 9a, 9b)
**Files:** `internal/broker/broker.go`, `internal/broker/broker_test.go`
**Depends on:** Tasks 9a (session), 9b (router)

## What to build

The broker struct that ties everything together.

```go
type Broker struct {
    config   Config
    hub      *events.Hub
    store    *tasks.Store
    locks    *locks.Manager
    listener net.Listener
    sessions map[string]*Session
    mu       sync.RWMutex
    done     chan struct{} // signals shutdown
}

type Config struct {
    SocketPath       string
    DBPath           string
    LeaseCheckPeriod time.Duration // default 30s
    IdleTimeout      time.Duration // default 30min
}
```

**New(cfg Config):**
1. Open tasks.Store: `tasks.NewStore(cfg.DBPath)`
2. Create events.Hub: `events.NewHub()`
3. Create locks.Manager: `locks.NewManager()`
4. Listen on socket: `net.Listen("unix", cfg.SocketPath)`
5. Return Broker

**Serve():**
1. Set up slog JSON handler: `slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))`
2. Run `store.RequeueAllClaimed()` — crash recovery. Log count.
3. Start lease checker goroutine (periodic, every `LeaseCheckPeriod`):
   ```go
   go func() {
       ticker := time.NewTicker(cfg.LeaseCheckPeriod)
       defer ticker.Stop()
       for {
           select {
           case <-ticker.C:
               requeued, _ := tasks.RequeueExpired(b.store)
               for _, id := range requeued {
                   if t, err := b.store.Get(id); err == nil {
                       publishTaskEvent(b, "task.requeued", t)
                   }
               }
           case <-b.done:
               return
           }
       }
   }()
   ```
4. Accept loop:
   ```go
   for {
       conn, err := b.listener.Accept()
       if err != nil {
           select {
           case <-b.done:
               return nil // shutting down
           default:
               slog.Error("accept failed", "err", err)
               continue
           }
       }
       s := newSession(conn, b)
       go s.readLoop()
   }
   ```

**Shutdown():**
1. Close `done` channel
2. Close listener
3. Close all session connections (iterate sessions, close each conn)
4. Close store
5. Remove socket file: `os.Remove(cfg.SocketPath)`

**Helper methods:**
```go
func (b *Broker) addSession(s *Session) {
    b.mu.Lock()
    b.sessions[s.name] = s
    b.mu.Unlock()
}

func (b *Broker) removeSession(name string) {
    b.mu.Lock()
    delete(b.sessions, name)
    b.mu.Unlock()
}

func (b *Broker) SessionCount() int {
    b.mu.RLock()
    defer b.mu.RUnlock()
    return len(b.sessions)
}
```

## Integration Tests (broker_test.go)

**Test helper:**
```go
func startTestBroker(t *testing.T) (*Broker, string) {
    t.Helper()
    tmpDir := t.TempDir()
    t.Setenv("HOME", tmpDir) // isolate socket paths
    sockPath := filepath.Join(tmpDir, "test.sock")
    dbPath := filepath.Join(tmpDir, "test.db")

    b, err := New(Config{
        SocketPath:       sockPath,
        DBPath:           dbPath,
        LeaseCheckPeriod: 100 * time.Millisecond, // fast for tests
    })
    if err != nil {
        t.Fatal(err)
    }
    go b.Serve()
    t.Cleanup(func() { b.Shutdown() })

    // Wait for broker to be ready
    deadline := time.Now().Add(5 * time.Second)
    for time.Now().Before(deadline) {
        conn, err := net.DialTimeout("unix", sockPath, 100*time.Millisecond)
        if err == nil {
            conn.Close()
            break
        }
        time.Sleep(50 * time.Millisecond)
    }
    return b, sockPath
}

func connectClient(t *testing.T, sockPath, name string) *client.Client {
    t.Helper()
    c, err := client.Connect(sockPath)
    if err != nil {
        t.Fatal(err)
    }
    resp, err := c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: name})
    if err != nil || !resp.OK {
        t.Fatalf("handshake failed: %v %+v", err, resp)
    }
    return c
}
```

**Required tests (ALL must exist and pass):**

```go
func TestBroker_FullRoundTrip_CreateClaimComplete(t *testing.T)
// connect → task create → task claim → verify token → task complete → task get → verify completed

func TestBroker_DisconnectCleansUpLocks(t *testing.T)
// connect → lock resource → disconnect cleanly → connect as new client → verify lock is released

func TestBroker_CleanDisconnect_KeepsClaim(t *testing.T)
// connect → claim task → send disconnect → close → verify task is STILL claimed (not requeued)

func TestBroker_UncleanDisconnect_RequeuesClaim(t *testing.T)
// connect → claim task → close WITHOUT sending disconnect → verify task back to pending

func TestBroker_DisconnectUnsubscribesEvents(t *testing.T)
// connect → subscribe → disconnect → publish to that topic → no panic

func TestBroker_PublishesTaskEventsOnStateTransitions(t *testing.T)
// connect two clients → client1 subscribes to task.events → client2 creates task →
// client1 receives task.created event

func TestBroker_InvalidJSONReturnsError(t *testing.T)
// connect → send raw garbage bytes → verify error response → send valid command → verify it works
// (broker survives bad input)

func TestBroker_LockConflict(t *testing.T)
// client1 locks resource → client2 tries to lock same resource → gets error with client1's name

func TestBroker_Status(t *testing.T)
// connect → create tasks → lock resource → call status → verify counts match
```

## Acceptance criteria

- [ ] All 9 integration tests pass: `go test ./internal/broker/ -v -count=1 -timeout=120s`
- [ ] Race detector: `go test ./internal/broker/ -race -count=1 -timeout=120s`
- [ ] Full regression: `go test ./... -race -count=1 -timeout=120s`
- [ ] `go vet ./...`
- [ ] slog handler configured once in Serve()
- [ ] Commit: `feat(broker): core — socket listener, accept loop, integration tests`

## POST-TASK: Run smoke test

After Task 9 is merged, before starting Task 10:
```bash
cd $(mktemp -d) && mkdir .git
go build -o waggle ~/Projects/Claude/waggle
./waggle start --foreground &
sleep 1
# If this doesn't work, Task 9 is not done.
```
(Full smoke test requires Task 11 CLI, but a manual binary test verifies the broker runs.)
