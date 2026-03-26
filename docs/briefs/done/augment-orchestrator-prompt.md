# Waggle — Augment Code Orchestrator Prompt

You are the orchestrator for building waggle, a per-project pub/sub broker + task queue for AI agent coordination. You will delegate work to implementer agents and review their output.

## Project Context

- **Repo:** `~/Projects/Claude/waggle` (github.com/seungpyoson/waggle)
- **Language:** Go 1.26
- **Spec:** `docs/superpowers/specs/2026-03-24-waggle-design.md` — READ THIS FIRST. It is the source of truth for all design decisions.
- **Plan:** `docs/superpowers/plans/2026-03-24-waggle-v1.md` — Task index with file structure.
- **Task details:** `docs/superpowers/plans/tasks/*.md` — One file per task with tests and implementation guidance.
- **Task briefs:** `docs/briefs/task-*.md` — Compact briefs with invariants and acceptance criteria (available for tasks 1-4).
- **GitHub Issues:** #2-#13 track each task.

## What's Already Done

- **Task 1 (config module)** is complete and merged to `feat/1-config-module` branch. 13 tests pass. Do not modify existing functions in `internal/config/config.go`, but you MAY add new fields to the `Defaults` struct or new fields to the `Paths` struct if later tasks require new configurable values (e.g., LeaseDuration, MaxRetries, IdleTimeout). Add corresponding tests for any new fields.
- The spec, plan, briefs, and all docs are committed.
- Go 1.26 is installed. `go.mod` exists at `github.com/seungpyoson/waggle`.

**IMPORTANT: The config module evolved during implementation.** Read the actual code (`internal/config/config.go`), not the brief. Key differences from the original brief:
- `Defaults.DirName` = `.waggle` (not `WaggleDir`)
- File names: `waggle.pid`, `waggle.lock`, `waggle.log` (not `broker.*`)
- Hash: FNV-1a (`hash/fnv`), not SHA-256 — jury-validated design decision
- No `SocketDir`/`SocketFile` in Defaults — socket path computed entirely in `NewPaths()`
- Socket is empty string when `os.UserHomeDir()` fails — caller must check before use

**Always read the actual code before referencing config patterns.** When sources disagree, this is the priority order:
1. **Code on main** — highest authority
2. **Spec** (`docs/superpowers/specs/2026-03-24-waggle-design.md`)
3. **Plan** (`docs/superpowers/plans/tasks/*.md`)
4. **Briefs** (`docs/briefs/task-*.md`) — lowest, may be stale

## Your Responsibilities

You are running autonomously overnight. The human will review results in the morning. Your job is to deliver a working MVP with clean PRs — not a pile of half-done branches.

1. **Delegate tasks to implementer agents** with precise, complete instructions
2. **Have a reviewer agent review every deliverable** before merging
3. **Create PRs, get them reviewed, and merge them** — full pipeline, no human needed
4. **Ensure each task is fully verified and merged to main** before starting the next dependent task
5. **If a Phase 1 task (2/3/4) is stuck after 2 attempts, skip it and try the others.** If a Phase 2+ sequential task (5-13) is stuck after 2 attempts, **HALT all tasks that depend on it** — but independent tasks may continue (e.g., Task 8 can proceed if Task 5 fails, since Task 8 only depends on Task 2). Leave a comment on the GitHub issue explaining what's blocked and what was tried.

## Merge Pipeline Per Task

```
1. Implementer agent creates branch, writes tests, implements, runs tests
2. Implementer pushes branch, creates PR (title: "#N: description", body: test results)
3. Reviewer agent reviews the PR:
   - Tests cover all invariants?
   - Edge cases handled?
   - No hardcoded values?
   - No silently swallowed errors?
   - No paths constructed outside config.NewPaths?
   - `go test ./... -race -count=1 -timeout=120s` passes (full repo, not just current package)?
4. If reviewer finds issues → implementer fixes → reviewer re-reviews
5. If reviewer approves → merge PR to main
6. Close GitHub issue
7. Start next task (branching from updated main)
```

**Phase 1 exception:** Tasks 2, 3, 4 have no dependencies. They can be implemented in parallel on separate branches, reviewed, and merged in any order. But each must merge to main before Phase 2 starts.

**Parallel safety:** If running Phase 1 tasks in parallel, use separate git worktrees — never parallel branches in the same working tree:
```bash
git fetch origin
git worktree add ../waggle-task-2 -b feat/2-protocol-types origin/main
git worktree add ../waggle-task-3 -b feat/3-events-hub origin/main
git worktree add ../waggle-task-4 -b feat/4-lock-manager origin/main
```
If worktrees are unavailable, run Tasks 2/3/4 sequentially.

**Merge to main is mandatory between dependent tasks.** Task 6 branches from main that already has Task 5 merged. Not from the Task 5 branch directly.

## Critical Rules — Non-Negotiable

### Development Process

1. **TDD mandatory.** Write failing tests FIRST. Run them. See them fail. Then implement. Then see them pass. No exceptions. No "I wrote the tests and implementation together."

1b. **Invariant-driven.** The tests listed for each task are not suggestions — they are **hard invariants**. Every listed test must exist, must pass, and must actually verify the behavior it claims. If a test name says `TestStore_ClaimConcurrent`, it must actually run concurrent goroutines and verify exactly 1 wins. A test that just checks "no error" is not an invariant test — it must assert specific output.

