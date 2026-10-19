# Native messaging implementation plan

Status: revision 2, proposed after the user's structural-fix requirements; awaiting fresh M0 approvals. The earlier Codex approval covers revision 1 only. No runtime implementation is approved by this document alone.

The user authorized implementation of reliable Codex↔Claude and Codex↔Codex communication, with approval from Claude Fable 5.1 at xhigh and Codex GPT-6 Astra at xhigh at key milestones. The original assessment was completed against `5fac0530a789f54b2a87fb10d9d21a14b2dded91`. This plan implements that objective; a successful model invocation or cooperative inbox pull is not completion.

Related work: GitHub issues #132 (liveness), #136 (ordered replay), #137 (bounded catch-up), #138 (receipt handoff), #139 (direct target validity), #140 (real-session evidence), #141 (diagnostics), #143 (sender identity); roadmap #142. Existing issues are context, not evidence of implemented behavior.

## Acceptance contract

The release must demonstrate, on this user's local subscription-authenticated runtimes:

1. Claude→Codex→Claude, Codex→Claude→Codex, and Codex A→Codex B→Codex A, with distinct concrete conversations and replies correlated to the original message.
2. Idle recipients start processing without another human prompt, shell command, or manual inbox poll. Busy recipients safely receive later or produce an explicit held/refused result.
3. Participating agents retain normal interactive use. Codex's normal terminal UI may attach to a shared/managed App Server. Existing sessions are attached only through supported, explicitly registered endpoints. Starting a different worker must never be reported as reaching the original session.
4. Names resolve unambiguously to an exact session incarnation. Two Codex sessions in the same directory cannot share identity. Known history, a live Waggle listener, a PID, or a transcript is insufficient to report runtime readiness.
5. Unknown/dead direct targets fail explicitly. Persisting a message is reported as stored, never runtime-accepted or replied. Offline durable enqueue is an explicitly different operation; it is not silently substituted for direct delivery.
6. Restart before dispatch preserves pending work. Ambiguous acceptance is reconciled from provider evidence or remains explicitly uncertain. No blind retry can duplicate a possibly accepted write-capable request. Stale workers cannot commit receipts for a newer attempt.
7. No gap in ordered replay/live handoff, bounded catch-up and queues, message deadlines, bounded conversation turns, explicit backpressure, cancellation/stop, and no uncontrolled reply loop.
8. Peer messages confer no permission. Runtime policies and inbound controls remain effective, sender identity is derived from a scoped registration, and tokens/credentials are never printed or placed in prompts. No paid API fallback or account rotation is introduced.
9. Build, applicable tests, degraded-state CLI help, native conformance, real-session matrix, and both final reviews pass. Missing environment evidence is not a pass. No merge or deployment is implied by reviewer approval.

The broad execution platform (objectives, work graphs beyond existing tasks, evidence marketplaces, fleet reconstruction, ACP/A2A federation, topology selection) remains future work. Stable identities, durable attempts, and explicit driver capabilities are founding boundaries now.

## Invariant, responsible component, and production evidence

Invariant: a durable message is addressed to one exact runtime incarnation and remains in an explicit state supported by observed native acceptance or a correlated reply. Storage, notification, transport progress, and model response are different facts. Only the per-project broker may transition canonical message/session/attempt state.

The current production path is CLI `send` → broker `handleSend` → name/name-push socket → runtime `handleDelivery` → a second SQLite record → `notifyRecord` → signal file → shell/PreToolUse hook. Bootstrap independently opens the runtime store and marks records surfaced/dismissed before its caller prints them. The broker never observes native input acceptance.

Direct evidence at the base head: `internal/broker/router.go:668` stores before target resolution; `internal/runtime/manager.go:382` writes the second record; `manager.go:483` writes the signal and marks notification; `internal/adapter/bootstrap.go:121` dismisses before returning; `cmd/adapter.go:53` calls Bootstrap before rendering; `cmd/runtime_pull.go:37` marks surfaced before printing. A local fixture executed the shipped Claude push hook and observed exit 0, a deleted signal, and a top-level `additionalContext` field instead of the documented nested context envelope. This is a verified local contract failure, not live-provider E2E evidence.

The current design cannot enforce the invariant because none of its canonical-state writers owns the native input boundary, and they independently interpret receipt. Fixing names, adding another acknowledgement file, or retrying hooks would retain that split. The structural change removes the intermediate delivery store and hook consumption from the supported messaging path, consolidates scheduling and receipts in the broker, and obtains native facts through driver modules.

