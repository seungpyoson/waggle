# M1 entrypoint feasibility findings

Runtime implementation has not started. These checks test prerequisites for the shared broker design; they do not establish either provider's conformance. Commands, outputs and limits are recorded in `m1-entrypoint-evidence.json`.

The required invariant remains: a message targets one verified session incarnation, and canonical delivery state reflects observed native facts. The broker owns identity, scheduling and receipts; provider drivers own only native protocol mechanics. Provider model names and tested version numbers must not become routing rules. Configuration owns operational paths, binaries and bounds.

## What the current checks establish

The current Codex session exposes both `CODEX_THREAD_ID` and `CODEX_SESSION_ID` to its ordinary shell tool. Both contain valid UUIDs and happen to be equal here; their values were not recorded. This is new evidence against assuming a custom launcher is necessary merely to expose an identifier. It does not prove SessionStart enrollment, endpoint membership, credential delivery or isolation across multiple sessions. Equality in this session does not make the two identifiers interchangeable.

The installed Codex CLI's default daemon endpoint is absent: `codex app-server daemon version` exits 1 with ENOENT at its default control socket. This does not establish where the current interactive host runs or whether it exposes another endpoint. No endpoint was guessed and no replacement server was started. The plan's explicit remote-TUI topology remains an unproven supported-workflow proposal, not a demonstrated requirement for all Codex sessions.

Direct runtime checks encountered these execution limits:

- An AF_UNIX socket cannot bind a fresh path beneath the allowed temporary root: EPERM. A second instrumented attempt identified `bind` as the failing phase. Both probes cleaned their temporary resources; testing stopped after those two failures.
- `codex --no-alt-screen` reached its terminal warning. After continuing, startup was blocked by the network allowlist for `ab.chatgpt.com`. No model prompt was submitted and no ready session was established.
- Interactive `claude`, with the previously documented optional-telemetry opt-out, exited 1 with `EEXIST: file already exists, mkdir '/tmp/claude-501'`. The cause inside that denied path was not inspected. No model prompt was submitted.

On the user's subsequent request for an unblock action, one scoped execution-approval probe also failed at Unix socket `bind` with EPERM. The command ran, so another per-command approval is not an established remedy. The execution environment must supply the required local IPC capability.

Direct configuration inspection subsequently located the authority in root-owned `/etc/codex/requirements.toml`: `github_workspace` is the selected managed profile, its domain list omits the required Go service, and it has no named Unix-socket grants. Its `/tmp` denial remains in force. `codex doctor --summary --ascii` was also blocked at `chatgpt.com`; a complete doctor result is not confirmed.

The proposed permission installer, generated policy copies and test-profile patch were withdrawn and removed after reassessing their scope. They added repository machinery around an administrator-owned environment prerequisite and did not establish native conformance. The earlier apply/resume commands are superseded and must not be used. The user's wrapped inline command failed to parse before applying the patch. Subsequent inspection verified that the live policy is unchanged at SHA256 `6c4d8248bced5c4f95fa660857bf15ebe5cc868dfc4fb57256c0af79f05a9cfd`; no proposed profile was installed. Further environment provisioning belongs to the normal administrator workflow, with the existing deny rules retained.

The earlier baseline build also remains blocked by missing Go dependencies and denied access to the standard module proxy, as recorded in `m1-preparation.md`. This pass did not repeat the blocked download or claim a successful build.

## Production path rechecked

`cmd/send.go` derives the sender from a caller-selected name and submits broker RPC. `internal/broker/router.go` persists the message before resolving the recipient, then selects a named connection or its `-push` listener and marks transport progress as pushed. The Codex installer writes a skill, an instruction block and a shell hook. The Claude installer registers SessionStart and PreToolUse hooks; its push hook consumes signal files and emits context. These paths do not bind the sender and recipient to verified native sessions or establish native consumption.

Adding a provider-name condition to that route cannot repair the missing authority. The canonical broker and mechanics-only drivers remain the intended boundary, with removal of the superseded delivery paths in the implementation scope. No runtime patch, alternative transport, launcher, credential registry or permission change was introduced during these checks.

## Exact remaining prerequisite

Real conformance requires an execution environment that can bind local Unix sockets, start both interactive CLIs successfully, and supply the pinned Go dependencies. The current environment cannot complete those checks. That is an environment limitation, not evidence that either provider supports or lacks the proposed messaging contract.

Once those prerequisites are available, the next evidence must come from the actual entrypoints and one broker-controlled enrollment/send/receipt path. Repeated design review cannot supply that evidence. No additional review was requested during this pass; the approved revision 4 document is unchanged.