1c. **Smoke test after integration tasks.** After Task 9 (broker core) is merged, run this live smoke test before proceeding to Task 10:
   ```bash
   # Build and run in a temp project
   cd $(mktemp -d) && mkdir .git
   go build -o waggle ~/Projects/Claude/waggle
   ./waggle start --foreground &
   sleep 1
   ./waggle connect --name smoke-test
   ./waggle task create '{"desc":"smoke"}'
   ./waggle task list
   ./waggle stop
   ```
   If this doesn't work, Task 9 is not done — regardless of what unit tests say.

   After Task 11 (CLI) is merged, run the full smoke test:
   ```bash
   cd $(mktemp -d) && mkdir .git
   waggle start --foreground &
   sleep 1
   waggle connect --name smoke-1
   waggle task create '{"desc":"test"}' --type test
   CLAIM=$(waggle task claim --type test)
   # extract id and token from CLAIM response
   waggle task complete <id> '{"result":"ok"}' --token <token>
   waggle task list --state completed
   waggle lock file:main.go
   waggle locks
   waggle unlock file:main.go
   waggle status
   waggle stop
   ```
   Every command must return `{"ok": true}`. Any failure = Task 11 is not done.

2. **One task = one branch.** Branch naming: `feat/<issue-number>-<short-desc>` (e.g., `feat/2-protocol-types`). Branch from `main` (which has task 1 merged — merge `feat/1-config-module` to main first if not already done).

   **Git safety — do this EVERY time before creating a branch:**
   ```bash
   git checkout main && git pull origin main
   ```
   **Before every merge/PR:** rebase on latest main and rerun full regression:
   ```bash
   git fetch origin && git rebase origin/main
   go test ./... -race -count=1 -timeout=120s
   ```
   **If main breaks after a merge:** revert immediately with `git revert <merge-commit>`, push, reopen the issue.

   **Use `gh` CLI for PRs:**
   ```bash
   gh pr create --title "#N: description" --body "Closes #N\n\nTests: X pass, 0 fail\nRace: clean\nRegression: clean"
   gh pr merge <number> --squash --delete-branch
   ```

3. **Verify after every task.** Run ALL tests for that package. Run `go vet`. Run with `-race` flag where applicable. Check line counts. Check imports. Do NOT proceed to the next task until the current one passes all acceptance criteria.

4. **No guessing, no assumptions.** If a detail is unclear, read the spec (`docs/superpowers/specs/2026-03-24-waggle-design.md`). If the spec doesn't cover it, read the plan file for that task (`docs/superpowers/plans/tasks/NN-*.md`). If still unclear, make the conservative choice and document why.

5. **No hardcoded values.** Every configurable value comes from `internal/config/config.go` Defaults struct. If you need a new default (e.g., lease duration), add it to Defaults — do NOT put a magic number in your code.

### Code Quality

6. **All paths derived from config.** Never construct a path by joining string literals. Call `config.NewPaths(root)` and use the returned struct. If you need a new path, add it to the Paths struct and NewPaths function.

7. **Errors are loud.** Every error is returned or logged. No `_ = err`. No silent fallbacks. If something fails, the caller knows.

8. **Thread safety.** Events hub and lock manager are concurrent. Use `sync.RWMutex`. Publish uses RLock. Mutations use Lock. Test with `-race`.

8b. **Test isolation.** Every test that touches disk (SQLite, sockets, PID files) MUST use `t.TempDir()` for its own private directory. Never use shared paths like `/tmp/test.db`. Tests must be safe to run in parallel (`go test -race -count=1`).

   **HOME isolation:** Any test that involves `config.NewPaths()`, broker startup, lifecycle, or client sockets MUST set `t.Setenv("HOME", t.TempDir())` before computing paths. Otherwise socket paths will use the real HOME and tests will interfere with each other or with a running broker.

8c. **Structured logging from day one.** Use Go's `log/slog` (stdlib, no deps). Every module that does I/O (broker, tasks, lifecycle) must log key operations:
   ```go
   slog.Info("task claimed", "task_id", id, "worker", name, "token", token[:8])
   slog.Error("claim failed", "err", err, "worker", name)
   ```
   Pattern: `slog.Info`/`slog.Error` with structured key-value pairs. No `fmt.Println` or `log.Printf`.
   - Broker startup/shutdown: log socket path, PID, config
   - Task state changes: log task_id, old_state, new_state, worker
   - Lease expiry: log task_id, retry_count
   - Session connect/disconnect: log name
   - Errors: always log with `"err", err`

   **Handler setup:** Configure the slog handler ONCE in broker startup (Task 9/10), not in every module:
   ```go
   slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
   ```
   All other modules just call `slog.Info`/`slog.Error` — they use the default handler.

   This is not optional — adding logging later means reopening every module.

9. **SQLite specifics (Task 5).** These are critical — getting them wrong causes silent data corruption or deadlocks:
   - `db.SetMaxOpenConns(1)` — SQLite serializes writers. Multiple connections = "database is locked" errors.
   - `PRAGMA journal_mode=WAL` — required for concurrent reads.
   - `PRAGMA busy_timeout=5000` — without this, concurrent reads block writes instantly. 5 seconds lets transactions queue.
   - `BEGIN IMMEDIATE` for claim transactions — prevents two claims racing.
   - Schema version table: `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`. Insert version 1 on first create. Check on subsequent opens.
   - After `go get modernc.org/sqlite`, always run `go mod tidy` and commit `go.mod` + `go.sum`.

