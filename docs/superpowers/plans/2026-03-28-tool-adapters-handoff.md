# Tool Adapters Handoff

**Date:** 2026-03-28
**Worktree:** `/Users/spson/Projects/Claude/waggle/.worktrees/feat-tool-adapters`
**Branch:** `feat/tool-adapters`
**Issue:** `#71`

## 2026-03-29 Recovery Checkpoint

Fresh-session recovery uncovered a dangerous test behavior that is operational, not product-scope:

- `go test ./cmd -run TestAdapterBootstrap` was allowed to fork `cmd.test runtime start --foreground`
- repeated adapter test execution caused many runtime-start child processes
- that drove severe system load and memory pressure on the host

Implications:

- do not run broad `./cmd` adapter tests again until the runtime-start path is explicitly stubbed in tests
- keep verification low-fanout and single-process while recovering this branch

Safe verification pattern during recovery:

- `GOCACHE=/tmp/go-build-cache GOMAXPROCS=1 go test ./cmd -run '^TestAdapterBootstrapRegistersWatchAndDerivesTTYAgentName$' -count=1 -p=1`
- then `GOCACHE=/tmp/go-build-cache GOMAXPROCS=1 go test ./cmd -run '^TestAdapterBootstrap' -count=1 -p=1`

Recovery work added/confirmed:

- `cmd/adapter_test.go` now contains command-level adapter bootstrap tests that encode the real invariants:
  - no runtime PID file when `WAGGLE_ADAPTER_SKIP_RUNTIME_START=1`
  - watch registration uses shared runtime store
  - unread records are surfaced then marked surfaced
  - markdown output honors explicit overrides
  - adapter flag state must not leak across repeated command executions

Resolved review item:

- `WAGGLE_ADAPTER_SKIP_RUNTIME_START` is now constrained to Go test execution as well as the env var, so it no longer acts as a hidden production behavior toggle

Last known low-overhead verification:

- exact test `TestAdapterBootstrapRegistersWatchAndDerivesTTYAgentName` passed in under one second once the seam was patched
- full `^TestAdapterBootstrap` still exposed a command-global leak until `executeRootCommandForTest` was changed to restore adapter globals per invocation, not only at test cleanup

Latest verified recovery commands and results:

- `GOCACHE=/tmp/go-build-cache GOMAXPROCS=1 go test ./cmd -run '^TestAdapterBootstrapRegistersWatchAndDerivesTTYAgentName$' -count=1 -p=1`
  - `ok   github.com/seungpyoson/waggle/cmd  0.574s`
- `GOCACHE=/tmp/go-build-cache GOMAXPROCS=1 go test ./cmd -run '^TestAdapterBootstrap' -count=1 -p=1`
  - `ok   github.com/seungpyoson/waggle/cmd  0.466s`
- `GOCACHE=/tmp/go-build-cache GOMAXPROCS=1 go test ./cmd -run '^(TestAdapterBootstrap|TestRuntime)' -count=1 -p=1`
  - `ok   github.com/seungpyoson/waggle/cmd  0.251s`
- `GOCACHE=/tmp/go-build-cache GOMAXPROCS=1 go test ./internal/adapter ./internal/install -count=1 -p=1`
  - `ok   github.com/seungpyoson/waggle/internal/adapter  0.401s`
  - `ok   github.com/seungpyoson/waggle/internal/install  0.399s`
- `GOCACHE=/tmp/go-build-cache GOMAXPROCS=1 go build ./...`
  - exit `0`
- `GOCACHE=/tmp/go-build-cache GOMAXPROCS=1 go test ./... -count=1 -p=1`
  - `ok   github.com/seungpyoson/waggle`
  - `ok   github.com/seungpyoson/waggle/cmd`
  - `ok   github.com/seungpyoson/waggle/e2e`
  - `ok   github.com/seungpyoson/waggle/internal/adapter`
  - `ok   github.com/seungpyoson/waggle/internal/broker`
  - `ok   github.com/seungpyoson/waggle/internal/client`
  - `ok   github.com/seungpyoson/waggle/internal/config`
  - `ok   github.com/seungpyoson/waggle/internal/events`
  - `ok   github.com/seungpyoson/waggle/internal/install`
  - `ok   github.com/seungpyoson/waggle/internal/locks`
  - `ok   github.com/seungpyoson/waggle/internal/messages`
  - `ok   github.com/seungpyoson/waggle/internal/protocol`
  - `ok   github.com/seungpyoson/waggle/internal/runtime`
  - `ok   github.com/seungpyoson/waggle/internal/spawn`
  - `ok   github.com/seungpyoson/waggle/internal/tasks`

Additional recovery note:

- the full repo pass exposed one stale unit test in `internal/adapter/bootstrap_test.go` after the test-only seam helper signature changed
- that test was updated to match the production safety rule:
  - skip only when `WAGGLE_ADAPTER_SKIP_RUNTIME_START=1`
  - and only under a Go test binary
- residual cheapness risk remains in the shared runtime manager rather than this branch-specific adapter code:
  - current runtime watch management still uses periodic reconciliation and per-watch retry/listener loops
  - treat that as `#77` next-slice hardening work, not a reason to broaden this branch