## Architecture and authority

Keep Go, SQLite, project identity, the existing task machinery, and the normal CLI. The per-project broker owns canonical message, participating-session, attempt, and receipt state and invokes the native driver modules itself. The machine runtime and its database are removed from message delivery, not retained as a delivery cache or second registry. Agent shell commands and enrollment hooks use broker RPC and never open canonical databases. Provider runtimes remain independently owned processes; a broker restart does not terminate them.

Provider mechanics belong under an internal driver package; they must not be embedded in broker routing. The broker schedules transport attempts and enforces generic state transitions; agents still own the meaning of messages and work. All paths, limits, timeouts, binaries, and provider defaults resolve through `internal/config/`.

A minimal driver boundary supports probe/attach, readiness observations, submit, reconcile, and close. Results are typed: definitely not submitted, accepted with provider reference, held, refused, unsupported, disconnected, or acceptance unknown. Context cancellation after submission does not imply the provider rejected the message. Drivers contain protocol translation and connection state only, with no durable queues, alternate routes, or independent retry policies. The interface is internal until both implementations prove it; there is no public plugin ABI freeze.

All startup hooks perform bounded registration or report unavailability and continue. `--help` must not instantiate a client, probe a provider, start a broker, require Git, or touch state. Normal commands fail with contextual errors. A receiver that lacks a documented capability is visibly unsupported rather than silently routed through terminal text or hooks.

## Session identity and trust

Canonical identity records include a globally unique session incarnation, project, provider/runtime type, provider conversation ID, user-facing name, broker ownership generation, opaque endpoint binding, capabilities, readiness, and activity times. This replaces name/PPID/TTY watches as the routing source; it is not an additional registry beside them. PID and process-start observations are supplemental diagnostics, not routing keys.

Registration binds the concrete runtime endpoint and returns a per-incarnation scoped credential over a private local channel. Sender identity comes from that authenticated registration; `--name` selects a label and cannot impersonate a registered sender. Agent-facing connections use the broker's private local socket with explicit role and scope checks. Provider authentication stays inside the provider installation. Provider endpoint tokens stay in the relevant driver boundary and are never persisted in the public ledger.

Exact mechanics for securely passing session binding into agent-facing calls must be confirmed in M1: Codex tool or launched-session configuration and Claude's SessionStart/native endpoint registration are candidates. Arbitrary session metadata or a user-supplied path is not trusted merely because it has the right name. Probes must reject unsupported endpoints with contextual diagnostics and preserve the host's policies.

After a broker restart, persisted sessions begin disconnected/unverified. Only native reconnection to the same explicitly bound endpoint and provider conversation plus observed readiness makes them ready. Broker ownership generations fence stale callbacks. Known provider conversation IDs remain available for reconciliation. Closing a terminal view is distinguished from terminating the provider runtime; no claim is made to restore private in-memory process state after a machine crash.

## Message and attempt lifecycle

Extend message storage with canonical sender/recipient session references, conversation ID, reply-to message ID, deadline, deduplication key, and provenance. Existing numeric IDs can remain internal database keys; externally exchanged message/session identifiers must be globally unique. Message text stays untrusted text, with bounded size and explicit peer provenance.

Each dispatch has an immutable attempt ID, leased owner/generation, claimed time, submission intent, provider reference when known, and an outcome. Persist the intent before calling the provider. The broker applies receipts only to the leased attempt and original target incarnation. A driver generation change fences stale writes. A late result can be recorded as evidence without silently authorizing a new dispatch.

Separate transport acceptance from agent processing. The ledger records stored/dispatching/runtime-accepted and an independently correlated reply. Held/refused/expired/disconnected/uncertain are explicit outcomes, not success. A user-visible result contains both the message state and relevant attempt outcome rather than compressing every stage into `acked`.

There are no automatic resubmissions in the initial contract. A definite non-acceptance is a visible failure; a lost submission response is uncertain. Restart can dispatch stored work only when no submission intent was committed. Intent without conclusive outcome must be reconciled or remain uncertain, and blocks later automatic submissions to that recipient to preserve ordering. Reconnection restores a transport to the same endpoint; it never replays an uncertain request or selects another endpoint. A client message ID is not proof of exactly-once semantics. A receiving agent can explicitly acknowledge or reply using its bound Waggle tool; plain provider completion without a reply is not fabricated into one.