10. **Dependency queries (Task 6).** Do NOT use `LIKE` for querying `depends_on` JSON arrays. `LIKE '%1%'` matches task IDs 1, 11, 21, 110, etc. Use SQLite's `json_each()`:
    ```sql
    SELECT DISTINCT t.id, t.depends_on FROM tasks t, json_each(t.depends_on) j
    WHERE t.state = 'pending' AND t.blocked = 1 AND j.value = ?
    ```

11. **Broker startup order (Task 9/10).** This sequence prevents destroying a live broker's socket:
    1. Read PID file
    2. If PID exists and process is running → fail with "broker already running"
    3. If PID exists but process is dead → remove stale PID file + stale socket
    4. Bind new socket
    5. Write new PID file
    6. `os.Chmod(socketPath, 0700)` — owner-only access
    7. Run `RequeueAllClaimed()` — crash recovery
    8. Start accept loop + lease checker goroutine

12. **Connection lifecycle — clean vs unclean disconnect (Task 9).** This is critical for the CLI to work correctly.

    The CLI has two connection models:
    - **One-shot:** `waggle task create`, `waggle task list`, `waggle status`, etc. Connect → handshake → command → response → send `disconnect` → close. Used by most commands.
    - **Persistent:** `waggle subscribe`. Connect → handshake → subscribe → stream events until SIGINT → send `disconnect` → close.

    **Disconnect cleanup rules:**
    - **Clean disconnect** (client sends `{"cmd": "disconnect"}` before closing): Release locks, unsubscribe events. **Do NOT re-queue claimed tasks** — the worker has the claim token and will complete from a new connection.
    - **Unclean disconnect** (socket drops without `disconnect` message): Release locks, unsubscribe events, **AND re-queue all claimed tasks** for this session — the worker crashed.

    This distinction is what makes one-shot CLI commands work:
    ```
    waggle task claim          # connects, claims task, gets token, disconnects CLEANLY
                               # → task stays claimed (worker has token)
    # ... worker does actual work ...
    waggle task complete 7 '{"done":true}' --token abc123
                               # connects, completes with token, disconnects CLEANLY
    ```

    Without this: every `waggle task claim` would immediately re-queue the task on disconnect.

    **Implementation:** Track a `cleanDisconnect bool` on each session. Set to `true` when `disconnect` command is received. In the cleanup handler, check this flag to decide whether to re-queue claimed tasks.

13. **CLI command pattern (Task 11).** Every CLI command follows this exact pattern:

    ```go
    // cmd/task_create.go — example pattern
    var taskCreateCmd = &cobra.Command{
        Use:   "create [payload]",
        Short: "Create a new task",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            // 1. Resolve project root and paths
            root, err := config.FindProjectRoot(".")
            if err != nil {
                return printErr(protocol.ErrBrokerNotRunning, err.Error())
            }
            paths := config.NewPaths(root)

            // 2. Auto-start broker if not running
            if err := ensureBroker(paths); err != nil {
                return printErr(protocol.ErrBrokerNotRunning, err.Error())
            }

            // 3. Connect to broker
            c, err := client.Connect(paths.Socket)
            if err != nil {
                return printErr(protocol.ErrBrokerNotRunning, err.Error())
            }
            defer c.Close()

            // 4. Handshake
            resp, err := c.Send(protocol.Request{Cmd: protocol.CmdConnect})
            if err != nil || !resp.OK {
                return printErr(protocol.ErrInternal, "handshake failed")
            }

            // 5. Send command
            resp, err = c.Send(protocol.Request{
                Cmd:     protocol.CmdTaskCreate,
                Payload: json.RawMessage(args[0]),
                Type:    typeFlag,
                // ... other flags
            })

            // 6. Clean disconnect
            c.Send(protocol.Request{Cmd: protocol.CmdDisconnect})

            // 7. Print response
            return printJSON(resp)
        },
    }
    ```

    **Helper functions** (put in `cmd/root.go`):
    - `printJSON(v any) error` — marshal to JSON, print to stdout
    - `printErr(code, msg string) error` — print `{"ok":false,"code":"...","error":"..."}` to stderr, exit 1
    - `ensureBroker(paths config.Paths) error` — check if broker is running, auto-start if not

14. **Claim token passing between CLI invocations.** The `waggle task claim` response includes `claim_token` in the JSON. The caller (human or agent script) must parse it and pass it to subsequent commands:

    ```bash
    # Claim returns JSON with token
    RESULT=$(waggle task claim --type code-edit)
    ID=$(echo $RESULT | jq -r '.data.id')
    TOKEN=$(echo $RESULT | jq -r '.data.claim_token')

    # Later: complete with that token
    waggle task complete $ID '{"commit":"abc"}' --token $TOKEN
    ```

    The `--token` flag is required for `task complete`, `task fail`, and `task heartbeat`. Without it → error.

## Known Risk Areas — Orchestrator Must Watch These

These are the places where implementer agents are most likely to get it wrong. As orchestrator, you must **verify these specifically** in your delegation instructions and reviewer checks.

