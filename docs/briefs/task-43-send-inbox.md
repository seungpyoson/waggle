# Task 43 Brief: Direct Messaging — waggle send + waggle inbox

**Branch:** `feat/43-direct-messaging`
**Goal:** Two connected sessions can exchange messages through the broker. This is the foundation for all agent coordination.

**Files to modify:**
- `internal/protocol/codes.go` — add command + error constants
- `internal/protocol/message.go` — no struct changes (uses existing Payload, Name, Message fields)
- `internal/broker/router.go` — add send, inbox handlers + route cases
- `internal/broker/broker.go` — open DB in New(), pass to both tasks.Store and messages.Store
- `internal/broker/session.go` — add writeMu for thread-safe push delivery
- `internal/tasks/store.go` — change NewStore to accept `*sql.DB` instead of path

**Files to create:**
- `internal/messages/store.go` — SQLite message store
- `internal/messages/store_test.go` — unit tests
- `cmd/send.go` — CLI command
- `cmd/inbox.go` — CLI command

**Dependencies:** None. Builds on v1 main.

---

## Design Decision: CLI Agent Name

Every CLI command is one-shot (connect → command → disconnect). Messaging commands need to know the agent's name. The pattern:

**Environment variable `WAGGLE_AGENT_NAME`** — set once, used by all messaging commands.

```bash
export WAGGLE_AGENT_NAME=alice
waggle send bob "hello"    # sends as alice
waggle inbox               # checks alice's inbox
```

If `WAGGLE_AGENT_NAME` is not set, fall back to `--name` flag:
```bash
waggle send --name alice bob "hello"
waggle inbox --name alice
```

If neither is set, error: `"agent name required: set WAGGLE_AGENT_NAME or use --name"`.

**All new CLI commands** (`send`, `inbox`, and future `ack`, `presence`) follow this pattern. Read existing `cmd/*.go` files (e.g., `cmd/connect.go`, `cmd/task_claim.go`) for the Cobra command structure.

## Design Decision: Shared Database

The broker opens the `*sql.DB` in `broker.New()` and passes it to both stores:

```go
// In broker.New():
db, err := sql.Open("sqlite", cfg.DBPath)
// ... configure db (MaxOpenConns, WAL, busy_timeout) ...
taskStore, err := tasks.NewStore(db)   // change signature: accept *sql.DB, not path
msgStore, err := messages.NewStore(db) // new store, same DB
```

This requires modifying `tasks.NewStore()` to accept `*sql.DB` instead of a string path. Move the `sql.Open()` + pragma setup to `broker.New()`. The task store's existing tests will need updating to open their own test DB and pass it in.

---

## What to build

### 1. Message Store (`internal/messages/store.go`)

SQLite table for durable message storage:

```sql
CREATE TABLE IF NOT EXISTS messages (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    from_name   TEXT NOT NULL,
    to_name     TEXT NOT NULL,
    body        TEXT NOT NULL,
    state       TEXT DEFAULT 'queued',
    created_at  TEXT NOT NULL,
    pushed_at   TEXT
);
```

```go
type Store struct {
    db *sql.DB
}

func NewStore(db *sql.DB) (*Store, error) {
    // Run CREATE TABLE IF NOT EXISTS
    // Return store
}

type Message struct {
    ID        int64  `json:"id"`
    From      string `json:"from"`
    To        string `json:"to"`
    Body      string `json:"body"`
    State     string `json:"state"`
    CreatedAt string `json:"created_at"`
    PushedAt  string `json:"pushed_at,omitempty"`
}

func (s *Store) Send(from, to, body string) (*Message, error)
// INSERT into messages, state='queued', created_at=now (RFC3339 UTC)
// from and to must be non-empty — validate here, not just in router

func (s *Store) Inbox(name string) ([]*Message, error)
// SELECT * FROM messages WHERE to_name = ? AND state IN ('queued', 'pushed') ORDER BY id ASC
// Returns empty slice (not nil) when no messages

func (s *Store) MarkPushed(id int64) error
// UPDATE messages SET state = 'pushed', pushed_at = now WHERE id = ?
```

### 2. Protocol Constants (`internal/protocol/codes.go`)

Add to command constants:
```go
CmdSend  = "send"
CmdInbox = "inbox"
```

No new error constants needed — existing `ErrInvalidRequest` and `ErrInternalError` cover all cases. (Send to unknown name is NOT an error — message is stored for when they connect.)

### 3. Router Handlers (`internal/broker/router.go`)

