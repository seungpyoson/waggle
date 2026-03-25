# Task 48 Brief: Message Delivery — Presence, Ack Lifecycle, Priority

**Branch:** `feat/48-message-delivery`
**Goal:** Make messaging reliable enough for autonomous orchestration. Sender knows messages were received. Orchestrator knows who's online.

**Dependencies:** Task 43 (basic send/inbox) must be merged to main first.

**Files to modify:**
- `internal/messages/store.go` — add ack, state transitions, TTL, priority
- `internal/messages/store_test.go` — new tests
- `internal/protocol/codes.go` — add command constants
- `internal/broker/router.go` — add ack, presence, send --await-ack handlers
- `internal/broker/broker.go` — add presence store, ack notification channels
- `internal/broker/session.go` — update cleanup to set presence offline

**Files to create:**
- `internal/presence/store.go` — in-memory presence tracker
- `internal/presence/store_test.go` — unit tests
- `cmd/ack.go` — CLI command
- `cmd/presence.go` — CLI command

---

## CLI Pattern

Same as Task 43: `WAGGLE_AGENT_NAME` env var or `--name` flag for agent identity. Use the `resolveAgentName()` helper created in Task 43.

```bash
WAGGLE_AGENT_NAME=bob waggle ack 5
waggle presence                        # no name needed — returns all agents
WAGGLE_AGENT_NAME=alice waggle send bob "start" --await-ack --timeout 30
```

---

## What to build

### 1. Presence Tracker (`internal/presence/store.go`)

In-memory (not SQLite — presence is ephemeral by nature). Broker restart resets all to offline. This is correct — presence reflects live connection state.

```go
type State string

const (
    Online  State = "online"
    Busy    State = "busy"
    Idle    State = "idle"
    Offline State = "offline"
)

type Entry struct {
    Name     string `json:"name"`
    State    State  `json:"state"`
    LastSeen string `json:"last_seen"`
}

type Store struct {
    mu      sync.RWMutex
    entries map[string]*Entry
}

func NewStore() *Store

func (s *Store) SetOnline(name string)
// Sets state=online, updates last_seen to now

func (s *Store) SetOffline(name string)
// Sets state=offline, updates last_seen to now

func (s *Store) SetBusy(name string)
// Sets state=busy, updates last_seen to now

func (s *Store) List() []*Entry
// Returns all entries (including offline) sorted by name

func (s *Store) Get(name string) *Entry
// Returns entry for name, or nil if never seen
```

**Integration with broker (modify existing handlers):**
- `handleConnect` → `presence.SetOnline(name)`
- `session.doCleanup()` → `presence.SetOffline(name)`
- `handleTaskClaim` → `presence.SetBusy(name)`
- `handleTaskComplete` → `presence.SetOnline(name)`
- `handleTaskFail` → `presence.SetOnline(name)`

### 2. Message Ack Lifecycle

Extend messages table (schema migration). Use `ALTER TABLE ADD COLUMN` — check column existence first to be idempotent:

```go
// In messages.NewStore(), after CREATE TABLE:
migrations := []string{
    "ALTER TABLE messages ADD COLUMN seen_at TEXT",
    "ALTER TABLE messages ADD COLUMN acked_at TEXT",
    "ALTER TABLE messages ADD COLUMN priority TEXT DEFAULT 'normal'",
    "ALTER TABLE messages ADD COLUMN ttl INTEGER",
}
for _, m := range migrations {
    _, err := db.Exec(m)
    // Ignore "duplicate column" errors — means migration already ran
}
```

State transitions: `queued → pushed → seen → acked` (also `→ expired` via TTL)

New store methods:
```go
func (s *Store) Ack(id int64) error
// UPDATE messages SET state = 'acked', acked_at = now WHERE id = ? AND state IN ('queued', 'pushed', 'seen')
// Returns error if message not found or already acked

func (s *Store) ExpireStale() (int, error)
// UPDATE messages SET state = 'expired' WHERE ttl IS NOT NULL
//   AND state IN ('queued', 'pushed')
//   AND (julianday('now') - julianday(created_at)) * 86400 > ttl
// Returns count of expired messages
```

Modify existing `Send()` to accept optional priority and TTL:
```go
type SendOpts struct {
    Priority string // "critical", "normal", "bulk" — default "normal"
    TTL      int    // seconds, 0 = no expiry
}

func (s *Store) SendWithOpts(from, to, body string, opts SendOpts) (*Message, error)
```

Update `Message` struct to include new fields:
```go
type Message struct {
    // ... existing fields ...
    SeenAt   string `json:"seen_at,omitempty"`
    AckedAt  string `json:"acked_at,omitempty"`
    Priority string `json:"priority"`
    TTL      int    `json:"ttl,omitempty"`
}
```