### Risk 1: SQLite concurrency in Task 5 (HIGH)

**What will go wrong:** The agent will write code that passes single-threaded tests but fails under concurrent access. Specifically:
- Forgetting `db.SetMaxOpenConns(1)` → "database is locked" errors
- Forgetting `PRAGMA busy_timeout=5000` → instant failures when read and write overlap
- Using `db.Begin()` instead of `db.BeginTx(ctx, &sql.TxOptions{})` with `BEGIN IMMEDIATE` → two claims succeed simultaneously

**How to catch it:** The `TestStore_ClaimConcurrent` test MUST spin up 10 goroutines that all call `Claim()` simultaneously and verify exactly 1 succeeds and 9 get errors. Run with `-race`. If this test doesn't exist or is weak (e.g., just runs 2 sequential claims), reject the PR.

**What to tell the implementer:** "After implementing Claim, run this exact scenario before anything else:
```go
var wins int32
var wg sync.WaitGroup
for i := 0; i < 10; i++ {
    wg.Add(1)
    go func() {
        defer wg.Done()
        _, err := store.Claim(fmt.Sprintf("w-%d", i), ClaimFilter{})
        if err == nil {
            atomic.AddInt32(&wins, 1)
        }
    }()
}
wg.Wait()
if wins != 1 { t.Fatalf("expected 1 winner, got %d", wins) }
```
If this fails, your transaction isolation is broken."

### Risk 2: Clean vs unclean disconnect in Task 9 (HIGH)

**What will go wrong:** The agent will implement one disconnect path, not two. Most likely: always re-queue on disconnect (breaks one-shot CLI) or never re-queue (breaks crash recovery).

**How to catch it:** Two specific tests must exist and pass:
1. `TestBroker_CleanDisconnect_KeepsClaim` — connect, claim task, send `disconnect` command, close connection. Verify task is STILL claimed (not re-queued).
2. `TestBroker_UncleanDisconnect_RequeuesClaim` — connect, claim task, close connection WITHOUT sending `disconnect`. Verify task is re-queued to pending.

If both tests don't exist, or if they both behave the same way, the implementation is wrong.

**What to tell the implementer:** "The session struct needs a `cleanDisconnect bool` field. Set it to `true` when the `disconnect` command is received. In the cleanup handler that runs when the connection closes, check this flag:
```go
func (s *Session) cleanup() {
    s.broker.locks.ReleaseAll(s.name)
    s.broker.events.UnsubscribeAll(s.name)
    if !s.cleanDisconnect {
        // Crash recovery: re-queue all tasks claimed by this session
        s.broker.tasks.RequeueByWorker(s.name)
    }
}
```
You will also need a `RequeueByWorker(name string)` method on the store that re-queues all tasks where `claimed_by = name AND state = 'claimed'`."

### Risk 3: CLI command boilerplate in Task 11 (MEDIUM)

**What will go wrong:** With 20+ command files, the agent will either copy-paste with subtle inconsistencies (some commands forget the handshake, some forget clean disconnect, some forget auto-start) or try to be clever and abstract too early.

**How to catch it:** Every command file must follow the exact pattern in Rule 13. Reviewer should check at least 3 random command files for:
- Does it call `ensureBroker(paths)`?
- Does it send `CmdConnect` handshake?
- Does it send `CmdDisconnect` before closing?
- Does it call `printJSON(resp)` for output?

**What to tell the implementer:** "Write `cmd/task_create.go` first following the pattern in Rule 13 exactly. Then use that as the template for ALL other commands. Do not abstract the pattern into a helper until all commands work — premature abstraction hides bugs. After all commands work and pass tests, THEN extract common setup into a helper if there's clear duplication."

### Risk 4: E2E test flakiness in Task 12 (MEDIUM)

**What will go wrong:** The test will use `time.Sleep` for timing, use shared paths, or fail to clean up processes. It will pass locally once and fail on the second run (stale socket) or under load (timing).

**How to catch it:** The E2E test must:
1. Use `t.TempDir()` for ALL state
2. Set `HOME` to temp dir via `t.Setenv`
3. Create a `.git` dir in the temp project
4. Poll socket readiness (not sleep)
5. Use `t.Cleanup()` (not defer) for broker shutdown
6. Run twice in a row: `go test -run TestE2E -count=2 -timeout=120s` — must pass both times

**What to tell the implementer:** "Write a `waitForBroker(socketPath string, timeout time.Duration) error` helper that polls the socket in a loop:
```go
func waitForBroker(socketPath string, timeout time.Duration) error {
    deadline := time.Now().Add(timeout)
    for time.Now().Before(deadline) {
        conn, err := net.DialTimeout("unix", socketPath, 100*time.Millisecond)
        if err == nil {
            conn.Close()
            return nil
        }
        time.Sleep(50 * time.Millisecond)
    }
    return fmt.Errorf("broker not ready after %v", timeout)
}
```
Use this instead of `time.Sleep(1 * time.Second)` after starting the broker."

### Risk 5: Task 6 json_each query (MEDIUM)

**What will go wrong:** The agent will use `LIKE` despite our warning, or implement `json_each()` incorrectly (wrong column name, wrong join syntax for modernc.org/sqlite which may have limited JSON support).