Sending an agent message never approves the recipient's tools or changes its model/sandbox/approval settings. The adapter must mark peer provenance and use a host policy independent of message content; it cannot pass a peer's requested permissions into provider control fields. Conversation deadlines and turn/hop budgets are inherited and decremented by canonical operations, not merely written into a prompt.

## Ordered delivery and persistence migration

Use one broker-owned durable ordered queue. A message commit assigns a per-recipient sequence and wakes the broker scheduler; that scheduler selects eligible rows from canonical SQLite and invokes the recipient's native driver. Startup drains the same queue through the same scheduler. An in-memory wakeup is only a scheduling hint, never a delivery record. There is no separate catch-up handler, live push dispatcher, delivery subscription, or second transport cursor. The old replay/live race is eliminated by removing that handoff from the production path. Read-only event subscriptions may display state but cannot dispatch or acknowledge messages.

Queue scans are bounded, ordered, and driven only by canonical states. At most one submission outcome may be unresolved per recipient. Message identity deduplication and capacity validation live in the broker. Capacity pressure returns explicit backpressure; no message is silently dropped and no alternate transport is started. Fault tests must prove that interruption between commit, wakeup, selection, intent, native submission, and receipt cannot strand stored work or repeat uncertain input.

Migration is one explicit offline cutover, with a preserved SQLite snapshot and no mixed-version operating period. Existing user data directories are present, so preserving historical records is required. The cutover requires verified cessation of the old Waggle writers; otherwise it fails without mutating state. A single target-database transaction preserves existing task data and imports the old broker/runtime records as unconfirmed history, recording source identity and reporting mismatches instead of guessing receipts or routes. Historical records are inspectable but are never automatically submitted to newly enrolled sessions. Source snapshots remain intact after conversion. New enrollment is required because old name/PPID/TTY records cannot establish native runtime identity.

The native schema and RPC version are deliberately incompatible with the old messaging writer. No compatibility adapter, dual receive mode, or legacy decoder can route new messages. A version mismatch produces an explicit unsupported-version failure before dispatch. Before cutover can ship, tests using the actual previous binary must prove it cannot start against or mutate the new messaging store, including legacy replay/ack paths. Do not rely on a marker that old binaries ignore. If that fence cannot be demonstrated, cutover remains blocked rather than adding a silent parallel database or compatibility route.

Required removal within messaging scope: broker name-push fallback and listener-derived presence; runtime watch/record/signal delivery and notification retry loops; PPID/TTY routing files; signal-consuming shell/PreToolUse hooks; bootstrap and runtime-pull receipt mutations; separate catch-up/listen delivery; and CLI access to the runtime delivery database. Keep a bounded enrollment hook only where the native host requires it. Installer removal is limited to known Waggle-owned blocks/files and preserves unrelated user settings. Existing provider integrations without a native driver must report unsupported receive capability; they do not retain a hidden legacy messaging path. Cleanup unrelated to these superseded paths remains outside this implementation.

## Provider-specific implementation

Codex: initial verified installation is CLI 0.153.4. Generate schemas from that binary. Use App Server JSON-RPC over the documented local WebSocket endpoint with one request-ID dispatcher, bounded framing, server-initiated request handling, notification routing, timeouts, and explicit connection lifecycle. Use a maintained WebSocket library; an unavailable dependency is a prerequisite failure, not a reason to add another framing implementation or transport. Attach to the actual owning App Server and thread; never reopen the same transcript in a second independent server as a substitute for live delivery. `thread/read`/loaded state inform readiness; `turn/start` handles idle delivery and `turn/steer` uses the exact active `expectedTurnId`. An idle/busy race is reported as non-acceptance or uncertain based on the native response; no automatic switch/resubmit hides it. The normal TUI attaches through that same endpoint. Keep a subscription while managing a thread; do not unload another client's live session. No provider approval is automatically granted. WebSocket transport is experimental, so the pinned installation, frame/error behavior, and disconnects require native conformance evidence.

Claude: initial verified installation is CLI 2.1.261. Capture the exact native messaging endpoint exported to that session, with registration performed at a bounded lifecycle hook. Probe native input and receipt semantics on an owned test session before encoding a transport contract. Native inbox availability does not by itself document every wire field or receipt guarantee. Keep accept/hold/refuse behavior truthful. The receipt contract explicitly records native acceptance when provable and a separate in-session Waggle acknowledgement/reply; where native acceptance cannot be proved, state remains uncertain until that explicit receipt. Do not impersonate another Claude process or forge a permission class. If the native interface cannot meet the required invariant, stop and report the exact limitation. Channels, terminal injection, print-mode replacement workers, or another provider are not runtime fallback paths.