**handleSend:**
```go
func handleSend(s *Session, req protocol.Request) protocol.Response {
    if req.Name == "" {
        return protocol.ErrResponse(protocol.ErrInvalidRequest, "recipient name required")
    }
    if req.Message == "" {
        return protocol.ErrResponse(protocol.ErrInvalidRequest, "message required")
    }

    msg, err := s.broker.msgStore.Send(s.name, req.Name, req.Message)
    if err != nil {
        return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
    }

    // Push to recipient if connected
    s.broker.mu.RLock()
    recipient, online := s.broker.sessions[req.Name]
    s.broker.mu.RUnlock()

    if online {
        pushMsg := protocol.Response{
            OK:   true,
            Data: mustJSON(map[string]any{
                "type":    "message",
                "id":      msg.ID,
                "from":    msg.From,
                "body":    msg.Body,
                "sent_at": msg.CreatedAt,
            }),
        }
        recipient.writeMu.Lock()
        recipient.enc.Encode(pushMsg)
        recipient.writeMu.Unlock()
        s.broker.msgStore.MarkPushed(msg.ID)
    }

    return protocol.OKResponse(mustJSON(msg))
}
```

**handleInbox:**
```go
func handleInbox(s *Session, req protocol.Request) protocol.Response {
    messages, err := s.broker.msgStore.Inbox(s.name)
    if err != nil {
        return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
    }
    return protocol.OKResponse(mustJSON(messages))
}
```

**Route cases to add:**
```go
case protocol.CmdSend:
    return handleSend(s, req)
case protocol.CmdInbox:
    return handleInbox(s, req)
```

**`send` and `inbox` require a connected session** — do NOT add them to `noSessionRequired`. The existing session check in `route()` enforces this.

### 4. Push Delivery Thread Safety (`internal/broker/session.go`)

Add write mutex to Session:
```go
type Session struct {
    // ... existing fields ...
    writeMu sync.Mutex // protects enc writes
}
```

**Every** `s.enc.Encode()` call in the codebase must hold `s.writeMu`:
- In `readLoop()` — wrap the existing `s.enc.Encode(resp)` call
- In `handleSend()` — wrap the push delivery (shown above)

Search for all `s.enc.Encode` and `\.enc\.Encode` in the broker package to ensure none are missed.

### 5. CLI Commands

Follow the existing Cobra pattern from `cmd/connect.go` and `cmd/task_claim.go`.

**`cmd/send.go`:**
```go
// waggle send [--name sender] <recipient> <message>
// Connects as WAGGLE_AGENT_NAME (or --name), sends message, disconnects.
// Args: recipient (positional 0), message (positional 1)
// Wire: {"cmd": "send", "name": "<recipient>", "message": "<message>"}
// Note: req.Name = recipient, s.name = sender (from connect handshake)
```

**`cmd/inbox.go`:**
```go
// waggle inbox [--name agent]
// Connects as WAGGLE_AGENT_NAME (or --name), checks inbox, prints messages, disconnects.
// Wire: {"cmd": "inbox"}
// Output: JSON array of messages, or human-readable if --format not specified
```

**Name resolution helper** (shared by send, inbox, and future messaging commands):
```go
func resolveAgentName(cmd *cobra.Command) (string, error) {
    name, _ := cmd.Flags().GetString("name")
    if name == "" {
        name = os.Getenv("WAGGLE_AGENT_NAME")
    }
    if name == "" {
        return "", fmt.Errorf("agent name required: set WAGGLE_AGENT_NAME or use --name")
    }
    return name, nil
}
```

## Invariants (must ALL hold)

| ID | Invariant | How to verify | Test name |
|----|-----------|---------------|-----------|
| M1 | Message persists in SQLite after send | Send, query DB directly, verify row | TestStore_SendAndInbox |
| M2 | Inbox returns only messages for the requesting agent | Send to A and B, A's inbox only shows A's | TestStore_InboxFiltersByRecipient |
| M3 | Messages survive broker restart | Send, restart broker, inbox still returns message | TestBroker_MessagesSurviveRestart |
| M4 | Push delivers to connected recipient immediately | Connect two clients, send, verify recipient gets push | TestBroker_SendPushDelivery |
| M5 | Send to disconnected agent stores message (no error) | Send to offline name, connect later, inbox shows it | TestBroker_SendOfflineDelivery |
| M6 | Send to unknown agent stores message (no error) | Send to name never connected, they connect, inbox has it | TestBroker_SendOfflineDelivery (same test, cover both cases) |
| M7 | Messages ordered by creation time | Send 3 messages, inbox returns in order | TestStore_InboxOrdering |
| M8 | Concurrent sends don't corrupt | 10 goroutines send simultaneously, all accounted for | TestStore_SendConcurrent |
| M9 | Write mutex prevents push/response race | Send while recipient is processing a command | TestBroker_SendPushDelivery (with -race flag) |
| M10 | Empty inbox returns empty list, not error | Connect, check inbox before any sends | TestStore_EmptyInbox |
| M11 | Inbox persists across reconnections | Send, disconnect recipient, reconnect, inbox still has message | TestBroker_InboxPersistsAcrossReconnect |
| M12 | Send requires connected session | Send without connect → NOT_CONNECTED | TestBroker_SendRequiresSession |