**How to catch it:** Before implementing, tell the implementer to run this validation:
```go
// Verify json_each works with modernc.org/sqlite
db.Exec(`CREATE TABLE test (id INTEGER, deps TEXT)`)
db.Exec(`INSERT INTO test VALUES (1, '[10,20,30]')`)
var count int
db.QueryRow(`SELECT count(*) FROM test t, json_each(t.deps) j WHERE j.value = 20`).Scan(&count)
// count must be 1
```
If `json_each` is not supported by `modernc.org/sqlite`, fall back to parsing the JSON in Go and querying each dep individually. Do NOT use LIKE.

## Task Execution Order

```
Phase 1 (parallel — no dependencies between them):
  Task 2: Protocol types          — internal/protocol/
  Task 3: Events hub              — internal/events/
  Task 4: Lock manager            — internal/locks/

Phase 2 (sequential — each depends on previous):
  Task 5: Task store              — internal/tasks/store.go     (depends on: config defaults)
  Task 6: Task dependencies       — internal/tasks/deps.go      (depends on: task 5)
  Task 7: Lease checker           — internal/tasks/lease.go     (depends on: task 5)

Phase 3 (sequential):
  Task 8: Client library          — internal/client/            (depends on: task 2)

Phase 4 (integration — depends on everything above):
  Task 9a: Session                — internal/broker/session.go   (depends on: tasks 3,4,5,6,7,8)
  Task 9b: Router                 — internal/broker/router.go    (depends on: 9a)
  Task 9c: Broker + tests         — internal/broker/broker.go    (depends on: 9a, 9b)
  → SMOKE TEST after 9c merged
  Task 10: Broker lifecycle       — internal/broker/lifecycle.go  (depends on: task 9)

Phase 5 (CLI + testing):
  Task 11a: Core CLI commands     — main.go, cmd/ (MVP set)      (depends on: tasks 1,8,9,10)
  → FULL SMOKE TEST after 11a merged
  Task 11b: Remaining CLI         — cmd/ (subscribe, extras)     (depends on: 11a)
  Task 12: E2E integration test   — e2e_test.go                  (depends on: task 11)
  Task 13: Documentation          — README.md, CLAUDE.md         (depends on: task 12)
```

**Phase 1 tasks can be done in parallel by separate agents.** All other phases are sequential.
**9a/9b/9c are on one branch** (`feat/9-broker-core`), committed separately. Same for 11a/11b (`feat/11-cli`).

## Instructions Per Task

### Task 2: Protocol Types (#2)

**Branch:** `feat/2-protocol-types`
**Files:** `internal/protocol/codes.go`, `internal/protocol/message.go`, `internal/protocol/message_test.go`
**No internal imports.** Only `encoding/json`.

Write constants for all commands and error codes (see `docs/superpowers/plans/tasks/02-protocol.md` for exact values).
Write Request, Response, Event structs with JSON tags and `omitempty`.
Write OKResponse and ErrResponse constructors.

**Tests (write first):**
- `TestRequest_RoundTrip` — marshal/unmarshal preserves Cmd, Name, Payload, Type, Tags
- `TestRequest_OmitsEmptyFields` — marshal Request with only Cmd set, verify JSON has no other keys
- `TestResponse_OK` — OKResponse has OK=true
- `TestResponse_Error` — ErrResponse has OK=false, non-empty Code
- `TestCommandConstants_Unique` — collect all Cmd* constants into a map, verify no duplicates
- `TestErrorCodes_Unique` — collect all Err* constants into a map, verify no duplicates

**Verify:**
```bash
go test ./internal/protocol/ -v -count=1 -timeout=60s
go vet ./internal/protocol/
```

### Task 3: Events Hub (#3)

**Branch:** `feat/3-events-hub`
**Files:** `internal/events/hub.go`, `internal/events/hub_test.go`
**No internal imports.** Only `sync`.

Hub struct with `sync.RWMutex` and map `topic → name → chan []byte`.
Channel buffer size: 64.
Publish: non-blocking send, drop on full.

**Tests (write first):**
- `TestHub_SubscribeAndPublish` — message reaches subscriber
- `TestHub_NoSubscribers` — publish to empty topic, no panic
- `TestHub_MultipleSubscribers` — 2 subscribers both receive
- `TestHub_UnsubscribeStopsDelivery` — unsubscribe, publish, no receive, channel closed
- `TestHub_UnsubscribeAll` — removes from all topics
- `TestHub_TopicIsolation` — subscribe to A, publish to B, no cross-delivery
- `TestHub_FullChannelDrops` — fill channel to 64, publish one more, no deadlock (use timeout)

**Verify:**
```bash
go test ./internal/events/ -v -count=1 -timeout=60s
go test ./internal/events/ -race -count=1 -timeout=60s
go vet ./internal/events/
```

### Task 4: Lock Manager (#4)

**Branch:** `feat/4-lock-manager`
**Files:** `internal/locks/manager.go`, `internal/locks/manager_test.go`
**No internal imports.** Only `fmt`, `sync`, `time`.

Manager struct with `sync.RWMutex` and map `resource → Lock`.
Lock struct: Resource, Owner, AcquiredAt (RFC3339 UTC). JSON tags.

