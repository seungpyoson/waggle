# Task 48 Review Fixes — PR #51 Amendments

**Branch:** `feat/48-delivery` (same PR #51)
**Goal:** Fix code quality issues and weak tests found during tech lead review. Same branch, same PR.

**Authority:** This spec is authoritative. Do NOT modify the brief (`task-48-delivery.md`). Code changes only.

---

## Round 1 (DONE — committed as 8d5a941)

Q1 (mustMarshalSession duplicate), Q2 (deadlock in doCleanup), Q3 (partial — package var), Q4 (Inbox transaction), Q5 (TTL test period), new TestBroker_PresenceEventOnDisconnect. All landed and verified.

---

## Round 2 — 4 remaining issues

### R2-1 — validPriorities must live in config.Defaults [MINOR]

**File:** `internal/messages/store.go` line 19, `internal/config/config.go`

**Problem:** Round 1 moved `validPriorities` from inline to a package-level var in `store.go`. Still not in `config.Defaults`. The project rule is: all configurable values in `config.Defaults` — single source of truth.

**Fix:**
1. Add to `config.Defaults` struct and initialization:
   ```go
   ValidMsgPriorities []string // default: []string{"critical", "normal", "bulk"}
   ```
2. In `store.go`, delete the package-level `validPriorities` var. In `Send()`, build the validation map from config:
   ```go
   validPriorities := make(map[string]bool, len(config.Defaults.ValidMsgPriorities))
   for _, p := range config.Defaults.ValidMsgPriorities {
       validPriorities[p] = true
   }
   ```
3. Update the error message to be dynamic (list valid values from config).

**Test:** Existing `TestStore_PriorityInvalid` covers validation. No new test needed.

---

### R2-2 — TestBroker_FullAckLifecycle doesn't assert state at each transition [MODERATE]

**File:** `internal/broker/broker_test.go`, `TestBroker_FullAckLifecycle`

**Problem:** D1 says "Message transitions through full lifecycle: queued → pushed → seen → acked." The test walks through send → push → inbox → ack → verify empty inbox, but never asserts the actual state value at each step. The states could skip steps (e.g. queued → acked directly) and this test would still pass. Unit tests (`TestStore_Ack`, `TestStore_InboxMarksSeen`) verify states individually, but the integration test — which is supposed to prove the full chain — doesn't.

**Fix:** After each operation, check the message state in the response or via inbox:

1. After send: verify send response contains `"state": "queued"`
2. After bob receives push: verify push data is present (already done)
3. After bob calls inbox: verify the message in inbox response has `"state": "seen"`
4. After bob acks: verify ack succeeds (already done)
5. After final inbox: verify empty (already done)

The key missing assertions are steps 1 and 3. Add:

```go
// After send — verify state is queued
var sendData map[string]interface{}
json.Unmarshal(resp.Data, &sendData)
if sendData["state"] != "queued" {
    t.Errorf("state after send = %q, want queued", sendData["state"])
}

// ... (push + inbox) ...

// After inbox — verify state is seen
var inboxMsgs []map[string]interface{}
json.Unmarshal(resp.Data, &inboxMsgs)
if len(inboxMsgs) != 1 {
    t.Fatalf("inbox len = %d, want 1", len(inboxMsgs))
}
if inboxMsgs[0]["state"] != "seen" {
    t.Errorf("state after inbox = %q, want seen", inboxMsgs[0]["state"])
}
```

**Invariant:** D1 (full lifecycle state transitions)

---

### R2-3 — TestBroker_TTLCheckerRuns doesn't prove the goroutine ran [MODERATE]

**File:** `internal/broker/broker_test.go`, `TestBroker_TTLCheckerRuns`

**Problem:** Round 1 added `startTestBrokerWithTTL` with 500ms period. But the test still verifies via `Inbox`, which has its own SQL filter (`WHERE ... CAST(strftime('%s','now') AS INTEGER) < CAST(strftime('%s', created_at) AS INTEGER) + ttl`) that independently excludes expired messages. The test passes even if the TTL checker goroutine is completely broken — Inbox filters them out in SQL regardless.

**Fix:** After sleeping for TTL + checker period, verify the message state directly — not through Inbox. Send a raw protocol request that reads the message by ID (or add a query to the test that checks state). The simplest approach:

1. After the sleep, bob sends a second `inbox` request (already done — this is fine for the "user sees correct behavior" angle)
2. **Additionally**, send a `send` to the same expired message's recipient and verify the expired message doesn't reappear. OR better:
3. Add a direct DB assertion. Since `startTestBrokerWithTTL` creates the broker, return the broker reference so the test can query `b.msgStore` directly:

```go
func startTestBrokerWithTTL(t *testing.T, ttlCheckPeriod time.Duration) (string, *Broker, func()) {
    // ... same as before but return b ...
}
```

Then in the test:
```go
sockPath, broker, cleanup := startTestBrokerWithTTL(t, 500*time.Millisecond)
defer cleanup()

// ... send with TTL=1, sleep 2s ...

// Verify TTL checker goroutine actually set state to "expired"
msgs, _ := broker.msgStore.Inbox("bob")  // Inbox filters expired — should be empty
// That's the existing check. Now prove the goroutine ran:
var state string
broker.msgStore.DB().QueryRow("SELECT state FROM messages WHERE id = ?", msgID).Scan(&state)
if state != "expired" {
    t.Errorf("state = %q, want expired (TTL checker goroutine should have marked it)", state)
}
```

Note: this requires either exposing `store.db` via a `DB()` method or adding a `GetMessage(id)` method to the store. The simplest is a `DB()` accessor for test use only. Or query through the existing store methods — add `GetByID(id int64) (*Message, error)` which returns the message regardless of state.

**Invariant:** D11 (TTL expiry runs periodically)

---

### R2-4 — TestBroker_AwaitAckNoLeak doesn't assert map empty [MINOR]

**File:** `internal/broker/broker_test.go`, `TestBroker_AwaitAckNoLeak`

**Problem:** Lines 1583-1585 comment: "We can't directly access broker.ackWaiters from here." The test only proves ack-after-timeout doesn't crash. It doesn't assert the waiter map is empty.

**Fix:** Same pattern as R2-3 — return the broker reference from the test helper, then assert directly:

```go
sockPath, broker, cleanup := startTestBrokerWithTTL(t, 30*time.Second) // or a new helper
// ... send with await-ack timeout=1, sleep 2s ...

broker.ackWaitersMu.Lock()
count := len(broker.ackWaiters)
broker.ackWaitersMu.Unlock()
if count != 0 {
    t.Errorf("ackWaiters has %d entries, want 0 (leak)", count)
}
```

Since both R2-3 and R2-4 need broker access from tests, the cleanest approach is to make `startTestBroker` return `*Broker` as well. Update the helper signature once, use it in both tests.

**Invariant:** D9 (no goroutine/channel leak on timeout)

---

## Implementation note: shared test helper change

R2-3 and R2-4 both need the test to access the `*Broker` directly. Refactor `startTestBroker` and `startTestBrokerWithTTL` to return `(string, *Broker, func())` instead of `(string, func())`. Update all existing callers — they can ignore the broker with `_`:

```go
sockPath, _, cleanup := startTestBroker(t)  // existing tests unchanged
sockPath, broker, cleanup := startTestBroker(t)  // tests that need broker access
```

This is a single mechanical change that enables both R2-3 and R2-4 without exposing store internals.

---

## Verification

After all fixes:

```bash
# Full test suite with race detector
go test ./... -race -count=1 -timeout=120s

# go vet
go vet ./...
```

Specifically verify:
- `go test ./internal/broker/ -run TestBroker_FullAckLifecycle -v` — must show state assertions at each step
- `go test ./internal/broker/ -run TestBroker_TTLCheckerRuns -v` — must assert `state = "expired"` from DB
- `go test ./internal/broker/ -run TestBroker_AwaitAckNoLeak -v` — must assert `ackWaiters` count = 0

## Do NOT

- Modify `docs/briefs/task-48-delivery.md` — brief is frozen
- Add new features beyond what's listed here
- Change the wire protocol
- Break existing tests
- Change `startTestBroker` return signature without updating ALL callers
