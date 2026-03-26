# Module Interfaces — Contracts Between Components

These are the exact Go interfaces that the broker (Task 9) calls. Implementers of the store, hub, and manager must satisfy these. Implementers of the broker must call only these methods.

**This file is the source of truth for inter-module communication.** If the plan files disagree with these interfaces, this file wins.

---

## TaskStore (internal/tasks/store.go)

```go
// Task represents a task row from SQLite.
type Task struct {
    ID             int64    `json:"id"`
    IdempotencyKey string   `json:"idempotency_key,omitempty"`
    Type           string   `json:"type,omitempty"`
    Tags           []string `json:"tags,omitempty"`
    Payload        string   `json:"payload"`
    Priority       int      `json:"priority"`
    State          string   `json:"state"`
    Blocked        bool     `json:"blocked"`
    DependsOn      []int64  `json:"depends_on,omitempty"`
    ClaimToken     string   `json:"claim_token,omitempty"`
    ClaimedBy      string   `json:"claimed_by,omitempty"`
    ClaimedAt      string   `json:"claimed_at,omitempty"`
    LeaseExpiresAt string   `json:"lease_expires_at,omitempty"`
    LeaseDuration  int      `json:"lease_duration"`
    MaxRetries     int      `json:"max_retries"`
    RetryCount     int      `json:"retry_count"`
    Result         string   `json:"result,omitempty"`
    FailureReason  string   `json:"failure_reason,omitempty"`
    CreatedAt      string   `json:"created_at"`
    UpdatedAt      string   `json:"updated_at"`
}

// State constants
const (
    StatePending   = "pending"
    StateClaimed   = "claimed"
    StateCompleted = "completed"
    StateFailed    = "failed"
    StateCanceled  = "canceled"
)

type CreateParams struct {
    Payload        string
    Type           string
    Tags           []string
    DependsOn      []int64
    Priority       int
    LeaseDuration  int    // seconds, 0 = use default from config
    MaxRetries     int    // 0 = use default from config
    IdempotencyKey string
}

type ClaimFilter struct {
    Type string
    Tags []string
}

type ListFilter struct {
    State string
    Type  string
    Owner string
}

// Store is the interface the broker calls.
// Implemented by the SQLite-backed store in internal/tasks/store.go.
type Store struct { /* ... */ }

func NewStore(dbPath string) (*Store, error)
func (s *Store) Close() error

// Core CRUD
func (s *Store) Create(p CreateParams) (*Task, error)
func (s *Store) Get(id int64) (*Task, error)
func (s *Store) List(filter ListFilter) ([]Task, error)

// Claim lifecycle
func (s *Store) Claim(worker string, filter ClaimFilter) (*Task, error)
func (s *Store) Complete(id int64, token, result string) error
func (s *Store) Fail(id int64, token, reason string) error
func (s *Store) Cancel(id int64) error
func (s *Store) Heartbeat(id int64, token string) error

// Crash recovery
func (s *Store) RequeueAllClaimed() (int, error)
func (s *Store) RequeueByWorker(worker string) (int, error) // for unclean disconnect

// Dependencies (in deps.go, operates on Store)
func ResolveDeps(s *Store, completedID int64) ([]int64, error)
func FailDependents(s *Store, failedID int64) ([]int64, error)
func ValidateDeps(s *Store, dependsOn []int64, selfID int64) error

// Lease (in lease.go, operates on Store)
func RequeueExpired(s *Store) ([]int64, error)
```

## EventHub (internal/events/hub.go)

```go
type Hub struct { /* ... */ }

func NewHub() *Hub
func (h *Hub) Subscribe(topic, name string) <-chan []byte
func (h *Hub) Unsubscribe(topic, name string)
func (h *Hub) UnsubscribeAll(name string)
func (h *Hub) Publish(topic string, msg []byte)
func (h *Hub) TopicCount() int
func (h *Hub) SubscriberCount() int
```

## LockManager (internal/locks/manager.go)

```go
type Lock struct {
    Resource   string `json:"resource"`
    Owner      string `json:"owner"`
    AcquiredAt string `json:"acquired_at"`
}

type Manager struct { /* ... */ }

func NewManager() *Manager
func (m *Manager) Acquire(resource, owner string) error
func (m *Manager) Release(resource, owner string)
func (m *Manager) ReleaseAll(owner string)
func (m *Manager) List() []Lock
func (m *Manager) Count() int
```