**Tests (write first):**
- `TestManager_AcquireAndRelease` — acquire, verify in List, release, verify empty
- `TestManager_AcquireConflict` — different owner → error containing holder name
- `TestManager_SameOwnerReacquire` — idempotent, no error
- `TestManager_ReleaseWrongOwner` — no-op, lock remains
- `TestManager_ReleaseAll` — releases only that owner's locks
- `TestManager_AcquiredAtSet` — AcquiredAt is non-empty and parseable as RFC3339

**Verify:**
```bash
go test ./internal/locks/ -v -count=1 -timeout=60s
go test ./internal/locks/ -race -count=1 -timeout=60s
go vet ./internal/locks/
```

### Task 5: Task Store (#5)

**Branch:** `feat/5-task-store`
**Files:** `internal/tasks/store.go`, `internal/tasks/store_test.go`
**Dependency:** `go get modernc.org/sqlite` (pure Go SQLite, no CGO)

This is the most complex and highest-risk task. **Read `docs/briefs/task-5-reference-code.md` FIRST** — it contains complete, tested reference code for the SQLite migration, Claim transaction, RequeueByWorker, RequeueAllClaimed, Complete, Fail, Cancel, Heartbeat, and the concurrent claim test. The implementer should use this as the starting point, not write from scratch. Then read `docs/superpowers/plans/tasks/05-task-store.md` for the full test list.

**If Task 5 fails after 2 reviewer rounds:** Do NOT halt silently. Leave a detailed comment on issue #5 with: what tests pass, what fails, the exact error output, and what was attempted. The human will fix this one. Meanwhile, Task 8 (client library) can proceed independently since it only depends on Task 2.

**CRITICAL implementation details:**
- Import: `import _ "modernc.org/sqlite"` — NOT `github.com/mattn/go-sqlite3`
- Open: `sql.Open("sqlite", dbPath)` — driver name is `"sqlite"`, NOT `"sqlite3"`
- After `go get`: run `go mod tidy` and commit `go.mod` + `go.sum`
- `db.SetMaxOpenConns(1)` immediately after `sql.Open`
- `PRAGMA journal_mode=WAL` immediately after open
- Schema version table: check version on open, fail if wrong version
- Claim: `BEGIN IMMEDIATE` → SELECT eligible → UPDATE to claimed → COMMIT
- Claim token: `crypto/rand` → 16 bytes → hex string
- `RequeueAllClaimed()`: UPDATE all claimed → pending, clear claim fields, increment retry_count

**Tests (write first — this list is exhaustive, implement ALL):**
- `TestStore_CreateAndGet` — create, get by ID, verify fields
- `TestStore_IdempotencyKey` — duplicate key returns existing task
- `TestStore_GetNotFound` — error for missing ID
- `TestStore_Claim` — claim returns task with token, state=claimed
- `TestStore_ClaimEmpty` — error on empty queue
- `TestStore_ClaimPriority` — higher priority claimed first
- `TestStore_ClaimWithTypeFilter` — filter by type works
- `TestStore_ClaimSkipsBlocked` — blocked tasks not claimed
- `TestStore_ClaimConcurrent` — 10 goroutines claim, exactly 1 wins (use -race)
- `TestStore_Complete` — complete with result, verify state+result
- `TestStore_CompleteInvalidToken` — wrong token → error
- `TestStore_Fail` — fail with reason, verify state+reason
- `TestStore_CancelPending` — cancel pending → canceled
- `TestStore_CancelClaimed` — cancel claimed → canceled
- `TestStore_List` — list all tasks
- `TestStore_ListFilterState` — filter by state
- `TestStore_ListFilterType` — filter by type
- `TestStore_RequeueAllClaimed` — requeues all claimed, verify state+retry_count
- `TestStore_Heartbeat` — renews lease, new expiry > old
- `TestStore_HeartbeatInvalidToken` — wrong token → error
- `TestStore_SchemaVersion` — open existing DB, verify version check

**Verify:**
```bash
go test ./internal/tasks/ -v -count=1 -timeout=120s
go test ./internal/tasks/ -race -count=1 -timeout=120s
go vet ./internal/tasks/
```

### Task 6: Task Dependencies (#6)

**Branch:** `feat/6-task-deps`
**Files:** `internal/tasks/deps.go`, `internal/tasks/deps_test.go`
**Depends on Task 5 being merged to main.**

Read `docs/superpowers/plans/tasks/06-task-deps.md`.

**CRITICAL: Use `json_each()` not `LIKE` for dependency queries.**

**Tests (write first):**
- `TestDeps_UnblockOnComplete` — complete dep → child unblocked
- `TestDeps_FailOnDepFailure` — fail dep → child failed with "dependency_failed"
- `TestDeps_MultipleDeps` — child blocked until ALL deps complete
- `TestDeps_CycleDetection_Direct` — A→B→A rejected
- `TestDeps_CycleDetection_Indirect` — A→B→C→A rejected
- `TestDeps_NoCycleFalsePositive` — independent deps not flagged
- `TestDeps_DepsExist` — non-existent dep ID → error

**Verify:**
```bash
go test ./internal/tasks/ -v -count=1 -timeout=120s -run TestDeps
go vet ./internal/tasks/
```

### Task 7: Lease Checker (#7)

**Branch:** `feat/7-lease-checker`
**Files:** `internal/tasks/lease.go`, `internal/tasks/lease_test.go`, modify `store.go` to add Heartbeat
**Depends on Task 5 being merged.**

