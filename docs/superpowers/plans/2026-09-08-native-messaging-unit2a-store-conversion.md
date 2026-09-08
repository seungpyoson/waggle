# Native Messaging Unit 2a: Offline Store Conversion and Activation — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An existing schema-v1 project store (`~/.waggle/data/<hash>/state.db`, tasks only) can be converted offline to the native schema (v2) with its tasks preserved, then explicitly activated, so the new broker can start on it; a prepared store can be rolled back to its snapshot.

**Architecture:** Conversion is an offline, single-transaction upgrade performed by `internal/brokerstate` (the only package allowed to open SQLite or remove files), guarded by a writer census (no old broker process, no open handles on the database or its WAL/SHM) and preceded by a consistent snapshot. Activation is a separate, ownership-guarded fenced transaction (`prepared → active`) shared with fresh initialization. The CLI exposes `waggle store inspect|convert|activate|rollback`. Machine-wide cutover (old runtime-database import, launcher retirement, executable replacement) is NOT in this unit; it remains M2 work per the design.

**Tech Stack:** Go 1.26, modernc.org/sqlite, cobra; macOS `lsof -F` and `ps` for the census (Darwin build tag; other platforms return an explicit "census unavailable" error that blocks conversion).

**Spec:** `docs/native-messaging-plan.md` — sections "Ordered delivery and persistence migration" (lines 242-262) and "Exclusive broker ownership"; project rules in `CLAUDE.md` "Design Principles".

## Global Constraints

- Zero hardcodes: file names (`state.db`, legacy `waggle.pid`/`broker.sock`, snapshot directory name), schema versions and timeouts are defined only in `internal/config/`.
- Single source of truth: one DDL text per table, shared by fresh creation and conversion; `schema_version` is the only version record; `cutover` is the only activation record (no marker files).
- No dual paths: fresh initialization and conversion both end in the same `Activate` transaction; there is no auto-migration on open (`OpenStore` still rejects v1 with `ErrSchemaVersion`).
- Fail loud: any census uncertainty (missing tool, parse error, empty partial listing, surviving handle, old process alive) blocks mutation with a specific error; an interrupted conversion leaves the source untouched (transaction) and the snapshot present.
- Never block the host: every new command's `--help` works from any directory with no broker, git or network.
- Only `internal/brokerstate` may call `sql.Open` or `os.Remove` (architecture guard `TestOwnershipHasNoAlternate`).
- Preserve the existing project-ID-to-hash mapping and the exact `config.NewPaths(projectID).DB` path.
- Every duration in tests comes from config values; socket tests run from the checkout with the package's short-path helper.
- Commit trailers: `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` and `Claude-Session: https://claude.ai/code/session_016Efcs8YDH46HY76SRRKwP7`. Never `git add -A`; never stage `.tmp-envoy`.

---

### Task 1: Schema split, provenance, shared activation, offline conversion core

**Files:**
- Modify: `internal/config/config.go` (legacy endpoint names, snapshot dir), `internal/config/ownership.go` (conversion config)
- Modify: `internal/brokerstate/acquire.go` (split DDL; provenance columns on `cutover`)
- Create: `internal/brokerstate/convert.go`, `internal/brokerstate/convert_test.go`
- Modify: `internal/tasks/store.go` (`UpgradeFromV1`), `internal/tasks/store_test.go`
- Modify: `internal/broker/broker.go` (`Initialize` uses shared `Activate`)