## Tests (TDD — write these first, run them, see them FAIL, then implement)

### Unit tests (`internal/messages/store_test.go`)

```
TestStore_SendAndInbox              — send message, inbox returns it
TestStore_InboxFiltersByRecipient   — send to A and B, A only sees A's
TestStore_InboxOrdering             — 3 messages arrive in order
TestStore_MarkPushed                — state changes from queued to pushed
TestStore_SendConcurrent            — 10 goroutines, all messages stored
TestStore_EmptyInbox                — inbox with no messages returns empty slice (not nil, not error)
TestStore_SendValidation            — empty from/to/body returns error
```

Each test creates its own `*sql.DB` via `sql.Open("sqlite", ":memory:")` with the same pragma setup as production.

### Integration tests (add to `internal/broker/broker_test.go`)

```
TestBroker_SendMessage              — client1 sends to client2, client2 inbox returns it
TestBroker_SendPushDelivery         — client2 connected, receives push immediately on send
TestBroker_SendOfflineDelivery      — send to offline name, name connects later, inbox has message
TestBroker_SendRequiresName         — send with empty name returns INVALID_REQUEST
TestBroker_SendRequiresMessage      — send with empty message returns INVALID_REQUEST
TestBroker_SendRequiresSession      — send without connect returns NOT_CONNECTED
TestBroker_MessagesSurviveRestart   — send message, stop broker, start new broker (same DB), inbox returns message
TestBroker_InboxPersistsAcrossReconnect — send, recipient disconnects, reconnects, inbox still has message
TestBroker_MultipleSendersOrdering  — A sends to C, B sends to C, C's inbox has both in order
```

Use existing `startTestBroker` / `connectClient` helpers from broker_test.go.

### E2E test (add to `e2e_test.go`)

```
TestE2E_DirectMessaging — full flow:
  1. Start broker (build binary, run waggle start)
  2. Connect as alice
  3. Connect as bob
  4. Bob sends to alice: "hello"
  5. Alice checks inbox: message from bob
  6. Alice sends to bob: "got it"
  7. Bob checks inbox: message from alice
  8. Stop broker
```

### Edge case tests the implementer should add if discovered:

The listed tests are the MINIMUM. If you discover edge cases during implementation (e.g., unicode in messages, very long messages, send to self), add tests for them. More tests is always better. But the listed tests MUST all exist and pass.

## Acceptance criteria

- [ ] All unit tests pass: `go test ./internal/messages/ -v -count=1`
- [ ] All integration tests pass: `go test ./internal/broker/ -v -count=1 -timeout=120s`
- [ ] Existing task tests still pass: `go test ./internal/tasks/ -v -count=1` (DB refactor must not break)
- [ ] E2E test passes: `go test -v -run TestE2E_DirectMessaging -count=1`
- [ ] Race detector: `go test ./... -race -count=1 -timeout=120s`
- [ ] `go vet ./...` — zero warnings
- [ ] CLI works: `WAGGLE_AGENT_NAME=bob waggle send alice "hello"` and `WAGGLE_AGENT_NAME=alice waggle inbox`

## POST-TASK: Live smoke test

After merging, before starting any dependent task:
```bash
# Setup
cd $(mktemp -d) && mkdir .git
go install ~/Projects/Claude/waggle
waggle start --foreground &
sleep 1

# Terminal 1: alice sends
WAGGLE_AGENT_NAME=alice waggle send bob "hello from alice"
echo "Send exit code: $?"
# Must print: {"ok": true, "data": {...}}

# Terminal 1: bob checks inbox (no need for separate terminal — one-shot commands)
WAGGLE_AGENT_NAME=bob waggle inbox
# Must show: message from alice: "hello from alice"

# Bob replies
WAGGLE_AGENT_NAME=bob waggle send alice "got it"

# Alice checks inbox
WAGGLE_AGENT_NAME=alice waggle inbox
# Must show: message from bob: "got it"

# Verify v1 regression — tasks still work
waggle connect --name smoke
waggle task create '{"desc":"test"}'
waggle task list
waggle status
waggle stop
```

Every command must return `{"ok": true}`. If any fails, Task 43 is not done.

- [ ] Commit: `feat(messaging): direct send + inbox — sessions can exchange messages`
