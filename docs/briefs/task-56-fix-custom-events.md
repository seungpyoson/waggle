# Task 56 Brief: Fix Custom Event Payload Wrapping

**Branch:** `fix/56-custom-event-wrapper`
**Goal:** Custom event topics deliver full `protocol.Event` payloads to subscribers, matching built-in events.

**Files to modify:**
- `internal/broker/router.go` — `handlePublish` (line 118)

**Files to verify (must NOT change):**
- `internal/events/hub.go` — hub API unchanged
- `internal/protocol/message.go` — Event struct unchanged
- `internal/client/client.go` — ReadStream unchanged

**Dependencies:** None. This is a bug fix on main.

---

## Root Cause

`handlePublish` (router.go:118-124) sends raw bytes to the hub:
```go
s.broker.hub.Publish(req.Topic, []byte(req.Message))
```

Built-in events wrap data in `protocol.Event` before publishing:
```go
// publishTaskEvent (router.go:488-502)
evt := protocol.Event{
    Topic: "task.events",
    Event: event,
    Data:  mustMarshal(task),
    TS:    time.Now().UTC().Format(time.RFC3339),
}
b.hub.Publish("task.events", mustMarshal(evt))
```

Subscribers (`ReadStream` in client.go:82-98) unmarshal everything as `protocol.Event`. Raw bytes produce `{"topic":"","event":"","ts":""}`.

## What to build

Modify `handlePublish` to wrap the user's message in a `protocol.Event` before publishing. Use the same pattern as `publishTaskEvent`.

The event name for user-published events should be `"custom"` (or use `req.Name` if provided, defaulting to `"custom"`). The topic comes from `req.Topic`. The data is `req.Message` as raw JSON.

**Decision:** Use `"custom"` as the event name. Users can filter by topic — the event name is secondary. Keep it simple.

```go
func handlePublish(s *Session, req protocol.Request) protocol.Response {
    if req.Topic == "" {
        return protocol.ErrResponse(protocol.ErrInvalidRequest, "topic required")
    }
    evt := protocol.Event{
        Topic: req.Topic,
        Event: "custom",
        Data:  json.RawMessage(req.Message),
        TS:    time.Now().UTC().Format(time.RFC3339),
    }
    s.broker.hub.Publish(req.Topic, mustMarshal(evt))
    return protocol.OKResponse(nil)
}
```

**Edge case:** If `req.Message` is not valid JSON, `json.RawMessage(req.Message)` will still marshal — but the subscriber will get invalid JSON in the Data field. Validate that `req.Message` is valid JSON before wrapping. If empty, use `null`.

## Invariants

| ID | Invariant | How to verify | Test name |
|----|-----------|---------------|-----------|
| E1 | Custom event subscribers receive non-empty Topic field | Subscribe to custom topic, publish, verify `event.Topic != ""` | TestBroker_CustomEventHasTopic |
| E2 | Custom event subscribers receive non-empty TS field | Same as E1, verify `event.TS != ""` | TestBroker_CustomEventHasTimestamp |
| E3 | Custom event Data contains the published payload | Publish `{"key":"value"}`, verify subscriber gets it in Data | TestBroker_CustomEventHasData |
| E4 | Custom event Event field is "custom" | Subscribe, publish, verify `event.Event == "custom"` | TestBroker_CustomEventName |
| E5 | Built-in task.events still work (regression) | Create task, verify subscriber gets full task.created event | TestBroker_TaskEventsRegression |
| E6 | Built-in presence.events still work (regression) | Connect agent, verify subscriber gets presence.online event | TestBroker_PresenceEventsRegression |
| E7 | Invalid JSON message is rejected | Publish `not json` to topic, verify error response | TestBroker_CustomEventInvalidJSON |
| E8 | Empty message publishes with null data | Publish with empty message, verify subscriber gets event with null data | TestBroker_CustomEventEmptyMessage |

## Tests (TDD — write first, see fail, then implement)

### Integration tests (add to `internal/broker/broker_test.go`)

```
TestBroker_CustomEventHasTopic          — subscribe to "chat.demo", publish, event.Topic == "chat.demo"
TestBroker_CustomEventHasTimestamp      — same subscription, event.TS is non-empty RFC3339
TestBroker_CustomEventHasData           — publish '{"key":"value"}', event.Data contains it
TestBroker_CustomEventName              — event.Event == "custom"
TestBroker_CustomEventEmptyMessage      — publish with empty message, event.Data is null/empty
TestBroker_CustomEventInvalidJSON       — publish "not json", broker returns INVALID_REQUEST error
TestBroker_TaskEventsRegression         — create a task, subscriber gets full task.created event (unchanged)
TestBroker_PresenceEventsRegression     — connect agent, subscriber gets presence.online event (unchanged)
```

**Test pattern:** Use `startTestBroker`, connect two clients. Client 1 subscribes via `protocol.CmdSubscribe`. Client 2 publishes via `protocol.CmdPublish`. Client 1 reads from `ReadStream()` and checks the Event fields.

For regression tests: use existing patterns from `TestBroker_FullRoundTrip_CreateClaimComplete` — create task, verify event on subscriber.

## Implementation Order

```
Phase A: Invariants (TDD)
  1. Add all 8 tests to broker_test.go — ALL must fail (E1-E4 fail because empty, E5-E6 should pass already, E7-E8 depend on validation)
  2. Run tests: go test ./internal/broker/ -v -run "TestBroker_CustomEvent|TestBroker_TaskEventsRegression|TestBroker_PresenceEventsRegression" -count=1
  3. Confirm E1-E4 fail, E5-E6 pass, E7-E8 fail

Phase B: Implementation
  4. Modify handlePublish in router.go (lines 118-124) — wrap in Event struct
  5. Add JSON validation for req.Message
  6. Run custom event tests — E1-E8 must all pass
  7. Run full suite: go test ./... -race -count=1 -timeout=120s
  8. Run: go vet ./...

Phase C: Smoke Test
  9. Live test (see below)
```

## POST-TASK: Live Smoke Test

```bash
cd ~/Projects/Claude/waggle && go build -o waggle .
waggle start --foreground &
sleep 1

# Terminal 1: Subscribe
waggle events subscribe chat.demo &
SUB_PID=$!
sleep 1

# Terminal 2: Publish
waggle events publish chat.demo '{"msg":"hello from smoke test"}'

# Wait for event
sleep 1
kill $SUB_PID

# Verify: subscriber output must show:
# {"topic":"chat.demo","event":"custom","data":{"msg":"hello from smoke test"},"ts":"2026-..."}
# NOT: {"topic":"","event":"","ts":""}

# Regression: task events still work
waggle events subscribe task.events &
SUB_PID=$!
sleep 1
waggle task create '{"desc":"regression"}' --type test
sleep 1
kill $SUB_PID
# Must show full task.created event

waggle stop
```

Every event must have non-empty `topic`, `event`, and `ts` fields. If any are empty, Task 56 is not done.

- [ ] Commit: `fix(events): wrap custom event payloads in protocol.Event`
