# Waggle Machine Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a machine-local Waggle runtime that owns persistent delivery and notifications, while removing long-lived listener management from Claude Code startup hooks.

**Architecture:** Add one machine-local runtime process inside the existing `waggle` CLI that watches `(project_id, agent_name)` endpoints, keeps persistent push listeners alive, stores local unread metadata, and emits OS notifications. Tool adapters become thin registration/read paths only; the broker remains the source of truth for message delivery and ack semantics.

**Tech Stack:** Go, Cobra CLI, existing Waggle broker/client/config packages, lightweight local JSON/SQLite persistence, shell hooks for thin adapters

---

## File Structure

**Create:**
- `internal/runtime/manager.go` — machine-local runtime orchestration, watch lifecycle, listener supervision
- `internal/runtime/store.go` — local unread/presentation state persistence for watched endpoints
- `internal/runtime/notifier.go` — notifier interface + platform command adapter
- `internal/runtime/types.go` — watch and local delivery record types
- `internal/runtime/manager_test.go` — runtime watch lifecycle and recovery tests
- `internal/runtime/store_test.go` — local state persistence tests
- `internal/runtime/notifier_test.go` — notifier selection/command tests
- `cmd/runtime.go` — `waggle runtime ...` command group
- `cmd/runtime_start.go` — start/status/stop subcommands
- `cmd/runtime_watch.go` — watch/unwatch/list subcommands
- `cmd/runtime_pull.go` — read unread local records for adapters

**Modify:**
- `internal/config/config.go` — add machine-runtime directories/files/defaults
- `cmd/root.go` — register runtime command group without coupling it to broker auto-start
- `cmd/listen.go` — expose reusable listener loop pieces if needed, or narrow command purpose
- `internal/install/claude_code.go` — install new safe Claude hook assets
- `internal/install/claude-code/hook.sh` — remove background listener startup; register watch intent + read local unread state only
- `integrations/claude-code/hook.sh` — same as installed asset
- `cmd/spawn.go` — auto-register watches for spawned agents
- `README.md` — document runtime model and safe automatic delivery setup

**Optional create if needed to keep boundaries clean:**
- `internal/runtime/listener.go` — extracted persistent listener logic shared by runtime and `waggle listen`

---

### Task 1: Define Runtime Types and Local Paths

**Files:**
- Create: `internal/runtime/types.go`
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing config/runtime path tests**

Add tests that assert the config layer can derive runtime-specific paths without inventing a second identity system.

```go
func TestNewPaths_IncludesRuntimePaths(t *testing.T) {
	paths := NewPaths("project-123")

	if paths.RuntimeDir == "" {
		t.Fatal("RuntimeDir should be set")
	}
	if paths.RuntimeState == "" {
		t.Fatal("RuntimeState should be set")
	}
	if paths.RuntimeSocket == "" {
		t.Fatal("RuntimeSocket should be set")
	}
}

func TestResolveProjectID_UnchangedForRuntime(t *testing.T) {
	t.Setenv("WAGGLE_PROJECT_ID", "explicit-id")
	id, err := ResolveProjectID()
	if err != nil {
		t.Fatalf("ResolveProjectID: %v", err)
	}
	if id != "explicit-id" {
		t.Fatalf("id = %q, want explicit-id", id)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config -run 'TestNewPaths_IncludesRuntimePaths|TestResolveProjectID_UnchangedForRuntime' -count=1`

Expected: FAIL because `Paths` does not yet expose runtime-specific fields.

- [ ] **Step 3: Add runtime path fields and defaults**

Extend config with runtime-only derived paths that still key off the same `project_id`.

```go
type Paths struct {
	ProjectID string
	DataDir   string
	DB        string
	PID       string
	Lock      string
	Log       string
	Socket    string

	RuntimeDir   string
	RuntimeState string
	RuntimeSocket string
}
```

Populate them in `NewPaths` using the same `project_id` hash and the same home-rooted base directory.

- [ ] **Step 4: Create shared runtime types**

Add the initial runtime types in `internal/runtime/types.go`.

```go
package runtime

type Watch struct {
	ProjectID string `json:"project_id"`
	AgentName string `json:"agent_name"`
	Source    string `json:"source"`
}

type DeliveryRecord struct {
	ProjectID   string `json:"project_id"`
	AgentName   string `json:"agent_name"`
	MessageID   int64  `json:"message_id"`
	FromName    string `json:"from_name"`
	Body        string `json:"body"`
	SentAt      string `json:"sent_at"`
	ReceivedAt  string `json:"received_at"`
	NotifiedAt  string `json:"notified_at,omitempty"`
	SurfacedAt  string `json:"surfaced_at,omitempty"`
	DismissedAt string `json:"dismissed_at,omitempty"`
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/config ./internal/runtime -count=1`