### 3. --await-ack (blocking send)

Wire protocol: `{"cmd": "send", "name": "deployer", "message": "start", "payload": {"await_ack": true, "timeout": 30}}`

**Implementation in `handleSend`:**
```go
// Parse payload for await_ack options
var opts struct {
    AwaitAck bool `json:"await_ack"`
    Timeout  int  `json:"timeout"` // seconds, default 30
}
if req.Payload != nil {
    json.Unmarshal(req.Payload, &opts)
}

// ... send message as before ...

if opts.AwaitAck {
    timeout := opts.Timeout
    if timeout <= 0 {
        timeout = 30 // default 30s
    }

    ch := make(chan struct{}, 1)
    s.broker.ackMu.Lock()
    s.broker.ackChans[msg.ID] = ch
    s.broker.ackMu.Unlock()

    select {
    case <-ch:
        // Acked — return success
        return protocol.OKResponse(mustJSON(map[string]any{"acked": true, "message_id": msg.ID}))
    case <-time.After(time.Duration(timeout) * time.Second):
        // Timeout — clean up channel, return error
        s.broker.ackMu.Lock()
        delete(s.broker.ackChans, msg.ID)
        s.broker.ackMu.Unlock()
        return protocol.ErrResponse("ACK_TIMEOUT", "ack not received within timeout")
    }
}
```

**Broker struct additions:**
```go
ackChans map[int64]chan struct{}
ackMu    sync.Mutex
```

Initialize in `New()`: `ackChans: make(map[int64]chan struct{})`

### 4. Router additions

Add to protocol constants:
```go
CmdAck      = "ack"
CmdPresence = "presence"
```

```go
case protocol.CmdAck:
    return handleAck(s, req)
case protocol.CmdPresence:
    return handlePresence(s, req)
```

**handleAck:**
```go
func handleAck(s *Session, req protocol.Request) protocol.Response {
    if req.TaskID == "" {
        return protocol.ErrResponse(protocol.ErrInvalidRequest, "message_id required")
    }
    id, err := strconv.ParseInt(req.TaskID, 10, 64)
    // Note: reusing TaskID field for message_id to avoid adding new field to Request.
    // Alternatively, parse from Payload. Pick one approach.
    if err != nil {
        return protocol.ErrResponse(protocol.ErrInvalidRequest, "invalid message_id")
    }

    if err := s.broker.msgStore.Ack(id); err != nil {
        return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
    }

    // Signal await-ack sender if waiting
    s.broker.ackMu.Lock()
    if ch, ok := s.broker.ackChans[id]; ok {
        close(ch)
        delete(s.broker.ackChans, id)
    }
    s.broker.ackMu.Unlock()

    return protocol.OKResponse(nil)
}
```

**handlePresence:**
```go
func handlePresence(s *Session, req protocol.Request) protocol.Response {
    entries := s.broker.presenceStore.List()
    return protocol.OKResponse(mustJSON(entries))
}
```

`presence` does NOT require a connected session — add to `noSessionRequired` map. Anyone should be able to check who's online.

### 5. TTL Expiry

Add periodic check in broker's `Serve()`:
```go
go func() {
    ticker := time.NewTicker(config.Defaults.MessageTTLCheckPeriod) // default 30s
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            count, _ := b.msgStore.ExpireStale()
            if count > 0 {
                log.Printf("expired %d stale messages", count)
            }
        case <-b.stopCh:
            return
        }
    }
}()
```

Add `MessageTTLCheckPeriod` to `config.Defaults` (default: 30s).

## Invariants

| ID | Invariant | How to verify | Test name |
|----|-----------|---------------|-----------|
| D1 | Ack transitions message to acked state | Send, ack, verify state=acked + acked_at set | TestStore_Ack |
| D2 | --await-ack blocks until receiver acks | Send with await, ack from other client, verify unblocks | TestBroker_AwaitAck |
| D3 | --await-ack times out if no ack | Send with await + 1s timeout, don't ack, verify timeout error | TestBroker_AwaitAckTimeout |
| D4 | Presence reflects connection state | Connect → online, disconnect → offline | TestBroker_PresenceConnectDisconnect |
| D5 | Presence shows busy when task claimed | Claim task, check presence, verify busy | TestBroker_PresenceBusyOnClaim |
| D6 | TTL expires old messages | Send with ttl=1, wait 2s, verify expired | TestBroker_MessageTTLExpiry |
| D7 | Priority stored and queryable | Send with priority, inbox shows priority field | TestStore_PriorityStored |
| D8 | Schema migration preserves existing messages | Create DB with Task 43 messages, run migration, verify intact | TestStore_SchemaMigration |
| D9 | Ack nonexistent message returns error | Ack id=99999, verify error response | TestBroker_AckNonexistent |
| D10 | Double ack is idempotent (no error) | Ack same message twice, second returns ok | TestStore_DoubleAck |
| D11 | Presence returns to online after task complete | Claim → busy, complete → online | TestBroker_PresenceOnlineAfterComplete |
| D12 | --await-ack cleans up channel if recipient disconnects | Send with await, disconnect recipient, verify no goroutine leak | TestBroker_AwaitAckRecipientDisconnect |
| D13 | Presence queryable without session | Call presence without connect, verify returns list | TestBroker_PresenceNoSession |

