# Task 2 Brief: Protocol Types — Wire Format Definition

**Branch:** `feat/001-004-foundation` (same branch as Task 1, commit after)
**Goal:** Define the request/response types for client-broker communication. These are the public wire contract.

**Files to create:**
- `internal/protocol/codes.go`
- `internal/protocol/message.go`
- `internal/protocol/message_test.go`

**Dependencies:** None (no imports from other internal packages).

---

## What to build

**`codes.go`** — Two groups of string constants:

Command constants (the `cmd` field values):
```
connect, disconnect, publish, subscribe,
task.create, task.list, task.claim, task.complete, task.fail,
task.heartbeat, task.cancel, task.get, task.update,
lock, unlock, locks, status, stop
```

Error code constants (the `code` field values):
```
BROKER_NOT_RUNNING, ALREADY_CONNECTED, NOT_CONNECTED,
RESOURCE_LOCKED, TASK_NOT_FOUND, INVALID_TOKEN,
NO_ELIGIBLE_TASK, INVALID_REQUEST, DUPLICATE_IDEMPOTENCY_KEY,
INTERNAL_ERROR
```

**`message.go`** — Three types + two constructors:

`Request` struct (client → broker):
- Cmd, Name, Topic, Message string fields
- Payload `json.RawMessage`
- Type, Tags, DependsOn, Priority, Lease, MaxRetries, IdempotencyKey
- Resource, TaskID, ClaimToken, Result, Reason
- Last, State, Owner
- All fields `omitempty` except Cmd

`Response` struct (broker → client):
- OK bool, Data `json.RawMessage`, Error string, Code string
- All fields except OK are `omitempty`

`Event` struct (broker → client, streamed):
- Topic, Event string, Data `json.RawMessage`, TS string

`OKResponse(data json.RawMessage) Response`
`ErrResponse(code, message string) Response`

## Invariants

| ID | Invariant | How to verify |
|----|-----------|---------------|
| P1 | Request round-trips through JSON without data loss | Test: marshal → unmarshal → compare |
| P2 | OKResponse always has OK=true | Test: check field |
| P3 | ErrResponse always has OK=false and non-empty Code | Test: check fields |
| P4 | All command constants are unique | Test: collect into map, check for dupes |
| P5 | All error code constants are unique | Test: collect into map, check for dupes |
| P6 | No imports outside stdlib | `encoding/json` only |

## Tests

```
TestRequest_RoundTrip          — marshal/unmarshal preserves all fields
TestRequest_OmitsEmptyFields   — empty fields not present in JSON output
TestResponse_OK                — OKResponse sets OK=true
TestResponse_Error             — ErrResponse sets OK=false with code
TestCommandConstants_Unique    — no duplicate command strings
TestErrorCodes_Unique          — no duplicate error code strings
```

## Acceptance criteria

- [ ] All 6 tests pass: `go test ./internal/protocol/ -v -count=1`
- [ ] `go vet ./internal/protocol/` — zero warnings
- [ ] Only imports `encoding/json` and `testing`
- [ ] Commit: `feat(protocol): wire format types and constants`