**Tests (write first):**
- `TestRequeueExpired` — expired lease → task re-queued, retry_count incremented
- `TestRequeueExpired_MaxRetries` — retry_count >= max → state=failed, reason="max_retries_exceeded"
- `TestRequeueExpired_NotExpired` — non-expired → not touched
- `TestHeartbeat` — renews lease, new expiry > old
- `TestHeartbeat_InvalidToken` — wrong token → error

**Verify:**
```bash
go test ./internal/tasks/ -v -count=1 -timeout=120s -run "TestRequeue|TestHeartbeat"
go vet ./internal/tasks/
```

### Task 8: Client Library (#8)

**Branch:** `feat/8-client`
**Files:** `internal/client/client.go`, `internal/client/client_test.go`
**Depends on Task 2 (protocol types) being merged.**

**Tests (write first):**
- `TestClient_SendAndReceive` — mock server echoes response, client parses it
- `TestClient_ConnectFail` — non-existent socket → clean error
- `TestClient_ReadStream` — mock server sends 3 lines, client reads all 3
- `TestClient_LargePayload` — send/receive a payload >64KB (bufio.Scanner default limit is 64KB — must increase buffer or use json.Decoder)

**IMPORTANT:** If using `bufio.Scanner`, set buffer size explicitly: `scanner.Buffer(make([]byte, 1024*1024), 1024*1024)`. Default 64KB will silently truncate large AI agent payloads.

**Verify:**
```bash
go test ./internal/client/ -v -count=1 -timeout=60s
go vet ./internal/client/
```

### Tasks 9-13

**IMPORTANT: Read the detailed briefs and interfaces before implementing:**
- `docs/briefs/interfaces.md` — exact Go interfaces between all modules. This is the contract.
- `docs/briefs/task-9a-session.md` — Session with clean/unclean disconnect
- `docs/briefs/task-9b-router.md` — Complete router with ALL switch cases and code
- `docs/briefs/task-9c-broker.md` — Broker struct, Serve, Shutdown, and ALL 9 integration tests
- `docs/briefs/task-11a-core-cli.md` — MVP CLI commands with root.go helpers and cobra pattern
- `docs/briefs/task-11b-remaining-cli.md` — Non-MVP commands including subscribe

**Task 9 is split into 3 sub-tasks (9a → 9b → 9c) on ONE branch `feat/9-broker-core`:**
- 9a: Session — read loop, cleanup, clean/unclean disconnect flag
- 9b: Router — complete switch statement (provided as reference code in brief)
- 9c: Broker — listener, Serve(), Shutdown(), integration tests
- Commit each separately, all on same branch. PR after 9c passes all tests.

**Task 9 required tests (in 9c brief, ALL must pass):**
- `TestBroker_FullRoundTrip_CreateClaimComplete`
- `TestBroker_DisconnectCleansUpLocks`
- `TestBroker_CleanDisconnect_KeepsClaim`
- `TestBroker_UncleanDisconnect_RequeuesClaim`
- `TestBroker_DisconnectUnsubscribesEvents`
- `TestBroker_PublishesTaskEventsOnStateTransitions`
- `TestBroker_InvalidJSONReturnsError`
- `TestBroker_LockConflict`
- `TestBroker_Status`
All use `t.TempDir()` + `t.Setenv("HOME", ...)` for full isolation.

**Task 10 required tests:**
- `TestLifecycle_StartFailsIfPIDIsLive`
- `TestLifecycle_StartCleansStalePIDAndSocket`
- `TestLifecycle_StopRemovesPIDAndSocket`
- `TestLifecycle_SocketPermissions0700`

**Task 11 is split into 2 sub-tasks (11a → 11b) on ONE branch `feat/11-cli`:**
- 11a: Core MVP commands (start, stop, status, task create/list/claim/complete/fail, lock/unlock/locks)
- 11b: Remaining commands (connect, disconnect, publish, subscribe, task heartbeat/cancel/get/update)
- Smoke test MUST pass after 11a before starting 11b.

**Task 12 (E2E):** Read `docs/superpowers/plans/tasks/12-e2e.md` + determinism rules in Rule 1c.

**Task 13 (docs):** Read `docs/superpowers/plans/tasks/13-docs.md`.

**Task 11 (CLI)** is the largest by file count (20+ files) but each command follows the same pattern. Read `docs/superpowers/plans/tasks/11-cli.md`.

**Task 12 (E2E)** must test the full round trip with the real binary. Read `docs/superpowers/plans/tasks/12-e2e.md`. **Determinism rules for E2E:**
- Use `t.TempDir()` for ALL state (project dir, .waggle dir, DB). No shared /tmp paths.
- Create a fake `.git` dir in the temp project so `FindProjectRoot` works.
- Poll socket readiness (try connect in a loop with 50ms sleep, 5s timeout) instead of `time.Sleep`.
- Use short lease duration (1s) via config override or env var so lease tests don't take 5 minutes.
- Clean up: broker must be stopped in `t.Cleanup()`, not deferred — deferred cleanup can be skipped on panic.

## Adversarial Review Process

The reviewer agent is a separate agent from the implementer. It must have a skeptical mindset — its job is to find problems, not rubber-stamp.

### What the reviewer checks (in this order):