Expected: PASS for config tests, and `internal/runtime` compiles even if it has no tests yet.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/runtime/types.go
git commit -m "feat: define machine runtime identity and paths"
```

### Task 2: Build Local Runtime Store

**Files:**
- Create: `internal/runtime/store.go`
- Create: `internal/runtime/store_test.go`

- [ ] **Step 1: Write the failing store tests**

Cover watch persistence, unread record persistence, and mark-surfaced behavior.

```go
func TestStore_SaveAndListWatches(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(filepath.Join(dir, "runtime.json"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	err = s.UpsertWatch(Watch{ProjectID: "p1", AgentName: "claude-main", Source: "explicit"})
	if err != nil {
		t.Fatalf("UpsertWatch: %v", err)
	}

	watches, err := s.ListWatches()
	if err != nil {
		t.Fatalf("ListWatches: %v", err)
	}
	if len(watches) != 1 {
		t.Fatalf("len(watches) = %d, want 1", len(watches))
	}
}

func TestStore_AddUnreadAndMarkSurfaced(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(filepath.Join(dir, "runtime.json"))

	rec := DeliveryRecord{
		ProjectID:  "p1",
		AgentName:  "claude-main",
		MessageID:  7,
		FromName:   "orchestrator",
		Body:       "start step 2",
		ReceivedAt: "2026-03-27T00:00:00Z",
	}
	if err := s.AddRecord(rec); err != nil {
		t.Fatalf("AddRecord: %v", err)
	}

	unread, err := s.Unread("p1", "claude-main")
	if err != nil {
		t.Fatalf("Unread: %v", err)
	}
	if len(unread) != 1 {
		t.Fatalf("len(unread) = %d, want 1", len(unread))
	}

	if err := s.MarkSurfaced("p1", "claude-main", 7); err != nil {
		t.Fatalf("MarkSurfaced: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime -run 'TestStore_' -count=1`

Expected: FAIL because the store is not implemented.

- [ ] **Step 3: Implement the minimal runtime store**

Use a single local state file with explicit watch and delivery collections. Keep the persistence implementation behind a store interface so SQLite can replace the backing later without changing runtime logic.

```go
type Store struct {
	path string
	mu   sync.Mutex
}

type persistedState struct {
	Watches []Watch          `json:"watches"`
	Records []DeliveryRecord `json:"records"`
}
```

Implement:

- `NewStore(path string) (*Store, error)`
- `UpsertWatch(w Watch) error`
- `RemoveWatch(projectID, agentName string) error`
- `ListWatches() ([]Watch, error)`
- `AddRecord(rec DeliveryRecord) error`
- `Unread(projectID, agentName string) ([]DeliveryRecord, error)`
- `MarkSurfaced(projectID, agentName string, messageID int64) error`

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/runtime -run 'TestStore_' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runtime/store.go internal/runtime/store_test.go
git commit -m "feat: add machine runtime local state store"
```

### Task 3: Add Notifier and Runtime Manager

**Files:**
- Create: `internal/runtime/notifier.go`
- Create: `internal/runtime/notifier_test.go`
- Create: `internal/runtime/manager.go`
- Create: `internal/runtime/manager_test.go`
- Modify: `cmd/listen.go` or create `internal/runtime/listener.go`

- [ ] **Step 1: Write the failing notifier test**

Write a test that the notifier can be stubbed and called exactly once when a delivery is processed.

```go
type stubNotifier struct {
	calls int
}

func (n *stubNotifier) Notify(title, body string) error {
	n.calls++
	return nil
}

func TestManager_NotifyOnDelivery(t *testing.T) {
	storeDir := t.TempDir()
	store, _ := NewStore(filepath.Join(storeDir, "runtime.json"))
	notifier := &stubNotifier{}

	mgr := NewManager(store, notifier, nil)
	err := mgr.handleDelivery(Watch{ProjectID: "p1", AgentName: "claude-main"}, DeliveryRecord{
		ProjectID: "p1",
		AgentName: "claude-main",
		MessageID: 1,
		FromName:  "planner",
		Body:      "message body",
	})
	if err != nil {
		t.Fatalf("handleDelivery: %v", err)
	}
	if notifier.calls != 1 {
		t.Fatalf("notifier.calls = %d, want 1", notifier.calls)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime -run 'TestManager_NotifyOnDelivery' -count=1`

Expected: FAIL because the manager and notifier abstraction do not exist.

- [ ] **Step 3: Implement notifier abstraction**

Define:

```go
type Notifier interface {
	Notify(title, body string) error
}
```

Add a command-backed implementation that can later switch by OS:

```go
type CommandNotifier struct{}

func (n *CommandNotifier) Notify(title, body string) error {
	cmd := exec.Command("osascript", "-e", fmt.Sprintf(`display notification %q with title %q`, body, title))
	return cmd.Run()
}
```

Keep platform branching isolated to this file. If a platform is unsupported, return a no-op notifier with a clear comment.

- [ ] **Step 4: Implement runtime manager**

The manager should own:

- watch loading from store
- listener goroutines per watch
- delivery handling
- stop lifecycle

Core shape:

```go
type ListenerFactory func(w Watch, handle func(DeliveryRecord) error) (io.Closer, error)

type Manager struct {
	store    *Store
	notifier Notifier
	newWatch ListenerFactory
}
```

`handleDelivery` must:

- de-duplicate by `(project_id, agent_name, message_id)`
- persist the record
- notify once

- [ ] **Step 5: Extract or wrap persistent listener logic**

Do not let the runtime shell out to a fragile CLI loop if a shared internal listener can be reused cleanly. Either:

- extract `waggle listen` transport into `internal/runtime/listener.go`, or
- add a thin internal helper that reuses the existing client read loop safely.

The runtime listener must connect to the same broker using the same `project_id` path logic.

- [ ] **Step 6: Run runtime tests**

Run: `go test ./internal/runtime -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/runtime/notifier.go internal/runtime/notifier_test.go internal/runtime/manager.go internal/runtime/manager_test.go cmd/listen.go internal/runtime/listener.go
git commit -m "feat: add machine runtime manager and notifier"
```

### Task 4: Add Runtime CLI and Watch Commands

**Files:**
- Create: `cmd/runtime.go`
- Create: `cmd/runtime_start.go`
- Create: `cmd/runtime_watch.go`
- Create: `cmd/runtime_pull.go`
- Modify: `cmd/root.go`

- [ ] **Step 1: Write the failing command tests**

At minimum, add focused command tests or CLI smoke tests for:

- `waggle runtime watch --agent claude-main`
- `waggle runtime watches`
- `waggle runtime pull --agent claude-main`

Use command-level tests if the repo already has a command testing pattern; otherwise add a small CLI integration test file.

```go
func TestRuntimeWatchCommand_RequiresAgent(t *testing.T) {
	cmd := newRuntimeWatchCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected missing agent error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd -run 'TestRuntime' -count=1`

Expected: FAIL because the runtime command group does not exist.

- [ ] **Step 3: Implement runtime command group**

Add:

- `waggle runtime start`
- `waggle runtime stop`
- `waggle runtime status`
- `waggle runtime watch --agent <name> [--project-id <id>] [--source explicit]`
- `waggle runtime unwatch --agent <name> [--project-id <id>]`
- `waggle runtime watches`
- `waggle runtime pull --agent <name> [--project-id <id>]`

`runtime watch` should default project resolution to the current repo using existing `config.ResolveProjectID()`.

`runtime pull` should output unread local records as JSON for adapters.

- [ ] **Step 4: Ensure runtime commands do not require broker auto-start**

Keep runtime lifecycle independent from broker startup logic in `cmd/root.go`. The runtime may attach to brokers later, but the CLI commands themselves must still work without forcing broker startup during help or local inspection.

- [ ] **Step 5: Run command tests**

Run: `go test ./cmd -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/runtime.go cmd/runtime_start.go cmd/runtime_watch.go cmd/runtime_pull.go cmd/root.go
git commit -m "feat: add machine runtime CLI"
```

### Task 5: Make Claude Safe and Redirect Automatic Delivery Through Runtime

**Files:**
- Modify: `internal/install/claude-code/hook.sh`
- Modify: `integrations/claude-code/hook.sh`
- Modify: `internal/install/claude_code.go`
- Test: `internal/install/claude_code_test.go`

- [ ] **Step 1: Write the failing Claude hook test**

Add an installer test that verifies the installed hook content no longer backgrounds `waggle listen`.

```go
func TestInstallClaudeCode_HookDoesNotStartBackgroundListener(t *testing.T) {
	tmpHome := t.TempDir()
	if err := installClaudeCode(tmpHome); err != nil {
		t.Fatalf("installClaudeCode: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpHome, ".claude", "hooks", "waggle-connect.sh"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	content := string(data)
	if strings.Contains(content, "waggle listen") {
		t.Fatal("hook should not start waggle listen")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/install -run 'TestInstallClaudeCode_HookDoesNotStartBackgroundListener' -count=1`

Expected: FAIL because the current hook still starts `waggle listen`.

- [ ] **Step 3: Replace hook behavior**

The new hook should do only these actions:

- resolve `project_id`
- optionally auto-register watch intent with `waggle runtime watch --source claude-session-start`
- read unread local records with `waggle runtime pull`
- render any safe summary into Claude startup context

It must not:

- run `waggle listen`
- background a child process
- `disown`
- `pkill` listener processes

The critical deletion is this block:

```bash
LISTEN_FILE="/tmp/waggle-${AGENT_NAME}.jsonl"
pkill -f "waggle listen.*--name ${AGENT_NAME}-push" 2>/dev/null || true
sleep 0.2
waggle listen --name "${AGENT_NAME}-push" --output "$LISTEN_FILE" &
disown
```

Replace it with something like:

```bash
$TIMEOUT_CMD 2 waggle runtime watch --agent "$AGENT_NAME" --source claude-session-start >/dev/null 2>&1 || true
UNREAD=$($TIMEOUT_CMD 2 waggle runtime pull --agent "$AGENT_NAME" 2>/dev/null) || UNREAD=""
```

- [ ] **Step 4: Update installer tests**

Add assertions that the installed hook includes runtime watch/pull commands and excludes background listener management.

- [ ] **Step 5: Run install tests**

Run: `go test ./internal/install -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/install/claude-code/hook.sh integrations/claude-code/hook.sh internal/install/claude_code.go internal/install/claude_code_test.go
git commit -m "fix: make Claude integration use machine runtime"
```

### Task 6: Add Spawn Auto-Registration and Documentation

**Files:**
- Modify: `cmd/spawn.go`
- Modify: `README.md`
- Test: `internal/spawn/manager_test.go` or command-level spawn tests

- [ ] **Step 1: Write the failing spawn/runtime test**

Add a test that verifies spawned agents register watch intent through the same runtime path instead of a special-case path.

Use either a command test with a stub runtime client or a targeted helper test around the registration call site.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd ./internal/spawn -run 'TestSpawn.*Watch' -count=1`

Expected: FAIL because spawn does not yet register the runtime watch.

- [ ] **Step 3: Update spawn to register watch intent**

After resolving the final `project_id` and `spawnName`, call the runtime watch registration path with source `spawn`.

Do this through a shared helper so:

- explicit watch registration
- adapter auto-registration
- spawn auto-registration

all use the same store/runtime request path.

- [ ] **Step 4: Update docs**

Document the new automatic delivery model in `README.md`:

- broker is per-project
- runtime is machine-local
- Claude no longer starts a listener from startup hook
- idle-time delivery comes from OS notifications
- in-tool surfacing happens at safe boundaries
- explicit runtime watch is the reliable fallback

- [ ] **Step 5: Run verification**

Run:

```bash
go test ./... -count=1
go build ./...
```

Expected: both PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/spawn.go README.md
git commit -m "feat: auto-register runtime watches from spawn"
```

## Self-Review

### Spec coverage

- Machine-local runtime: covered by Tasks 1-4
- Shared watch model: covered by Tasks 1-4 and Task 6
- Safe Claude adapter: covered by Task 5
- Spawn integration: covered by Task 6
- Single source of truth and no dual paths: enforced by shared watch/store/runtime path across Tasks 2-6

### Placeholder scan

The plan intentionally leaves CLI names under `waggle runtime ...` as the implementation choice for v1. This is a concrete path, not a placeholder. No `TODO` or undefined task references remain.

### Type consistency

Core shared types used throughout the plan:

- `runtime.Watch`
- `runtime.DeliveryRecord`
- `runtime.Store`
- `runtime.Manager`

No later task renames these concepts.

## Verification Checklist

Before claiming implementation complete, verify:

```bash
go test ./... -count=1
go build ./...
rg -n "waggle listen|disown|pkill -f .*waggle listen" internal/install/claude-code/hook.sh integrations/claude-code/hook.sh
```

Expected:

- tests pass
- build passes
- the hook grep returns no `waggle listen`/`disown`/listener `pkill` matches
