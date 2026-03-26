# Task 9a: Session — Per-Connection State and Read Loop

**Branch:** `feat/9-broker-core` (9a, 9b, 9c share one branch, committed separately)
**File:** `internal/broker/session.go`
**Depends on:** Tasks 2 (protocol), 3 (events), 4 (locks), 5-7 (tasks), 8 (client)

## What to build

A Session wraps one client connection. It reads NDJSON requests, dispatches to the router (Task 9b), and writes responses.

**Key behavior: clean vs unclean disconnect.**

```go
type Session struct {
    name            string
    conn            net.Conn
    broker          *Broker
    encoder         *json.Encoder  // writes JSON + newline to conn
    scanner         *bufio.Scanner // reads NDJSON lines from conn
    cleanDisconnect bool
    claimedTasks    []int64 // track task IDs claimed during this session
}
```

**readLoop():**
```
1. scanner.Scan() in a loop
2. json.Unmarshal line into protocol.Request
3. If unmarshal fails → send ErrResponse(ErrInvalidRequest, "invalid JSON"), continue (don't kill connection)
4. Call route(b, s, req) → get protocol.Response
5. encoder.Encode(response) — writes JSON + newline
6. If req.Cmd == "subscribe" → switch to streaming mode (see below)
7. When scanner stops (EOF or error) → call cleanup()
```

**Streaming mode (for subscribe):**
```
1. After route() processes subscribe, get the channel from hub.Subscribe()
2. Loop: read from channel → encoder.Encode(event)
3. Also keep reading from scanner in a separate goroutine to detect disconnect
4. When connection closes or disconnect received → unsubscribe, break
```

**cleanup():**
```go
func (s *Session) cleanup() {
    s.broker.locks.ReleaseAll(s.name)
    s.broker.events.UnsubscribeAll(s.name)
    if !s.cleanDisconnect {
        s.broker.store.RequeueByWorker(s.name)
        slog.Info("unclean disconnect, requeued tasks", "session", s.name)
    }
    s.broker.removeSession(s.name)
    s.conn.Close()
    slog.Info("session closed", "session", s.name, "clean", s.cleanDisconnect)
}
```

**IMPORTANT:** Set scanner buffer to 1MB to handle large payloads:
```go
s.scanner = bufio.NewScanner(conn)
s.scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
```

## Tests

These will be integration tests in `broker_test.go` (Task 9c), not unit tests for session alone. Session is tested through the broker.

## Acceptance criteria

- [ ] Session struct with all fields
- [ ] readLoop handles valid JSON, invalid JSON (no crash), and EOF
- [ ] cleanup distinguishes clean vs unclean disconnect
- [ ] Scanner buffer set to 1MB
- [ ] slog logging on connect, disconnect, errors
- [ ] Commit: `feat(broker): session — per-connection state and read loop`