## Client (internal/client/client.go)

```go
type Client struct { /* ... */ }

func Connect(socketPath string) (*Client, error)
func (c *Client) Send(req protocol.Request) (*protocol.Response, error)
func (c *Client) ReadStream() ([]byte, error)
func (c *Client) Close() error
```

## Broker (internal/broker/ — Task 9)

```go
type Config struct {
    SocketPath       string
    DBPath           string
    LeaseCheckPeriod time.Duration
    IdleTimeout      time.Duration
}

type Broker struct {
    config   Config
    hub      *events.Hub
    store    *tasks.Store
    locks    *locks.Manager
    listener net.Listener
    sessions map[string]*Session // name -> session
    mu       sync.RWMutex
}

func New(cfg Config) (*Broker, error)
func (b *Broker) Serve() error    // blocks, runs accept loop
func (b *Broker) Shutdown() error // graceful stop
func (b *Broker) SessionCount() int
```

## Session (internal/broker/session.go — Task 9a)

```go
type Session struct {
    name            string
    conn            net.Conn
    broker          *Broker
    encoder         *json.Encoder
    scanner         *bufio.Scanner
    cleanDisconnect bool   // set to true when disconnect command received
    claimedTasks    []int64 // track task IDs claimed by this session
}

func newSession(conn net.Conn, broker *Broker) *Session
func (s *Session) readLoop()   // reads NDJSON, dispatches to router, writes response
func (s *Session) cleanup()    // called when connection closes (clean or unclean)
func (s *Session) sendEvent(data []byte) error // write to connection for subscribe
```

## Router (internal/broker/router.go — Task 9b)

```go
// route dispatches a request to the appropriate module and returns a response.
// This is a pure function of (broker state + request) → response.
// Side effects: modifies store/hub/locks via broker reference.
func route(b *Broker, s *Session, req protocol.Request) protocol.Response
```

The router switch statement:
```go
switch req.Cmd {
case protocol.CmdConnect:      // set session name, add to broker.sessions
case protocol.CmdDisconnect:   // set s.cleanDisconnect = true, trigger cleanup
case protocol.CmdPublish:      // b.hub.Publish(req.Topic, []byte(req.Message))
case protocol.CmdSubscribe:    // b.hub.Subscribe(req.Topic, s.name), switch to streaming mode
case protocol.CmdTaskCreate:   // b.store.Create(...), then b.hub.Publish("task.events", ...)
case protocol.CmdTaskList:     // b.store.List(...)
case protocol.CmdTaskClaim:    // b.store.Claim(...), track in s.claimedTasks, publish event
case protocol.CmdTaskComplete: // b.store.Complete(...), ResolveDeps, publish event
case protocol.CmdTaskFail:     // b.store.Fail(...), FailDependents, publish event
case protocol.CmdTaskHeartbeat:// b.store.Heartbeat(...)
case protocol.CmdTaskCancel:   // b.store.Cancel(...), FailDependents, publish event
case protocol.CmdTaskGet:      // b.store.Get(...)
case protocol.CmdTaskUpdate:   // future: append progress note
case protocol.CmdLock:         // b.locks.Acquire(req.Resource, s.name)
case protocol.CmdUnlock:       // b.locks.Release(req.Resource, s.name)
case protocol.CmdLocks:        // b.locks.List()
case protocol.CmdStatus:       // return session count, task stats, lock count, topic count
case protocol.CmdStop:         // trigger b.Shutdown()
default:                       // ErrResponse(protocol.ErrInvalidRequest, "unknown command")
}
```

**IMPORTANT:** Every `task.*` command that changes state must publish to `task.events`:
```go
func publishTaskEvent(b *Broker, eventName string, task *tasks.Task) {
    data, _ := json.Marshal(map[string]any{
        "topic": "task.events",
        "event": eventName,
        "id":    task.ID,
        "state": task.State,
        "ts":    time.Now().UTC().Format(time.RFC3339),
    })
    b.hub.Publish("task.events", data)
}
```
