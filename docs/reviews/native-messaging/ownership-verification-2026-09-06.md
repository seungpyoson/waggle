# Ownership integration verification — 2026-09-06

This records checks of the uncommitted ownership integration, not M1 approval.
HEAD remains `5fac0530a789f54b2a87fb10d9d21a14b2dded91`; the approved design
SHA256 remains `e18636f22a2b970cc5a75384fe29abd6eb6a21361920f109a5435ffee62473b4`.
The existing `GEMINI.md` deletion is preserved.

The user applied the narrow Go proxy allowlist patch and ran
`GOPROXY=https://proxy.golang.org go mod download` successfully in their terminal.
The dependency blocker reported in the earlier preparation records is resolved
for the current build. The agent still cannot write the normal module cache.

## Checks run

Commands used the existing module cache with `GOPROXY=off` and
`GOCACHE="$TMPDIR/waggle-build-cache"`.

- `go build -o waggle .`: exit 0. Go emitted a nonfatal module stat-cache write
  permission warning. The binary was rebuilt after the maintenance correction.
- `go vet ./...`: exit 0, including after that correction.
- `go test ./cmd -run 'Test(Uninstall|RunUninstall)' -count=1 -v`: all five pass.
- `go test -race ./cmd -count=1`: exit 0.
- `go test ./... -v`: exit 1. Broker, client, ownership and runtime integration
  tests encounter denied Unix socket binding or denied OS process inspection.
  Root process tests time out waiting for an unavailable broker endpoint; those
  tests do not capture the child's startup diagnostic, so their individual
  startup causes are not confirmed.
- `TestRuntimeRealProcessesReceiveWatchedAgentMessage`: explicitly skipped by
  its existing Unix bind preflight. This is not a pass.
- All 49 advertised CLI help entrypoints: exit 0 when recursively invoked through
  the built binary from outside a repository, with an empty PATH and isolated
  empty HOME. No state was created. The separate attempt to enforce network
  denial with `sandbox-exec` failed at `sandbox_apply` with EPERM; that stricter
  environment case remains not confirmed.
- Repository formatting command and `git diff --check`: exit 0.

## Correction and remaining work

Inspection found that the maintenance consolidation had dropped the existing
`task.stale` event. The single task maintenance transaction now cancels expired
tasks and reads queue health, collecting the stale event only when required.
Publication remains after commit. The existing `TestBroker_TaskStaleEvent`
exercises this through broker RPC, but cannot pass its socket prerequisite in
this environment. Its behavior after the correction is not confirmed.

The working tree still contains name-push delivery, the legacy runtime store and
hooks, and caller-named acknowledgements. Canonical native enrollment, ordered
attempts, provider drivers, removal of those paths, and offline conversion are
unfinished. Passing the build and CLI checks does not establish native messaging.

## User-provided full race run and shutdown correction

The user subsequently supplied normal-terminal output from
`go test -race ./... -v`: every package passed, with no reported race failures.
`TestBroker_SendMessageBodyTooLarge` was explicitly skipped because its fixture
cannot reach the body validator through the scanner's frame limit. The earlier
environment-skipped real-process runtime test passed in this run. The ownership,
stale-task event, task/dependency rollback, and built-binary E2E tests also passed.
This is user-provided execution evidence; its exact working-tree digest was not
included. It precedes the following shutdown correction.

The repeated `error requeuing tasks ... broker admission is closed` lines are
a defect in the integration, despite the passing suite:

1. Session cleanup calls `Owner.Do` after shutdown has closed admission.
2. Cleanup is triggered when `Owner.Serve` closes accepted connections during
   drain, so this is normal shutdown ordering, not an exceptional timing case.
3. `Broker.Serve` already holds an operation across those connections and their
   teardown, but discarded the operation argument instead of passing it through.
4. The session API treated teardown as newly admitted work, separating its
   persistence authority from the service lifetime that already owns it. This
   reaches the violated lifetime invariant; more checks cannot supply the
   missing capability.

Source inspection of `Broker.Serve`, `Session.readLoop`, `Session.cleanup`,
`Owner.Do`, `Owner.Serve` and the working diff confirms this chain. No evidence
supports dismissing it as harmless logging. The existing tests did not inspect
claims after orderly release and before startup recovery. Runtime execution of
the corrected broker remains unconfirmed in the agent environment.

The correction passes the existing service operation to the connection's read
loop and deferred cleanup. Each new RPC still requests admission separately.
Cleanup has one caller; paired-session teardown closes the peer connection
instead of running its cleanup directly, and the extra `sync.Once` state is
removed. There is no shutdown exemption, new capability constructor, or retry.

`TestAdmissionDrainIncludesWorkAdmittedBeforeShutdown` now verifies a transaction
committed during drain and visible to the next owner: pass under `-race` here.
`TestBrokerShutdownRequeuesClaimsBeforeRelease` drives a live broker, leaves a
claiming client connected during shutdown, then checks persistence through a
new storage owner without running broker recovery. It requires one requeue
before release. The user subsequently ran `go test -race ./internal/broker`
in the normal terminal: exit 0, package duration 28.305s. This runs the whole
broker suite, including the new shutdown regression. The intended filter flags
were pasted as a separate shell command and failed afterward; that shell error
does not invalidate the completed package run. This is user-provided evidence,
with no working-tree digest included.
Build, vet, and compile-only checks of all packages pass after the correction.

Legacy runtime push-token release warnings are separate: that daemon attempts
RPC after broker shutdown. Its entire delivery/cleanup path remains in the
approved removal scope. No log suppression or reconnect retry was added.

No transport substitution, changed dependency version, or broader permission
patch was added to turn blocked integration checks into passes.