Outbound tools: the CLI is the initial authenticated RPC-backed send/reply entrypoint for both hosts. Enrollment must supply its binding through a supported host mechanism; if that prerequisite cannot be proven, stop before claiming the host is supported. Do not add MCP or shell-identity inference to mask a binding failure. Sender discovery is automatic after enrollment and returns a precise actionable error if enrollment is absent. Receiving messages never depends on calling the CLI or running `waggle inbox`.

## Milestones and reviewer gate

M0 — approve this design and the concrete acceptance contract before runtime implementation. Reviewers must identify blocking gaps and return APPROVE or REQUEST_CHANGES for the exact document digest. Approval authorizes implementation/conformance work, not a claim that provider behavior is already proven.

M1 — native transport and enrollment proof: both provider drivers invoked through the canonical broker path, exact identities, normal terminal use, and actual bounded round trips with idle/busy coverage. No throwaway successful path may substitute for that entrypoint. Include proof of session credential propagation and native receipt contracts. Deterministic transport fakes cover framing, unsolicited events, competing request IDs, held/refused input, cancellation, and lost responses. Native tests cover the real behavior fakes cannot establish. Both reviewers approve the exact code diff and evidence before M2.

M2 — complete the durable integration and cutover: canonical attempts/receipts, restart reconciliation, generation fencing, the single ordered queue, deduplication, target validity, reply correlation, bounded loops, migration and all required removals. Run fault-controlled tests before/after queue commit, intent commit, provider submit, and receipt persistence. Verify that the old binary cannot mutate the new store and all superseded production paths are absent. Both reviewers approve the exact code diff and evidence before M3.

M3 — release verification: all three real conversation directions, idle/busy/dead/ambiguous/duplicate/restart scenarios, build, full Go tests, CLI help from a non-repository directory with broker/Git/network absent, installation diagnostics, and no terminal corruption. Save exact versions, commands, IDs, receipt/reply evidence, and remaining limits. Both reviewers must approve the same final head/diff and evidence. Only then is the user goal eligible for completion. Merge/deployment is a separate authority boundary.

Reviewer identity must be recorded from dispatch configuration and returned runtime metadata where available; asking a model to state its own identity is not verification. Required models are exactly `claude-fable-5-1` with `xhigh` and `gpt-6-astra` with `xhigh`. No aliases, silent fallback, or self-approval substitutes. Record request digest, source head/diff digest, response, verdict, and unresolved findings. Code/design changes that invalidate an approval require a fresh review. Refusal, timeout, missing reviewer, or network failure keeps the gate closed.

## Current execution constraints

Git metadata writes failed for isolated branch/worktree creation, including the escalated attempt. The original worktree has a pre-existing deletion of GEMINI.md; preserve it. Proposed documentation remains an uncommitted, reviewable change until a branch can be created. Do not modify permissions or relocate Git metadata to bypass the restriction.

Required Go module archives are missing locally; previous dependency fetches were denied by network policy for proxy.golang.org. Some existing tests hardcode /tmp, which this environment denies. Route new test resources through configured/allowed temporary roots; do not weaken production limits or count tests skipped by the environment as passed. If required reviewer/runtime access is also blocked, finish the concrete review packet and unaffected preparation, report the blocker, and keep the implementation goal active.

The user explicitly approved sending the plan and repository AGENTS.md to Anthropic for the required Fable 5.1 xhigh review. That disclosure approval is no longer missing. The authorized CLI dispatch was then stopped by the network allowlist for api.anthropic.com, before a review response was produced. Resolve the network prerequisite rather than changing destination, model, or review path.

## Sources checked

- OpenAI App Server: https://developers.openai.com/codex/app-server
- GPT-6 Astra identifier/effort: https://developers.openai.com/api/docs/models/gpt-6-astra
- Claude native messaging: https://code.claude.com/docs/en/cross-session-messaging
- Claude Fable 5.1 identifier: https://platform.claude.com/docs/en/models/fable-5-1/overview
- Claude effort: https://platform.claude.com/docs/en/build-with-claude/effort
- Current local Codex schema and CLI help; current local Claude CLI help.

These sources establish available interfaces and requested model names, not successful Waggle conformance. Runtime and release assertions remain pending the milestone evidence above.
