# Waggle v4 — Push Messaging Orchestrator Prompt

You are implementing push messaging for waggle. Four issues, sequential execution, one PR per issue. Claude Code will review each PR before the human merges.

## Project Context

- **Repo:** `~/Projects/Claude/waggle` (github.com/seungpyoson/waggle)
- **Language:** Go 1.26 + bash + JavaScript (hooks)
- **v1 + v2 + v3 are complete and on main.** All tests pass. Do not modify existing behavior — only extend.
- **Task briefs:** `docs/briefs/task-56-fix-custom-events.md`, `docs/briefs/task-57-auto-start-broker.md`, `docs/briefs/task-58-agent-discovery.md`, `docs/briefs/task-59-push-messages.md`
- **GitHub Issues:** #56, #57, #58, #59

## What's Already Done

- **v1:** Broker (Unix socket, SQLite, goroutine per connection), tasks (CRUD, claim, complete, fail, cancel, heartbeat, dependencies, lease management), events (in-memory pub/sub), locks (advisory), sessions (connect/disconnect with cleanup), CLI (all commands via Cobra), config (path resolution, defaults, socket hashing)
- **v2:** Direct messaging (send, inbox), delivery lifecycle (queued→pushed→seen→acked), presence (in-memory, online/offline), priority + TTL on messages, --await-ack blocking sends, spawn (terminal tabs with safe command builder, register-before-spawn)
- **v3:** Task lifecycle (TTL, stale detection, queue health), Claude Code integration (SessionStart hook, /waggle skills, installer)
- **E2E tests:** Full round-trip coverage for tasks, messaging, ack lifecycle

**IMPORTANT: Read the actual code before implementing.** When sources disagree:
1. **Code on main** — highest authority
2. **Task brief** — implementation guidance
3. This prompt — context, may be outdated on code details

Key code to read first:
- `internal/broker/router.go` — handlePublish (line 118, the bug), publishTaskEvent (line 488, the correct pattern), handleSend (line 509, push delivery), handlePresence (line 648, agent list)
- `internal/protocol/message.go` — Event struct, Response struct
- `internal/events/hub.go` — Publish/Subscribe API
- `internal/client/client.go` — ReadStream (line 82), Receive (line 65)
- `internal/broker/broker_test.go` — startTestBroker pattern
- `~/.claude/hooks/waggle-connect.sh` — the SessionStart hook to modify
- `cmd/events.go` — subscribe/publish CLI commands

## Four Issues — Sequential, One Branch Per Issue

```
Wave 0 (can be parallel but sequential is fine):
  #56 [S]: fix custom event payload wrapping (bug fix, Go only)
  #57 [S]: auto-start broker from SessionStart hook (shell only)
      ↓ both merged to main
Wave 1:
  #58 [S]: agent discovery — waggle sessions + hook (Go + shell)
      ↓ merged to main
Wave 2:
  #59 [M]: push messages — waggle listen + PreToolUse hook (Go + shell + JS)
```

**Each issue produces one PR. No PR is merged until Claude Code reviews and approves it.** After the human merges a PR, pull main and run the cross-issue regression test from the brief before starting the next issue.

## Git Workflow

```
For each issue:
  1. git checkout main && git pull origin main
  2. git checkout -b <branch-name>      # branch name is in the brief
  3. Follow the brief's Phase A→F
  4. Create PR:
     gh pr create --title "#N: description" --body "..."
  5. STOP. Do not merge. Wait for review.

After human merges:
  6. git checkout main && git pull origin main
  7. Run cross-issue regression test from the brief
  8. Only after regression passes: start next issue
```

**Branch names** (from the briefs):
- `fix/56-custom-event-wrapper`
- `feat/57-auto-start-broker`
- `feat/58-agent-discovery`
- `feat/59-push-messages`

## Development Order Within Each Issue

Every issue follows this exact sequence. No step is skippable. Each brief specifies the phases in detail.

