# Task 48 Brief: Message Delivery — Ack Lifecycle, Presence, Priority, TTL, --await-ack

**Issue:** #48
**Branch:** `feat/48-delivery`
**Goal:** Extend the basic send/inbox from Task 43 with a complete delivery lifecycle: ack tracking, in-memory presence, priority and TTL on messages, and blocking `--await-ack`. All commands over the same NDJSON Unix socket.

**Dependencies:** Task 43 (basic send/inbox) merged to main.

---

## Scope

**In:**
- Schema migration (ALTER TABLE ADD COLUMN with existence checks — no DROP, no data loss)
- `ack` command — mark a message acked; signal any `--await-ack` waiter
- `presence` command — list connected agents (in-memory, ephemeral; reuses `broker.sessions` map)
- Priority field on `send` (string: `critical` | `normal` | `bulk`; stored, not acted on by broker)
- TTL field on `send`; periodic TTL checker goroutine; inbox excludes expired messages
- `send --await-ack [--timeout N]` — blocks sender's readLoop until receiver acks or timeout

**Out:**
- `seen` command (state set implicitly by inbox read — not a separate wire command this task)
- Busy / idle presence substates (online / offline only; future work)
- Priority-based delivery ordering changes (client's responsibility per spec)
- Presence persisted in SQLite (in-memory only — resets on broker restart by design)
- Phase 2 / Phase 3 v2 work (Claude Code hooks, spawn)

---

## Invariants (D1–D15)

| ID  | Invariant                                                                    | How to verify                                                |
|-----|------------------------------------------------------------------------------|--------------------------------------------------------------|
| D1  | Message transitions through full lifecycle: queued → pushed → seen → acked  | Send, push, inbox (→ seen), ack; read state at each step     |
| D2  | `ack` marks message as acked with acked_at timestamp                         | After ack, acked_at is non-null; state = "acked"             |
| D3  | `ack` on non-existent message returns `MESSAGE_NOT_FOUND`                    | Ack unknown ID → check error code                            |
| D4  | `ack` on message not addressed to caller returns `FORBIDDEN`                 | Alice acks message addressed to bob → FORBIDDEN              |
| D5  | `presence` returns all connected agents with state "online"                  | Two clients connect; presence lists both names + state       |
| D6  | Presence updates on connect (added) and disconnect (removed)                 | Connect → name visible; disconnect → name gone from list     |
| D7  | `send --await-ack` blocks until receiver acks, then returns OK               | Send with AwaitAck=true; receiver acks; sender gets OK       |
| D8  | `send --await-ack --timeout N` returns TIMEOUT error after N seconds         | Send with Timeout=1; no ack for 2s; sender gets TIMEOUT      |
| D9  | No goroutine or channel leak on --await-ack timeout                          | Run `-race`; verify ackWaiters map empty after timeout       |
| D10 | Message with TTL expires after duration (state → "expired")                  | Send ttl=1; wait 2s; MarkExpired; state = "expired"          |
| D11 | TTL expiry runs periodically (same goroutine pattern as lease checker)       | Broker starts TTL checker goroutine in Serve()               |
| D12 | Expired messages not returned by inbox (belt-and-suspenders SQL filter)      | Message past TTL absent from inbox even before checker runs  |
| D13 | Priority field stored and returned on messages                               | Send priority="critical"; inbox shows priority="critical"    |
| D14 | Concurrent acks don't corrupt state                                          | 10 goroutines ack same message; exactly one wins             |
| D15 | Schema migration adds columns without dropping table                         | Existing message rows survive NewStore() on upgraded DB      |

---

## Files to Modify

| File                              | Change                                                                              |
|-----------------------------------|-------------------------------------------------------------------------------------|
| `internal/config/config.go`       | Add `TTLCheckPeriod`, `AwaitAckDefaultTimeout`, `DefaultMsgPriority`, `MaxTTL`      |
| `internal/protocol/codes.go`      | Add `CmdAck`, `CmdPresence`; add `ErrMessageNotFound`, `ErrForbidden`, `ErrTimeout` |
| `internal/protocol/message.go`    | Add `MessageID int64`, `MsgPriority string`, `TTL int`, `AwaitAck bool`, `Timeout int` |
| `internal/messages/store.go`      | Run column migrations; extend `Message`; update `Send`; add `Ack`, `MarkExpired`   |
| `internal/broker/broker.go`       | Add `ackWaiters` map + mutex; add `TTLCheckPeriod` to broker.Config; start TTL checker goroutine in `Serve()` |
| `internal/broker/router.go`       | Add `handleAck`, `handlePresence`; update `handleSend`; add route cases; add `publishPresenceEvent` |
| `internal/broker/session.go`      | `doCleanup` publishes `presence.offline` event; `handleConnect` publishes `presence.online` |

## Files to Create

| File                         | Purpose                                                                  |
|------------------------------|--------------------------------------------------------------------------|
| `internal/messages/ttl.go`   | `StartTTLChecker(store, period, stopCh)` — mirrors `tasks.StartLeaseChecker` |
| `cmd/ack.go`                  | `waggle ack <message_id>` CLI command                                    |
| `cmd/presence.go`             | `waggle presence` CLI command                                            |

**No new packages.** Presence is tracked directly via `broker.sessions` — no `internal/presence/` package needed.

---

## CLI Pattern

Same as Task 43: `WAGGLE_AGENT_NAME` env var or `--name` flag for agent identity. Use the `resolveAgentName()` helper created in Task 43.

```bash
WAGGLE_AGENT_NAME=bob waggle ack 5
WAGGLE_AGENT_NAME=alice waggle presence
WAGGLE_AGENT_NAME=alice waggle send bob "start" --await-ack --timeout 30
```

---

## What to Build

### 1. Config Additions (`internal/config/config.go`)

Add these fields to the `Defaults` struct:

```go
TTLCheckPeriod         time.Duration  // default: 30 * time.Second
AwaitAckDefaultTimeout time.Duration  // default: 30 * time.Second
MaxTTL                 int            // default: 86400 (24h in seconds)
DefaultMsgPriority     string         // default: "normal"
```

`ValidateDefaults()` auto-validates duration and int fields as positive. `DefaultMsgPriority` is a string — skipped by the reflection loop (correct).

Also add `TTLCheckPeriod time.Duration` to `broker.Config` alongside `LeaseCheckPeriod`, and apply the same default-then-validate pattern in `broker.New()`.

### 2. Schema Migration (`internal/messages/store.go`)

Add a `migrate(db *sql.DB) error` function called from `NewStore()`. Check each column via `pragma_table_info` before `ALTER TABLE`. **Never ignore errors silently** — only skip the ALTER if the column already exists (D15):

```go
func migrate(db *sql.DB) error {
    type col struct{ name, ddl string }
    columns := []col{
        {"seen_at",  "ALTER TABLE messages ADD COLUMN seen_at  TEXT"},
        {"acked_at", "ALTER TABLE messages ADD COLUMN acked_at TEXT"},
        {"priority", "ALTER TABLE messages ADD COLUMN priority TEXT NOT NULL DEFAULT 'normal'"},
        {"ttl",      "ALTER TABLE messages ADD COLUMN ttl      INTEGER"},
    }
    for _, c := range columns {
        var count int
        if err := db.QueryRow(
            `SELECT COUNT(*) FROM pragma_table_info('messages') WHERE name = ?`, c.name,
        ).Scan(&count); err != nil {
            return fmt.Errorf("checking column %s: %w", c.name, err)
        }
        if count == 0 {
            if _, err := db.Exec(c.ddl); err != nil {
                return fmt.Errorf("adding column %s: %w", c.name, err)
            }
        }
    }
    return nil
}
```

### 3. Message Struct Changes (`internal/messages/store.go`)

```go
type Message struct {
    ID        int64  `json:"id"`
    From      string `json:"from"`
    To        string `json:"to"`
    Body      string `json:"body"`
    Priority  string `json:"priority"`
    State     string `json:"state"`
    CreatedAt string `json:"created_at"`
    PushedAt  string `json:"pushed_at,omitempty"`
    SeenAt    string `json:"seen_at,omitempty"`
    AckedAt   string `json:"acked_at,omitempty"`
    TTL       *int   `json:"ttl,omitempty"`
}
```

### 4. Store Method Changes (`internal/messages/store.go`)

**Updated `Send` signature** (breaking change — update all callers in `router.go`):

```go
func (s *Store) Send(from, to, body, priority string, ttl *int) (*Message, error)
```

- Validate `priority` against `{"critical", "normal", "bulk"}`. Empty → `config.Defaults.DefaultMsgPriority`.
- Validate `ttl`: if non-nil, must be > 0 and ≤ `config.Defaults.MaxTTL`.

**New `Ack` method** — requires ownership check (D3, D4):

```go
func (s *Store) Ack(id int64, caller string) error
// Use a transaction (check-then-update must be atomic — D14):
// 1. SELECT id, to_name FROM messages WHERE id = ?  → MESSAGE_NOT_FOUND if absent
// 2. Check to_name == caller                        → FORBIDDEN if mismatch
// 3. UPDATE ... SET state='acked', acked_at=now WHERE id=? AND state NOT IN ('acked','expired')
//    0 rows affected = already terminal state → treat as success (idempotent)
```

Return sentinel errors from the store so the router can map to wire codes:
```go
var ErrMessageNotFound = errors.New("message not found")
var ErrNotRecipient    = errors.New("not recipient")
```

**New `MarkExpired` method** (D10, D11):

```go
func (s *Store) MarkExpired() (int, error)
// UPDATE messages SET state='expired'
// WHERE state NOT IN ('acked','expired')
//   AND ttl IS NOT NULL
//   AND CAST(strftime('%s','now') AS INTEGER) >= CAST(strftime('%s', created_at) AS INTEGER) + ttl
// Returns count of updated rows
```

**Updated `Inbox` method** (D1, D12):

- Extend state filter to include `seen`: `state IN ('queued', 'pushed', 'seen')`
- Add inline TTL filter (belt-and-suspenders — excludes expired even before checker runs):
  ```sql
  AND (ttl IS NULL OR CAST(strftime('%s','now') AS INTEGER) < CAST(strftime('%s', created_at) AS INTEGER) + ttl)
  ```
- After the SELECT, mark returned `pushed` messages as `seen` (UPDATE state='seen', seen_at=now per message). This drives D1: lifecycle progresses on inbox read.
- Scan all new columns (`seen_at`, `acked_at`, `priority`, `ttl`).

### 5. Wire Protocol Additions

**New command constants (`internal/protocol/codes.go`):**

```go
CmdAck      = "ack"
CmdPresence = "presence"

ErrMessageNotFound = "MESSAGE_NOT_FOUND"
ErrForbidden       = "FORBIDDEN"
ErrTimeout         = "TIMEOUT"
```

**New Request fields (`internal/protocol/message.go`):**

```go
MessageID   int64  `json:"message_id,omitempty"`   // ack command
MsgPriority string `json:"msg_priority,omitempty"` // send — string enum, avoids collision with Priority int (task priority)
TTL         int    `json:"ttl,omitempty"`           // send — seconds; 0 = no expiry
AwaitAck    bool   `json:"await_ack,omitempty"`     // send
Timeout     int    `json:"timeout,omitempty"`       // send --await-ack; seconds
```

**Note on `MsgPriority` vs `Priority`:** The existing `Priority int` field encodes task priority (0–100). Message priority is a string enum. A separate JSON key (`msg_priority`) prevents unmarshal ambiguity.

**Wire format examples:**

```json
{"cmd": "ack", "message_id": 5}
{"cmd": "send", "name": "deployer", "message": "start step 2", "msg_priority": "critical", "ttl": 300, "await_ack": true, "timeout": 30}
{"cmd": "presence"}
```

### 6. --await-ack (blocking send)

**Broker struct additions (`internal/broker/broker.go`):**

```go
type Broker struct {
    // ... existing fields ...
    ackWaiters   map[int64]chan struct{}
    ackWaitersMu sync.Mutex
}
```

Initialize in `New()`: `ackWaiters: make(map[int64]chan struct{})`.

Add `TTLCheckPeriod time.Duration` to `broker.Config` and include it in the `durField` loop in `New()`.

In `Serve()`, start TTL checker right after the lease checker goroutine:

```go
b.wg.Add(1)
go func() {
    defer b.wg.Done()
    messages.StartTTLChecker(b.msgStore, b.config.TTLCheckPeriod, b.stopCh)
}()
```

**`handleSend` additions (after persisting + pushing message):**

```go
if req.AwaitAck {
    timeout := time.Duration(req.Timeout) * time.Second
    if timeout <= 0 {
        timeout = config.Defaults.AwaitAckDefaultTimeout
    }

    ch := make(chan struct{}, 1) // buffered: ack can arrive before select
    s.broker.ackWaitersMu.Lock()
    s.broker.ackWaiters[msg.ID] = ch
    s.broker.ackWaitersMu.Unlock()

    select {
    case <-ch:
        return protocol.OKResponse(mustMarshal(msg))
    case <-time.After(timeout):
        s.broker.ackWaitersMu.Lock()
        delete(s.broker.ackWaiters, msg.ID) // D9: no leak
        s.broker.ackWaitersMu.Unlock()
        return protocol.ErrResponse(protocol.ErrTimeout, "await-ack timed out")
    case <-s.broker.stopCh:
        s.broker.ackWaitersMu.Lock()
        delete(s.broker.ackWaiters, msg.ID)
        s.broker.ackWaitersMu.Unlock()
        return protocol.ErrResponse(protocol.ErrInternalError, "broker shutting down")
    }
}
```

Parse `req.MsgPriority` (default `config.Defaults.DefaultMsgPriority`) and derive `ttl *int` from `req.TTL` (nil when 0) before calling `msgStore.Send`.

### 7. TTL Checker (`internal/messages/ttl.go`)

Mirror `tasks.StartLeaseChecker` exactly — same goroutine pattern (D11):

```go
package messages

import ("log"; "time")

func StartTTLChecker(store *Store, period time.Duration, stopCh <-chan struct{}) {
    ticker := time.NewTicker(period)
    defer ticker.Stop()
    for {
        select {
        case <-stopCh:
            return
        case <-ticker.C:
            if count, err := store.MarkExpired(); err != nil {
                log.Printf("ttl checker: %v", err)
            } else if count > 0 {
                log.Printf("ttl checker: expired %d messages", count)
            }
        }
    }
}
```

### 8. Router Handler Changes (`internal/broker/router.go`)

**Route cases to add:**

```go
case protocol.CmdAck:
    return handleAck(s, req)
case protocol.CmdPresence:
    return handlePresence(s)
```

Both `ack` and `presence` require a connected session — do NOT add to `noSessionRequired`.

**`handleAck`:**

```go
func handleAck(s *Session, req protocol.Request) protocol.Response {
    if req.MessageID == 0 {
        return protocol.ErrResponse(protocol.ErrInvalidRequest, "message_id required")
    }
    if err := s.broker.msgStore.Ack(req.MessageID, s.name); err != nil {
        if errors.Is(err, messages.ErrMessageNotFound) {
            return protocol.ErrResponse(protocol.ErrMessageNotFound, err.Error())
        }
        if errors.Is(err, messages.ErrNotRecipient) {
            return protocol.ErrResponse(protocol.ErrForbidden, err.Error())
        }
        return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
    }
    // Signal --await-ack sender if blocked
    s.broker.ackWaitersMu.Lock()
    if ch, ok := s.broker.ackWaiters[req.MessageID]; ok {
        delete(s.broker.ackWaiters, req.MessageID)
        s.broker.ackWaitersMu.Unlock()
        ch <- struct{}{} // buffered ch=1, never blocks
    } else {
        s.broker.ackWaitersMu.Unlock()
    }
    return protocol.OKResponse(nil)
}
```

**`handlePresence`** — reads directly from `broker.sessions` (D5, D6):

```go
func handlePresence(s *Session) protocol.Response {
    s.broker.mu.RLock()
    agents := make([]map[string]string, 0, len(s.broker.sessions))
    for name := range s.broker.sessions {
        agents = append(agents, map[string]string{"name": name, "state": "online"})
    }
    s.broker.mu.RUnlock()
    sort.Slice(agents, func(i, j int) bool { return agents[i]["name"] < agents[j]["name"] })
    return protocol.OKResponse(mustMarshal(agents))
}
```

### 9. Presence Events (`internal/broker/router.go` and `session.go`)

Add helper alongside `publishTaskEvent` in `router.go`:

```go
func publishPresenceEvent(b *Broker, event, name string) {
    evt := protocol.Event{
        Topic: "presence.events",
        Event: event,
        Data:  mustMarshal(map[string]string{"name": name}),
        TS:    time.Now().UTC().Format(time.RFC3339),
    }
    b.hub.Publish("presence.events", mustMarshal(evt))
}
```

In `handleConnect`, after registering session (D6):

```go
publishPresenceEvent(s.broker, "presence.online", s.name)
```

In `session.doCleanup`, after removing from sessions map (D6):

```go
if s.name != "" {
    publishPresenceEvent(s.broker, "presence.offline", s.name)
}
```

### 10. CLI Commands

Follow the existing Cobra pattern from `cmd/send.go` and `cmd/task_claim.go`. Use `resolveAgentName()` from Task 43.

**`cmd/ack.go`:**

```go
// waggle ack <message_id>
// Connects as WAGGLE_AGENT_NAME (or --name), sends ack command, disconnects.
// Args: message_id (positional, integer)
// Wire: {"cmd": "ack", "message_id": <id>}
```

**`cmd/presence.go`:**

```go
// waggle presence
// Connects as WAGGLE_AGENT_NAME (or --name), requests presence list, prints JSON, disconnects.
// Wire: {"cmd": "presence"}
// Output: [{"name": "alice", "state": "online"}, ...]
```

**`waggle send` updates (`cmd/send.go`)** — add flags:

```go
sendCmd.Flags().String("priority", "", "Message priority: critical, normal, bulk")
sendCmd.Flags().Int("ttl", 0, "Message TTL in seconds (0 = no expiry)")
sendCmd.Flags().Bool("await-ack", false, "Block until receiver acks the message")
sendCmd.Flags().Int("timeout", 0, "Timeout in seconds for --await-ack (default: 30)")
```

Map flags to new Request fields (`MsgPriority`, `TTL`, `AwaitAck`, `Timeout`).

---

## Invariants Cross-Reference

| ID  | Test name |
|-----|-----------|
| D1  | TestBroker_FullAckLifecycle |
| D2  | TestStore_Ack |
| D3  | TestBroker_AckNonexistent |
| D4  | TestBroker_AckForbidden |
| D5  | TestBroker_PresenceShowsConnected |
| D6  | TestBroker_PresenceConnectDisconnect |
| D7  | TestBroker_AwaitAck |
| D8  | TestBroker_AwaitAckTimeout |
| D9  | TestBroker_AwaitAckNoLeak |
| D10 | TestStore_MarkExpired |
| D11 | TestBroker_TTLCheckerRuns |
| D12 | TestStore_InboxExcludesExpired |
| D13 | TestStore_PriorityStored |
| D14 | TestStore_ConcurrentAck |
| D15 | TestStore_SchemaMigration |

---

## Tests (TDD — write first, see fail, then implement)

### Unit tests (`internal/messages/store_test.go` — add to existing file)

```
TestStore_Ack                    — ack sets state=acked + acked_at timestamp
TestStore_AckNonexistent         — ack unknown id returns ErrMessageNotFound
TestStore_AckForbidden           — caller != to_name returns ErrNotRecipient
TestStore_AckIdempotent          — ack already-acked message: success (idempotent)
TestStore_ConcurrentAck          — 10 goroutines ack same message; exactly one wins in DB
TestStore_MarkExpired            — send with ttl=1; sleep 1s; MarkExpired; state=expired
TestStore_MarkExpired_NoTTL      — messages without ttl never marked expired
TestStore_InboxExcludesExpired   — expired message absent from Inbox (SQL filter, no checker)
TestStore_InboxMarksSeen         — inbox call transitions pushed→seen; sets seen_at
TestStore_PriorityStored         — Send with priority=critical; inbox shows priority=critical
TestStore_PriorityDefault        — Send with empty priority; inbox shows priority=normal
TestStore_PriorityInvalid        — Send with priority=unknown returns error
TestStore_TTLValidation          — Send with ttl=0 → nil; ttl>MaxTTL returns error
TestStore_SchemaMigration        — create v1 schema, insert row, run migration, read row; defaults correct
```

### Integration tests (`internal/broker/broker_test.go` — add to existing file)

```
TestBroker_Ack                       — send, ack, verify state transition end-to-end
TestBroker_AckNonexistent            — ack id=99999 → MESSAGE_NOT_FOUND response
TestBroker_AckForbidden              — alice acks bob's message → FORBIDDEN response
TestBroker_FullAckLifecycle          — queued → pushed → seen (inbox) → acked (ack cmd)
TestBroker_AwaitAck                  — sender blocks, receiver acks, sender unblocks ≤ 1s
TestBroker_AwaitAckTimeout           — sender blocks with timeout=1, no ack, TIMEOUT error
TestBroker_AwaitAckNoLeak            — after timeout, broker.ackWaiters is empty
TestBroker_AwaitAckBrokerShutdown    — broker shuts down while awaiting; sender gets error cleanly
TestBroker_PresenceShowsConnected    — two clients connect; presence lists both with state=online
TestBroker_PresenceConnectDisconnect — connect → present; disconnect → absent from presence list
TestBroker_PresenceEvents            — connect fires presence.events: {event: presence.online}
TestBroker_SendWithPriority          — send msg_priority=critical; inbox shows priority=critical
TestBroker_SendWithTTL               — send ttl=1; wait; MarkExpired; inbox empty
TestBroker_TTLCheckerRuns            — broker starts TTL checker; expired msg absent from inbox
```

### E2E test (add to `e2e_test.go`)

```
TestE2E_AckLifecycle — full flow:
  1. Start broker
  2. alice sends to bob with --await-ack (runs in background goroutine)
  3. bob checks inbox (message appears; state = seen)
  4. bob acks the message
  5. alice's send returns OK
  6. presence shows alice and bob
  7. Stop broker
```

---

## Acceptance Criteria

- [ ] All message store tests pass: `go test ./internal/messages/ -v -count=1`
- [ ] All broker integration tests pass: `go test ./internal/broker/ -v -count=1 -timeout=120s`
- [ ] All existing tests still pass (regression): `go test ./... -v -count=1`
- [ ] Race detector: `go test ./... -race -count=1 -timeout=120s`
- [ ] `go vet ./...` — zero warnings
- [ ] E2E: `go test -v -run TestE2E_AckLifecycle -count=1`
- [ ] CLI: `waggle ack <id>`, `waggle presence`, `waggle send --await-ack --timeout 5 bob "start"`

---

## POST-TASK: Live Smoke Test

```bash
# Setup
cd $(mktemp -d) && mkdir .git
go install ~/Projects/Claude/waggle
waggle start --foreground &
sleep 1

# Presence — empty broker
waggle presence
# Must return: []

# Alice sends to bob with await-ack (blocks in background)
WAGGLE_AGENT_NAME=alice waggle send bob "do the thing" --await-ack --timeout 30 &
SEND_PID=$!
sleep 0.5  # give broker time to register waiter

# Presence — alice is connected (her readLoop is blocked on await-ack)
WAGGLE_AGENT_NAME=alice waggle presence
# Must show alice with state=online

# Bob checks inbox — transitions to seen
WAGGLE_AGENT_NAME=bob waggle inbox
# Must show 1 message from alice, state=seen

# Bob acks
WAGGLE_AGENT_NAME=bob waggle ack 1
# Must return: {"ok": true}

# Alice's send should have unblocked
wait $SEND_PID
echo "Send exit code: $?"
# Must be 0

# Priority + TTL round-trip
WAGGLE_AGENT_NAME=charlie waggle send dave "urgent" --priority critical --ttl 60
WAGGLE_AGENT_NAME=dave waggle inbox
# Must show priority=critical, ttl=60

# Ack validation
WAGGLE_AGENT_NAME=alice waggle ack 999
# Must return: {"ok": false, "code": "MESSAGE_NOT_FOUND"}

waggle stop
```

## POST-TASK: Task 43 Regression Smoke

Must run after every change — Task 43 must not regress:

```bash
# Basic send/inbox round-trip
WAGGLE_AGENT_NAME=alice waggle send bob "hello from alice"
echo "Send exit code: $?"     # must be 0
WAGGLE_AGENT_NAME=bob waggle inbox
# Must show message from alice

WAGGLE_AGENT_NAME=bob waggle send alice "got it"
WAGGLE_AGENT_NAME=alice waggle inbox
# Must show message from bob

# v1 task regression
waggle connect --name smoke
waggle task create '{"desc":"test"}'
waggle task list
waggle status
waggle stop
```

Every command must return `{"ok": true}`. If any fails, the task is not complete.

- [ ] Commit: `feat(messaging): ack lifecycle, presence tracking, priority, TTL, --await-ack`