- `GOCACHE=/tmp/go-build-cache GOMAXPROCS=1 go build ./...`
  - exit `0`
- `GOCACHE=/tmp/go-build-cache GOMAXPROCS=1 go test ./... -count=1 -p=1`
  - `ok   github.com/seungpyoson/waggle  10.881s`
  - `ok   github.com/seungpyoson/waggle/cmd  0.262s`
  - `ok   github.com/seungpyoson/waggle/e2e  0.766s`
  - `ok   github.com/seungpyoson/waggle/internal/adapter  0.203s`
  - `ok   github.com/seungpyoson/waggle/internal/broker  26.940s`
  - `ok   github.com/seungpyoson/waggle/internal/client  0.195s`
  - `ok   github.com/seungpyoson/waggle/internal/config  0.644s`
  - `ok   github.com/seungpyoson/waggle/internal/events  0.252s`
  - `ok   github.com/seungpyoson/waggle/internal/install  0.202s`
  - `ok   github.com/seungpyoson/waggle/internal/locks  0.157s`
  - `ok   github.com/seungpyoson/waggle/internal/messages  6.233s`
  - `ok   github.com/seungpyoson/waggle/internal/protocol  0.186s`
  - `ok   github.com/seungpyoson/waggle/internal/runtime  2.849s`
  - `ok   github.com/seungpyoson/waggle/internal/spawn  11.509s`
  - `ok   github.com/seungpyoson/waggle/internal/tasks  18.959s`

Resume rule:

- do not reintroduce package-level or parallel Go test runs while recovering this harness
- fix the class of problem first:
  - no daemon spawn from adapter tests
  - no command-global leakage between executions

Cheapness rule for the branch:

- do not ship adapter work that introduces per-agent daemon fanout, polling storms, or other multiplicative background cost
- if review uncovers deeper shared-runtime scaling work, treat `#77` as the immediate next slice after this branch rather than deferring it until later roadmap phases

## Goal

Execute the first adapter slice after machine-runtime:

- preserve one invisible coordination contract underneath all tools
- keep Waggle low-visibility
- implement a serious `Codex` adapter first
- include `Gemini` only if it falls out cleanly later
- defer `Augment Code` Mac app integration

## Agreed Design Decisions

These were already discussed and approved:

1. Optimize for one system integrated well, not broad shallow coverage.
2. Preserve one shared runtime-backed adapter contract:
   - resolve `project_id`
   - resolve stable `agent_name`
   - ensure runtime is available
   - register watch intent
   - surface unread local runtime records at a safe boundary
3. Do not create a new transport path or per-tool unread model.
4. Do not make `waggle <tool>` wrappers the long-term UX.
5. `Codex` is first even though `Gemini` appears to have a cleaner native hook surface.
6. `Augment` is deferred because usage is mainly through the Mac app and no clean low-visibility integration boundary has been identified yet.

## Context Gathered

### Existing Claude adapter is the reference shape

- `integrations/claude-code/hook.sh`
- `internal/install/claude_code.go`
- `cmd/runtime_start.go`
- `cmd/runtime_watch.go`
- `cmd/runtime_pull.go`
- `internal/runtime/registry.go`

The Claude adapter already expresses the right contract:

- `waggle runtime start`
- `waggle runtime watch`
- `waggle runtime pull`

### Codex boundary

Observed locally:

- `codex` exists at `/Users/spson/.npm-global/bin/codex`
- no obvious native hook command in `codex --help`
- strong config/context surface in `~/.codex/`
- global `AGENTS.md` and installed skills are real surfaces

Conclusion:

- Codex should use a Waggle-managed AGENTS block + skill/context, not a permanent visible wrapper UX

### Gemini boundary

Observed locally:

- `gemini` exists at `/Users/spson/.npm-global/bin/gemini`
- `gemini hooks`
- `gemini skills`
- config in `~/.gemini/`

Conclusion:

- Gemini probably has a cleaner native integration surface than Codex
- still deferred in this slice unless it falls out trivially later

## Files Added / Changed So Far

### Design / planning docs

- `docs/superpowers/specs/2026-03-28-waggle-tool-adapters-design.md`
- `docs/superpowers/plans/2026-03-28-waggle-tool-adapters.md`

### Install-side Codex integration

- `cmd/install.go`
- `internal/install/managed_block.go`
- `internal/install/codex.go`
- `internal/install/codex_test.go`
- `integrations/codex/AGENTS-block.md`
- `integrations/codex/skills/waggle-runtime/SKILL.md`
- `internal/install/codex/AGENTS-block.md`
- `internal/install/codex/skills/waggle-runtime/SKILL.md`

What this install-side work is intended to do:

- add `waggle install codex`
- install a Codex skill at `~/.codex/skills/waggle-runtime/SKILL.md`
- upsert a managed Waggle block into `~/.codex/AGENTS.md`
- uninstall cleanly
- keep Codex on the runtime-backed contract without creating new transport logic

### Adapter bootstrap command work