**Interfaces:**
- Consumes: `brokerstate.Acquire`, `ownershipSchema`, `tasks.Schema()`, `messages.Schema`, `config.NativeSchemaVersion`.
- Produces:
  ```go
  // internal/config/config.go
  Defaults.LegacyPIDFile    = "waggle.pid"
  Defaults.LegacySocketFile = "broker.sock"
  Defaults.SnapshotDir      = "rollback"          // under Paths.DataDir
  Paths.LegacyPID, Paths.LegacySocket, Paths.SnapshotDir string   // resolved by NewPaths
  const LegacySchemaVersion = 1                      // internal/config/ownership.go

  // internal/brokerstate/convert.go
  type ConversionConfig struct { Database, SnapshotDir string; TransactionTimeout time.Duration }
  func NewConversionConfig(paths config.Paths) ConversionConfig
  type Handle struct { PID int; Command string; Path string }
  type WriterCensus interface {
      // OpenHandles lists processes holding any of paths (or their -wal/-shm siblings) open.
      OpenHandles(ctx context.Context, paths []string) ([]Handle, error)
      // WaggleProcesses lists running processes whose executable basename is the Waggle binary, excluding self.
      WaggleProcesses(ctx context.Context) ([]Handle, error)
  }
  var ErrCensusUnavailable = errors.New("writer census unavailable; conversion blocked")
  var ErrWritersPresent    = errors.New("old Waggle writers or open handles present; conversion blocked")
  var ErrNotLegacy         = errors.New("store is not a schema-v1 legacy store")
  var ErrNotPrepared       = errors.New("store is not in the prepared cutover state")
  type Report struct { Database, Snapshot string; FromVersion, ToVersion int; Tasks int; ConvertedAt time.Time }
  // Convert performs the offline upgrade: census → VACUUM INTO snapshot → one BEGIN IMMEDIATE transaction
  // (verify exactly one schema_version row == LegacySchemaVersion; create ownership tables; tasks.UpgradeFromV1;
  // messages.Schema; cutover row prepared with provenance; schema_version := NativeSchemaVersion) → journal_mode=wal.
  func Convert(ctx context.Context, cfg ConversionConfig, census WriterCensus) (Report, error)
  // Rollback restores the newest snapshot over the database while the store is still prepared (refuses if active
  // or if the census finds writers). It removes -wal/-shm siblings of the target first.
  func Rollback(ctx context.Context, cfg ConversionConfig, census WriterCensus) (Report, error)
  type Inspection struct { Database string; Exists bool; SchemaVersion int; Cutover string; Provenance *Provenance; LegacyPIDPresent, LegacySocketPresent bool; Snapshots []string }
  type Provenance struct { SourceSchema int; ConvertedAt string; Snapshot string }
  // Inspect is read-only (opens mode=ro, never creates, never changes journal mode).
  func Inspect(ctx context.Context, cfg ConversionConfig, paths config.Paths) (Inspection, error)
  // Activate is the single prepared→active transition, used by broker.Initialize and `waggle store activate`.
  func (o *Owner) Activate(ctx context.Context) error   // fenced Write: UPDATE cutover SET state='active' WHERE state='prepared'; 0 rows → ErrNotPrepared unless already active (then nil)

  // internal/tasks/store.go
  func UpgradeFromV1() string   // ALTER TABLE tasks ADD COLUMN ttl INTEGER  (the only v1→v2 tasks difference)
  ```
- DDL split in `acquire.go`: `schemaVersionDDL` (the `schema_version` table) and `ownershipTables` (broker_owner, cutover, endpoint_binding). Fresh creation executes both; conversion executes only `ownershipTables`. The `cutover` table gains nullable provenance columns `source_schema INTEGER, converted_at TEXT, snapshot TEXT`; fresh stores insert `(1,'prepared',NULL,NULL,NULL)`.

- [ ] **Step 1: Tests first (RED).** In `convert_test.go` (use `statetest`-style temp stores; the v1 fixture is created by executing the exact DDL from `main` at 5fac053 `internal/tasks/store.go:128-153`, embedded as a test constant with a comment naming that commit, then `INSERT INTO schema_version VALUES (1)` and three tasks):
  - `TestConvertUpgradesLegacyStoreAndPreservesTasks`: after `Convert`, `schema_version` is exactly one row = 2; `cutover` = prepared with provenance (source 1, snapshot path exists); the three tasks are intact and `ttl` is NULL; `PRAGMA table_info(tasks)` equals that of a fresh `CreateStore`+`Initialize` store (column names, types, defaults, in order); `PRAGMA journal_mode` = wal; `Acquire(OpenStore)` succeeds and `RequireActive` returns `ErrPrepared`; after `owner.Activate`, `RequireActive` is nil.
  - `TestConvertRefusesNonLegacyAndDuplicateVersions`: a fresh v2 store → `ErrNotLegacy`; a v1 store with two version rows → error mentioning duplicate; a missing file → error; a symlink → error. Source unchanged after each refusal (compare file hash).
  - `TestConvertBlocksOnCensus`: a fake census returning a handle → `ErrWritersPresent`, no snapshot created, source unchanged; a fake census returning an error → `ErrCensusUnavailable`.
  - `TestConvertIsAtomicOnFailure`: inject failure after the snapshot by using a fake census whose second call (Convert calls the census twice: before snapshot and immediately before the transaction, per design "recheck immediately before conversion") reports a writer → source unchanged, snapshot present, error `ErrWritersPresent`.
  - `TestRollbackRestoresSnapshotWhilePrepared`: convert, then `Rollback` → schema_version 1, tasks intact, no -wal/-shm; after `Activate`, `Rollback` → `ErrNotPrepared`.
  - `TestInspectIsReadOnly`: `Inspect` on a v1 store reports version 1, cutover "", legacy endpoint flags per files present; file hash unchanged; on a missing file `Exists=false` without creating it.
  - In `internal/tasks/store_test.go`: `TestUpgradeFromV1MatchesFreshSchema` — apply v1 DDL + `UpgradeFromV1()` on one in-memory store and `Schema()` on another; `PRAGMA table_info` and index lists are equal.
