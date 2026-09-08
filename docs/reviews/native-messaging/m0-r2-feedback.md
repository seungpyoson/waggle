# Revision 2 external review feedback and disposition

Source: two review reports supplied directly by the user on 2026-09-05. This file summarizes them; the verbatim reports remain in the conversation. Neither report is an approval.

Both reports identify the reviewed document SHA256 as `35aaac843cec2790983ad21ea5d7327a2aa52b1433783716e2e517ff75486366`, base HEAD `5fac0530a789f54b2a87fb10d9d21a14b2dded91`, and drift NONE. The reviewed bytes are preserved in `m0-r2-plan.md`.

Claude verdict: **REQUEST_CHANGES — M0 design only**. The report says its harness identifies `claude-fable-5-1`; effort is not observable inside that session. Dispatch identity/effort were not independently verified by this agent. The report confirms source inspection, not builds, tests or native Waggle conformance.

GPT verdict: **REQUEST_CHANGES — M0 design only**. The report explicitly marks model/effort metadata unconfirmed. It reports source, official documentation and CLI-help inspection; no builds, tests or native sessions. Do not equate this externally supplied report with the separately dispatched prior Astra approval.

## Blocking findings addressed in revision 3

1. **Claude: sole broker ownership lacked enforcement.** Source permits a second broker after timeout-driven socket/PID deletion. Revision 3 chooses one transactional database ownership row with monotonically increasing generation and process-lifetime tenure. No health-timeout takeover is permitted. All writes/selection require current ownership; native submission and endpoint cleanup drain before release. This is a deliberate correction to a renewable lease: a database generation alone cannot fence external effects from a paused old process.
2. **Claude: cutover identities and machine scope were unspecified.** Revision 3 preserves the canonical database path and the existing shared schema-version table, replacing its singleton value with 2. It specifies distinct v2 socket/PID names, one version-rejecting decoder, machine-wide offline conversion, exact project-ID partitioning, and old-binary timeout/installer tests. Versioned IPC avoids relying on an error response from a suspended process. Verified cessation is required because old open database handles do not recheck schema version. Arbitrary later reinstallation of old software remains an explicit external limitation.
3. **Claude: native token handling could elevate peer messages.** Revision 3 forbids the native Claude token from reaching Waggle and sends no native auth line or permission-class assertion. Independent Waggle capabilities authenticate outbound calls. Broker process ancestry must also avoid the receiving session's own-child exemption; merely omitting the token is insufficient.
4. **GPT: provider-retained input lacked ordering/stop semantics.** Revision 3 keeps the recipient barrier for intended, held, accepted and uncertain input until correlated consumption or definitive non-retention evidence. Expiry/stop prevents future Waggle dispatches and does not claim native withdrawal. The initial contract invokes no provider cancellation or shared-turn interruption. Late evidence, indefinite uncertainty, operator retirement and re-enrollment restrictions are explicit.

## Additional corrections addressed

- Hook evidence now identifies an unsupported top-level field at PreToolUse; prior fixture execution remains attributed, with no claim of live host receipt.
- Codex has one explicit Unix App Server/remote TUI topology, independently owned by the user. Its native Unix wire protocol uses WebSocket framing. `codex queue`, implicit shared-daemon discovery, TCP WebSocket endpoints and replacement servers are excluded from the Waggle delivery path; upstream experimental status remains explicit.
- Codex outbound credential propagation is a required per-thread M1 proof; undocumented hook exports or process-wide environment cannot substitute. Plain-session daemon topology is not assumed.
- Claude endpoint propagation is documented, while supplied live-session observations remain reviewer evidence. Waggle's own credential propagation is still unconfirmed.
- Inbox reads lose mark-seen mutations.
- Held, refused, rate-limited, duplicate, queue-cap and silent-drop outcomes require actual external-socket observations before receiving definitive labels.
- M1/M2/M3 evidence now includes retained input, shared-turn stop limits, stale live owners/handles/callbacks, prior-binary cleanup and installers, same-directory isolation, interrupted multi-project cutover, and allowed short socket roots.

These are proposed design corrections, not verified runtime fixes. Fresh dual M0 approval is required for revision 3. Runtime implementation has not started.