- `cmd/adapter.go`
- `cmd/adapter_test.go`
- `internal/adapter/bootstrap.go`
- `internal/adapter/bootstrap_test.go`
- `cmd/root.go` was modified to treat `adapter` as broker-independent

Intent of this work:

- add `waggle adapter bootstrap <tool>`
- make the shared adapter contract explicit in code
- resolve project id and agent name
- best-effort start the runtime
- register watch
- pull unread records
- output bootstrap state for tool integrations

## Current Branch State

At the last verified status check:

- `cmd/root.go` exists and is no longer zeroed
- `cmd/adapter.go` exists and is no longer zeroed
- `internal/adapter/bootstrap.go` exists and is no longer missing
- install-side Codex files are present

`git status --short` showed these pending changes:

- `M cmd/install.go`
- `M cmd/root.go`
- `?? cmd/adapter.go`
- `?? cmd/adapter_test.go`
- `?? docs/superpowers/plans/2026-03-28-waggle-tool-adapters.md`
- `?? docs/superpowers/specs/2026-03-28-waggle-tool-adapters-design.md`
- `?? integrations/codex/`
- `?? internal/adapter/`
- `?? internal/install/codex.go`
- `?? internal/install/codex/`
- `?? internal/install/codex_test.go`
- `?? internal/install/managed_block.go`

## Last Known Verification

These passed earlier:

- `go test ./internal/adapter ./internal/install -count=1`

The broader command test run surfaced two important problems:

1. `TestAdapterBootstrapDerivesTTYScopedAgentName`
   - failure: previous adapter flag state leaked across tests
   - observed result: `"agent-a"` instead of expected `"codex-ttys009"`
   - fix needed: reset adapter bootstrap globals in `executeRootCommandForTest`

2. `go test ./cmd ...`
   - timed out because adapter bootstrap attempted to start the real runtime daemon under `go test`
   - that polluted later runtime-store cleanup and caused a long hang
   - fix needed: add a test seam so adapter tests can skip real runtime startup

## Exact Fixes In Progress When Interrupted

These are the next edits that should be made before re-running tests:

### 1. Add a runtime-start test seam

In `internal/adapter/bootstrap.go`, inside `ensureRuntimeStarted`:

- short-circuit when `WAGGLE_ADAPTER_SKIP_RUNTIME_START=1`

This is the simplest way to keep adapter command tests from launching the real runtime daemon.

### 2. Set that env var in adapter command tests

In `cmd/adapter_test.go`, add:

- `t.Setenv("WAGGLE_ADAPTER_SKIP_RUNTIME_START", "1")`

to all three adapter bootstrap tests.

### 3. Reset adapter command globals between tests

In `cmd/runtime_test.go`, inside `executeRootCommandForTest`, snapshot and restore:

- `adapterBootstrapTool`
- `adapterBootstrapAgent`
- `adapterBootstrapProjectID`
- `adapterBootstrapSource`
- `adapterBootstrapFormat`

Without this, one adapter test leaks state into the next.

## Remaining README Work

README has not been updated yet in the branch.

Needed changes:

1. Update the adapter wording so it no longer says only Claude is shipped if Codex is now shipped in-branch.
2. Add explicit install examples:
   - `waggle install claude-code`
   - `waggle install codex`
3. Keep scope honest:
   - Claude shipped
   - Codex shipped if branch completes
   - Gemini and Augment still future work in this slice

## Recommended Resume Order

1. Make the three small test-seam fixes listed above.
2. Run:
   - `gofmt -w ...` on touched Go files
   - `go test ./internal/adapter ./internal/install ./cmd -run 'TestAdapterBootstrap|TestRuntime' -count=1`
3. If that passes, update `README.md`.
4. Then run:
   - `go test ./internal/adapter ./internal/install ./cmd -count=1`
   - `go test ./... -count=1`
   - `go build ./...`
5. Only after that, decide whether to keep Gemini deferred explicitly in this branch or attempt any Gemini work.

## Important Constraint

Do **not** broaden scope mid-recovery.

This branch should remain:

- shared adapter contract
- Codex install + bootstrap path
- docs honesty

Not:

- Gemini hook work
- Augment work
- runtime observability
- dismiss/read UX
- versioning/release ergonomics

## Suggested Fresh-Session Prompt

Use the prompt below in a fresh Codex session:

> Resume work on the `feat/tool-adapters` branch in `/Users/spson/Projects/Claude/waggle/.worktrees/feat-tool-adapters`.
>
> Read `docs/superpowers/plans/2026-03-28-tool-adapters-handoff.md` first and treat it as authoritative handoff context.
>
> Goal:
> - recover and complete the Codex-first adapter slice for issue `#71`
> - keep one invisible runtime-backed coordination contract
> - keep Gemini deferred unless it falls out trivially
> - keep Augment deferred
>
> Immediate tasks:
> 1. add the adapter runtime-start test seam
> 2. fix adapter test env setup
> 3. reset adapter command globals between tests
> 4. rerun targeted tests
> 5. update README shipped-scope/install docs
>
> Constraints:
> - do not broaden scope
> - do not create new transport paths
> - do not make visible `waggle <tool>` wrappers the long-term UX
> - keep the branch systematic, not patchy
