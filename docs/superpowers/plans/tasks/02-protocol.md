# Task 2: Protocol Types — Wire Format Definition

**Files:**
- Create: `internal/protocol/message.go`
- Create: `internal/protocol/codes.go`
- Create: `internal/protocol/message_test.go`

Defines the request/response types that both broker and client use. Implementation-agnostic — this is the wire spec in Go types.

- [ ] **Step 1: Write failing tests for request/response serialization**

```go
// internal/protocol/message_test.go
package protocol

import (
    "encoding/json"
    "testing"
)

func TestRequest_RoundTrip(t *testing.T) {
    req := Request{
        Cmd:     CmdTaskCreate,
        Name:    "worker-1",
        Payload: json.RawMessage(`{"desc":"fix bug"}`),
        Type:    "code-edit",
        Tags:    []string{"auth", "urgent"},
    }

    data, err := json.Marshal(req)
    if err != nil {
        t.Fatal(err)
    }

    var got Request
    if err := json.Unmarshal(data, &got); err != nil {
        t.Fatal(err)
    }

    if got.Cmd != CmdTaskCreate {
        t.Errorf("cmd = %q, want %q", got.Cmd, CmdTaskCreate)
    }
    if len(got.Tags) != 2 {
        t.Errorf("tags len = %d, want 2", len(got.Tags))
    }
}

func TestResponse_OK(t *testing.T) {
    r := OKResponse(json.RawMessage(`{"id":1}`))
    if !r.OK {
        t.Error("expected OK=true")
    }
}

func TestResponse_Error(t *testing.T) {
    r := ErrResponse(ErrBrokerNotRunning, "broker not running")
    if r.OK {
        t.Error("expected OK=false")
    }
    if r.Code != ErrBrokerNotRunning {
        t.Errorf("code = %q, want %q", r.Code, ErrBrokerNotRunning)
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/protocol/ -v`
Expected: FAIL — types not defined

- [ ] **Step 3: Implement codes.go**

```go
// internal/protocol/codes.go
package protocol

// Command constants — the canonical names for wire commands.
const (
    CmdConnect    = "connect"
    CmdDisconnect = "disconnect"

    CmdPublish   = "publish"
    CmdSubscribe = "subscribe"

    CmdTaskCreate    = "task.create"
    CmdTaskList      = "task.list"
    CmdTaskClaim     = "task.claim"
    CmdTaskComplete  = "task.complete"
    CmdTaskFail      = "task.fail"
    CmdTaskHeartbeat = "task.heartbeat"
    CmdTaskCancel    = "task.cancel"
    CmdTaskGet       = "task.get"
    CmdTaskUpdate    = "task.update"

    CmdLock   = "lock"
    CmdUnlock = "unlock"
    CmdLocks  = "locks"

    CmdStatus = "status"
    CmdStop   = "stop"
)

// Error codes — stable identifiers for programmatic error handling.
const (
    ErrBrokerNotRunning = "BROKER_NOT_RUNNING"
    ErrAlreadyConnected = "ALREADY_CONNECTED"
    ErrNotConnected     = "NOT_CONNECTED"
    ErrResourceLocked   = "RESOURCE_LOCKED"
    ErrTaskNotFound     = "TASK_NOT_FOUND"
    ErrInvalidToken     = "INVALID_TOKEN"
    ErrNoEligibleTask   = "NO_ELIGIBLE_TASK"
    ErrInvalidRequest   = "INVALID_REQUEST"
    ErrDuplicateKey     = "DUPLICATE_IDEMPOTENCY_KEY"
    ErrInternal         = "INTERNAL_ERROR"
)
```

- [ ] **Step 4: Implement message.go**

```go
// internal/protocol/message.go
package protocol

import "encoding/json"

// Request is the client-to-broker wire format.
type Request struct {
    Cmd            string          `json:"cmd"`
    Name           string          `json:"name,omitempty"`
    Topic          string          `json:"topic,omitempty"`
    Message        string          `json:"message,omitempty"`
    Payload        json.RawMessage `json:"payload,omitempty"`
    Type           string          `json:"type,omitempty"`
    Tags           []string        `json:"tags,omitempty"`
    DependsOn      []int64         `json:"depends_on,omitempty"`
    Priority       *int            `json:"priority,omitempty"`
    Lease          string          `json:"lease,omitempty"`
    MaxRetries     *int            `json:"max_retries,omitempty"`
    IdempotencyKey string          `json:"idempotency_key,omitempty"`
    Resource       string          `json:"resource,omitempty"`
    TaskID         int64           `json:"task_id,omitempty"`
    ClaimToken     string          `json:"claim_token,omitempty"`
    Result         string          `json:"result,omitempty"`
    Reason         string          `json:"reason,omitempty"`
    Last           int             `json:"last,omitempty"`
    State          string          `json:"state,omitempty"`
    Owner          string          `json:"owner,omitempty"`
}

// Response is the broker-to-client wire format.
type Response struct {
    OK    bool            `json:"ok"`
    Data  json.RawMessage `json:"data,omitempty"`
    Error string          `json:"error,omitempty"`
    Code  string          `json:"code,omitempty"`
}

// Event is a streamed broker-to-client message on subscribe connections.
type Event struct {
    Topic string          `json:"topic"`
    Event string          `json:"event"`
    Data  json.RawMessage `json:"data,omitempty"`
    TS    string          `json:"ts"`
}

// OKResponse creates a success response with optional data.
func OKResponse(data json.RawMessage) Response {
    return Response{OK: true, Data: data}
}

// ErrResponse creates an error response with code and message.
func ErrResponse(code, message string) Response {
    return Response{OK: false, Code: code, Error: message}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/protocol/ -v`
Expected: PASS (3/3)

- [ ] **Step 6: Commit**

```bash
git add internal/protocol/
python3 ~/.claude/lib/safe_git.py commit -m "feat: protocol types — wire format definition"
```