```
Phase A: Invariants (TDD)
  1. Read the brief's invariant table
  2. Write tests that verify each invariant — they must FAIL
  3. Run the tests — confirm they fail
  4. Log the failures

Phase B: Implementation
  5. Implement the minimum code to make tests pass
  6. Run tests after each change — not all at the end
  7. When all invariant tests pass, run full suite:
     go test ./... -race -count=1 -timeout=120s
  8. Run: go vet ./...

Phase C: Smoke Test
  9. Run the SMOKE TEST from the brief (live, end-to-end)
  10. If smoke test fails: fix, re-run all tests, re-run smoke
  11. Smoke test MUST pass before creating PR

Phase D: Cross-Issue Regression
  12. Run the CROSS-ISSUE REGRESSION test from the brief
  13. All previous issues' smoke tests must still pass

Phase E: PR (do NOT merge)
  14. Push branch, create PR
  15. PR body format:
      ```
      ## Summary
      <bullets>

      ## Invariants
      All N invariants verified (list them)

      ## Tests
      X pass, 0 fail, race clean, vet clean

      ## Smoke Test
      <paste output>

      ## Cross-Issue Regression
      <paste output — must show previous issues still work>

      Closes #N
      ```
  16. STOP. Do not merge. Human + Claude Code review.
```

## Critical Rules — Non-Negotiable

### Process
1. **TDD mandatory.** Write failing tests FIRST. No exceptions.
2. **Invariant-driven.** Every invariant in the brief must have a passing test.
3. **Smoke test is the real gate.** If it fails, the issue is not done.
4. **Cross-issue regression is mandatory.** Each PR proves previous work still holds.
5. **One issue = one branch = one PR.** Never combine issues.
6. **Do NOT merge PRs.** Create PR, stop. Claude Code reviews, human merges.
7. **Do NOT rewrite the brief.** If scope needs to change, leave a comment on the GitHub issue.

### Code Quality
8. **All configurable values in `config.Defaults`.** No magic numbers.
9. **Errors are loud.** Every error is returned or logged. No `_ = err`.
10. **Reuse existing patterns.** Read the codebase before writing new code. Match existing style.
11. **Hook scripts must be fast.** SessionStart: <3s total. PreToolUse: <100ms.
12. **CLI commands must work with `--help` from any directory.** Every new CLI command must be tested with `cd /tmp && waggle <cmd> --help` — must print help and exit 0. No broker or project context required for help text. (See #65.)

### Issue-Specific Notes

**#56 (custom events):**
- One function change in `handlePublish`. Copy the `publishTaskEvent` pattern.
- Validate that `req.Message` is valid JSON before wrapping.
- Regression tests for task.events and presence.events are critical.

**#57 (auto-start broker):**
- Shell-only change. No Go code.
- Race prevention: use `mkdir` as an atomic lock.
- Hook must stay under 3s total even with auto-start.
- Test with multiple parallel hook invocations.

**#58 (agent discovery):**
- `handlePresence` already returns agent list — this is mostly a CLI + hook wiring task.
- The `waggle sessions` command connects with an ephemeral name `_discovery-<pid>`.
- Filter `_discovery-*` names from hook display.

**#59 (push messages):**
- **Read the design section in the brief carefully.** The listener connects as `<name>-push`. `handleSend` is modified to also push to `<name>-push`.
- This is the largest issue. It touches Go (broker + client + CLI), shell (hook), and JavaScript (PreToolUse hook).
- The PreToolUse hook uses clear-after-read: reads the listen file, injects messages, truncates the file.
- Test the full end-to-end flow: send from A → listener captures → hook injects → AI sees it.

## What NOT to Do

- Do NOT merge PRs. Create and stop.
- Do NOT rewrite briefs.
- Do NOT modify existing v1/v2/v3 behavior unless required by the current issue.
- Do NOT add features beyond what's in the brief.
- Do NOT skip the smoke test or cross-issue regression.
- Do NOT combine multiple issues into one PR.