## Tests (TDD — write first, see fail, then implement)

### Unit tests (`internal/presence/store_test.go`)
```
TestPresence_SetOnline              — name appears as online
TestPresence_SetOffline             — name appears as offline
TestPresence_SetBusy                — name appears as busy
TestPresence_List                   — returns all entries sorted by name
TestPresence_Get                    — returns entry for known name, nil for unknown
TestPresence_ConcurrentUpdates      — 10 goroutines, no race
```

### Unit tests (`internal/messages/store_test.go` — additions)
```
TestStore_Ack                       — ack sets state=acked + acked_at timestamp
TestStore_AckNonexistent            — ack unknown id returns error
TestStore_DoubleAck                 — ack same message twice, no error on second
TestStore_ExpireStale               — TTL-expired messages marked expired
TestStore_ExpireStale_NoTTL         — messages without TTL are never expired
TestStore_PriorityStored            — priority persists and returns correctly
TestStore_SchemaMigration           — create v1 table, insert messages, run migration, verify all fields
```

### Integration tests (`internal/broker/broker_test.go` — additions)
```
TestBroker_Ack                      — send, ack, verify state transition
TestBroker_AckNonexistent           — ack id=99999, verify error
TestBroker_AwaitAck                 — sender blocks, receiver acks, sender unblocks within timeout
TestBroker_AwaitAckTimeout          — sender blocks, no ack, timeout error returned
TestBroker_AwaitAckRecipientDisconnect — sender awaits, recipient disconnects, verify no leak (sender times out cleanly)
TestBroker_PresenceConnectDisconnect — connect → online, disconnect → offline
TestBroker_PresenceBusyOnClaim      — claim task → busy, complete → online
TestBroker_PresenceOnlineAfterComplete — complete/fail returns presence to online
TestBroker_PresenceNoSession        — presence command without connect still works
TestBroker_MessageTTLExpiry         — send with TTL=1, trigger expiry, verify expired state
```

### Edge cases the implementer should add if discovered:

The listed tests are the MINIMUM. Add tests for edge cases you discover (e.g., ack by non-recipient, concurrent ack + expiry race, priority ordering in inbox). More tests is always better.

## Acceptance criteria

- [ ] All presence tests pass: `go test ./internal/presence/ -v -count=1`
- [ ] All message tests pass: `go test ./internal/messages/ -v -count=1`
- [ ] All integration tests pass: `go test ./internal/broker/ -v -count=1 -timeout=120s`
- [ ] Task 43 tests still pass (regression): `go test ./... -v -count=1`
- [ ] Race detector: `go test ./... -race -count=1 -timeout=120s`
- [ ] `go vet ./...` — zero warnings
- [ ] CLI: `waggle presence`, `WAGGLE_AGENT_NAME=bob waggle ack 1`, `WAGGLE_AGENT_NAME=alice waggle send bob "start" --await-ack --timeout 5`

## POST-TASK: Live smoke test

```bash
cd $(mktemp -d) && mkdir .git
go install ~/Projects/Claude/waggle
waggle start --foreground &
sleep 1

# Presence check
waggle presence
# Must return empty list or no entries

# Connect alice
WAGGLE_AGENT_NAME=alice waggle send bob "do the thing" --await-ack --timeout 10 &
SEND_PID=$!
# Alice is now blocking, waiting for ack

# Bob checks inbox and acks
WAGGLE_AGENT_NAME=bob waggle inbox
# Must show message from alice
WAGGLE_AGENT_NAME=bob waggle ack 1
# Alice's send must now unblock (check $SEND_PID exited)
wait $SEND_PID
echo "Send exit code: $?"
# Must be 0

# Presence
waggle presence
# Should show alice and bob

# v1 regression
waggle connect --name smoke
waggle task create '{"desc":"test"}'
waggle task list

# Task 43 regression
WAGGLE_AGENT_NAME=charlie waggle send dave "hi"
WAGGLE_AGENT_NAME=dave waggle inbox

waggle stop
```

- [ ] Commit: `feat(messaging): ack lifecycle, presence tracking, message priority`