- [ ] **Step 2: Run RED** (`go test -race -run 'Convert|Rollback|Inspect|UpgradeFromV1' ./internal/brokerstate/ ./internal/tasks/`).
- [ ] **Step 3: Implement** config additions; DDL split and provenance columns; `UpgradeFromV1`; `Convert`/`Rollback`/`Inspect`/`Activate`; `broker.Initialize` = domain schema + `owner.Activate`. Snapshot name: `state.db.v1-<RFC3339Nano UTC>` under `Paths.SnapshotDir`; use `VACUUM INTO ?` (consistent, includes committed WAL). Convert opens the source with `mode=rw`, `busy_timeout` from config, one connection; the transaction is `BEGIN IMMEDIATE`; `UPDATE schema_version SET version=? WHERE version=?` must affect exactly one row.
- [ ] **Step 4: GREEN**, then `go test -race -count=3 ./internal/brokerstate/... ./internal/tasks/ ./internal/broker/` and `go test -run TestOwnershipHasNoAlternate -count=1 .`.
- [ ] **Step 5: Commit** `feat(brokerstate): offline v1→v2 store conversion, snapshot rollback, shared activation`.

### Task 2: Darwin writer census and legacy endpoint retirement

**Files:**
- Create: `internal/brokerstate/census_darwin.go`, `internal/brokerstate/census_other.go`, `internal/brokerstate/census_darwin_test.go`
- Modify: `internal/brokerstate/convert.go` (retire legacy endpoints after a successful conversion)