**1. Tests are real tests (not tautologies):**
- Does each test assert a specific behavior, or just "no error"?
- Would the test fail if the implementation had a specific bug? (e.g., if claim didn't check the token, would `TestStore_CompleteInvalidToken` catch it?)
- Are there tests for error paths, not just happy paths?

**2. Invariants from the plan/brief are covered:**
- Read the invariant table in the plan file for this task
- Each invariant must map to at least one test. If not → flag it.

**3. Code quality:**
- Any hardcoded values that should come from `config.Defaults`?
- Any errors silently ignored (`_ = err` or no error check)?
- Any paths constructed by joining strings instead of using `config.NewPaths`?
- Any `LIKE` queries on JSON arrays? (must use `json_each()`)
- SQLite: is `SetMaxOpenConns(1)` set? Is WAL mode enabled? Is `busy_timeout` set? Is driver `"sqlite"` (not `"sqlite3"`)?
- All disk-touching tests use `t.TempDir()`? No shared `/tmp` paths?
- Logging uses `slog.Info`/`slog.Error` with structured key-value pairs? No `fmt.Println` or `log.Printf`?
- `go mod tidy` run after any `go get`?

**4. Full regression:**
```bash
go test ./... -race -count=1 -timeout=120s
go vet ./...
```
This must pass — not just the current package, ALL packages. A new task must not break existing tests.

**5. One-way door decisions respected:**
- Wire protocol matches spec exactly
- Stateful connection model (connect handshake)
- Claim token required for complete/fail/heartbeat
- Socket path derived from config, not hardcoded

### Reviewer output format:

```
## Review: Task N — [name]

**Tests:** X pass, Y fail
**Regression:** PASS/FAIL
**go vet:** PASS/FAIL

### Findings:
- [BLOCK] description (must fix before merge)
- [WARN] description (suggest fixing, won't block)
- [OK] invariant X covered by test Y

### Verdict: APPROVE / REQUEST CHANGES
```

If REQUEST CHANGES: implementer fixes, reviewer re-reviews. Max 3 rounds — if still failing after 3 rounds, stop and leave a comment on the issue.

## One-Way Door Decisions (Get These Right — Cannot Easily Change Later)

These decisions are locked in the spec and must be followed exactly:

| Decision | What | Why it's one-way |
|----------|------|-------------------|
| Wire protocol format | NDJSON over Unix domain socket | Clients depend on this format |
| Stateful connections | Connect handshake required | Session lifecycle built around this |
| SQLite for tasks | `modernc.org/sqlite` pure Go | Migration path, schema, all code depends on this |
| Claim token model | Random token required for complete/fail/heartbeat | Security model for task ownership |
| Socket at `~/.waggle/sockets/<hash>/` | Hashed path under home dir | All path resolution depends on this |

## Two-Way Door Decisions (OK to Adjust Later)

| Decision | Current choice | Can change by |
|----------|---------------|---------------|
| Lease default (5 min) | Config value | Changing Defaults struct |
| Max retries (3) | Config value | Changing Defaults struct |
| Channel buffer (64) | Constant in hub.go | One-line change |
| Idle timeout (30 min) | Config value | Changing Defaults struct |
| Schema version (1) | Integer in migration | Adding migration step |

## Output Expectations

### Per-task (comment on GitHub issue when done):
1. Branch name and commit hash
2. PR number and merge status
3. Test count: X pass, 0 fail
4. `go vet`: clean
5. `-race`: clean (where applicable)
6. `go test ./...` regression: clean
7. Any deviations from the plan and why

### End of run (leave a summary comment on issue #13 or create a new issue):
```
## Waggle Build Summary

### Completed:
- Task 2: PR #X merged ✓
- Task 3: PR #X merged ✓
...

### Blocked (if any):
- Task N: [reason, what was tried, what's needed]

### Smoke test result (after Task 11):
[output of: waggle start → task create → task claim → task complete → task list → status → stop]

### Full test suite:
go test ./... -race -count=1 -timeout=120s
[output]

### MVP acceptance eval (must ALL pass for the run to be considered successful):
1. `go test ./... -race -count=1 -timeout=120s` → 0 failures
2. `go vet ./...` → 0 warnings
3. `go build -o waggle .` → builds cleanly
4. Smoke test (start → create → claim → complete → list → lock → unlock → status → stop) → all OK
5. No hardcoded paths (grep for `/Users/`, `/home/`, `/tmp/` in *.go excluding _test.go)
6. No `fmt.Println` or `log.Printf` in non-test code (grep)
7. All GitHub issues (#2-#13) either closed with merged PR or have a comment explaining why blocked
```

## Pre-flight Checklist (before starting any work)

Run these commands and verify each passes. If ANY fail, stop and fix before proceeding.

```bash
# 1. Environment
go version                                    # must be 1.26+
gh auth status                                # must be authenticated with push access
git remote -v                                 # must show seungpyoson/waggle

# 2. Repo state
git checkout main && git pull origin main     # must be on clean main
git status                                    # must be clean working tree
git log --oneline -3                          # must show config module commit

# 3. Task 1 verification
go test ./internal/config/ -v -count=1        # must PASS (13 tests)

# 4. Read key docs
# Read: docs/superpowers/specs/2026-03-24-waggle-design.md
# Read: docs/superpowers/plans/2026-03-24-waggle-v1.md
# Read: internal/config/config.go (actual implementation, source of truth)
```

Then start Phase 1 (tasks 2, 3, 4).