**Interfaces:**
- Produces: `type OSWriterCensus struct{ Binary string }` implementing `WriterCensus`; `func NewOSWriterCensus() (OSWriterCensus, error)` (resolves own executable basename via `os.Executable`). Darwin: `OpenHandles` runs `lsof -F pcfn -- <paths...>` (include `-wal`/`-shm` siblings; parse the `-F` field format; a non-zero exit with empty output means "no handles" ONLY when lsof's exit code is 1 and stderr is empty — otherwise `ErrCensusUnavailable`); `WaggleProcesses` runs `ps -axo pid=,comm=` and matches basename == own binary basename, excluding `os.Getpid()`. `census_other.go` returns `ErrCensusUnavailable` for both. Convert must, after the transaction commits, remove `Paths.LegacyPID` and `Paths.LegacySocket` if present (they belong to the retired broker; the census already proved no owner), reporting what it removed in `Report.RetiredEndpoints []string`.

- [ ] **Step 1: Tests (RED).** `TestOSWriterCensusSeesOwnOpenHandle`: open a temp file in the test and keep it open → `OpenHandles` reports this PID; close it → empty. `TestOSWriterCensusDetectsRunningBinary`: build nothing; instead start `sleep` under a copied executable name? No — copy `/bin/sleep` to a temp dir as `<own basename>` and run it: `WaggleProcesses` reports it; kill it; empty. `TestConvertRetiresLegacyEndpoints`: with fake census, create empty `waggle.pid` and `broker.sock` files in the data dir → after Convert they are gone and listed in the report.
- [ ] **Step 2: RED, Step 3: implement, Step 4: GREEN** (`go test -race -count=3 ./internal/brokerstate/...`).
- [ ] **Step 5: Commit** `feat(brokerstate): darwin writer census (lsof/ps) gates conversion; retire legacy endpoints`.

### Task 3: CLI `waggle store inspect|convert|activate|rollback`

**Files:**
- Create: `cmd/store.go`, `cmd/store_test.go`

**Interfaces:**
- Consumes: Task 1/2 functions, `config.ResolveProjectID`, `config.NewPaths`, `brokerstate.Acquire(OpenStore)`, `Owner.Activate`, `Owner.BeginShutdown/Wait`.
- Produces: `waggle store inspect` (JSON of `Inspection`, exit 0 even when the store is missing), `waggle store convert` (runs `Convert` with `NewOSWriterCensus`; prints `Report`; `SHUTDOWN_INCOMPLETE`-style error codes: `CONVERSION_BLOCKED` for census, `NOT_LEGACY`, `CONVERSION_FAILED`), `waggle store activate` (acquires ownership with `OpenStore`, `Activate`, orderly release via `BeginShutdown`+`Wait` bounded by `ShutdownTimeout`; refuses with `BROKER_RUNNING` when acquisition reports a live owner), `waggle store rollback` (calls `Rollback`; refuses when active). All commands resolve the project like `start` does. `--help` of each works with no broker/git/network.

- [ ] **Step 1: Tests (RED)** in `cmd/store_test.go`: run the cobra commands in-process against a temp HOME with `WAGGLE_PROJECT_ID` set: inspect on missing store; convert on a v1 fixture then inspect shows version 2/prepared; activate then inspect shows active; rollback after activate fails with the documented code; rollback on a fresh conversion succeeds. Use a fake census injected through a package-level constructor variable ONLY if no cleaner seam exists — prefer a `newCensus` function value on the command struct set by tests.
- [ ] **Step 2–4: RED → implement → GREEN** (`go test -race -count=3 ./cmd/`).
- [ ] **Step 5: Commit** `feat(cli): waggle store inspect/convert/activate/rollback`.

### Task 4: End-to-end upgrade proof

**Files:**
- Modify: `e2e_test.go`

- [ ] **Step 1: Test** `TestE2E_LegacyStoreConvertsActivatesAndServes`: in an isolated HOME, create a v1 store at the exact `NewPaths(projectID).DB` in the REAL legacy shape (ruling after Task 1 review: main's tasks DDL plus the runtime `ttl` ALTER, main's legacy `messages` table with its four runtime ALTERs and a few rows, WAL journal mode; reuse the Task 1 fixture constants) with two tasks (one with a non-NULL ttl), plus legacy `waggle.pid`/`broker.sock` files; run `waggle start --foreground` → must fail with the schema-version message and exit non-zero; run `waggle store convert` → exit 0, report JSON, legacy files gone, snapshot present; `waggle store inspect` → version 2, prepared, `legacy_messages` listed as preserved; `waggle start --foreground` on the prepared store → starts (maintenance ownership) but `waggle task claim` still works? (tasks are not gated by cutover; assert `waggle task list` returns the two tasks) and an enrollment RPC is refused with the prepared error; `waggle stop`; `waggle store activate` → exit 0; start again → `waggle status` OK and `waggle task list` still shows both tasks with the ttl value intact; stop. Second scenario: `waggle store rollback` right after convert restores version 1 (inspect) and the legacy start error returns.
- [ ] **Step 2: Run** `go test -race -count=1 -run TestE2E_LegacyStore .` from the checkout (TMPDIR default).
- [ ] **Step 3: Commit** `test(e2e): legacy store converts, activates and serves with tasks preserved`.

### Task 5: Verification and docs

**Files:**
- Modify: `CLAUDE.md` (Troubleshooting: "Upgrading an existing project store" — the four commands and what each does), `docs/native-messaging-plan.md` (Status line: note Unit 2a per-project conversion implemented; machine-wide cutover/runtime import remain M2)

- [ ] **Step 1:** `go build ./... && go vet ./... && go test -race -count=1 ./...` (TMPDIR default), `-count=3` on brokerstate/broker/cmd, degraded `--help` for `store` and its subcommands from /tmp with `env -i`, `git diff --check <base>`.
- [ ] **Step 2:** Convert a COPY of the real local v1 store (`~/.waggle/data/843d17c7950d/state.db` copied into a temp HOME; never the original) with the built binary and record the report in the result file.
- [ ] **Step 3:** Docs edits; commit `docs: document project store upgrade; record Unit 2a status`.
- [ ] **Step 4:** Write `/Volumes/Dev-OWC-Envoy/waggle-native-builds/work/build--native-messaging/reviews/independent-assessment-20260907/unit2a-result.md` (commits, suite tails, help check, real-store-copy conversion report, what remains for M2: runtime.db import, launcher retirement, executable replacement, machine-wide census).
