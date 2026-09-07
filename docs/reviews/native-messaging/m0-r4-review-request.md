# M0 revision 4 review request

Configure the reviewer as Claude Fable 5.1 (`claude-fable-5-1`) at `xhigh`. Record dispatch configuration independently of the model's self-description. This prepared request is not a completed review.

---

Perform an independent, read-only M0 design review of Waggle revision 4.

Repository: `/Users/spson/Projects/Claude/waggle`

Read `AGENTS.md` and the complete `docs/native-messaging-plan.md`. Inspect relevant source to validate claims. Do not use prior approvals as evidence.

Expected HEAD: `5fac0530a789f54b2a87fb10d9d21a14b2dded91`

Expected document SHA256: `e18636f22a2b970cc5a75384fe29abd6eb6a21361920f109a5435ffee62473b4`

Verify both before review and before verdict. If either differs, report DRIFT and withhold approval. The branch is `feat/native-agent-messaging`; no runtime implementation has started.

The previous Claude review requested changes because receive activation depended on an ordinary outbound tool call, preventing first delivery to a fresh idle session. Revision 4 makes endpoint/conversation verification and native readiness sufficient for receive-binding. Outbound identity authenticates independently per call; proving credential propagation belongs to M1 conformance, not each session's activation.

Review the complete design for structural contradictions, especially:

1. Can the first policy-eligible message reach a freshly enrolled idle session with zero prior ordinary tool calls? Can bounded SessionStart return before native readiness without needing another human prompt? Are reconnection, missing enrollment, and independent per-call authentication coherent?
2. Does Codex verify the exact thread on its registered Unix endpoint and reject plain/unattached sessions? Are first-message acknowledgement, ordinary tool credentials, same-directory isolation, fork/resume and concurrent TUI behavior correctly gated on M1 evidence?
3. Does the native envelope and installed acknowledgement instruction make consumption receipts possible without fabricating them in hooks, callbacks or inbox reads? Do late receipts preserve stop/deadline semantics?
4. Do lifetime broker ownership, generation checks, configured shutdown bounds and predecessor-leftover cleanup exclude stale writes/submission/endpoint removal without timeout takeover?
5. Is machine-wide cutover explicit about OS process/open-file cessation, incomplete inspection, replacement of old PATH/service executables, retained snapshots, same-database schema fencing, v2 IPC isolation and canonical prepared/active state? Is interruption safe within the declared offline boundary?
6. Are Claude token exclusion, independent broker ancestry, default inbound hold and expired-hold limitations preserved? Does retained or uncertain input keep its barrier without claiming native withdrawal?

Require one invariant owner and one canonical path, explicit states, clear prerequisite failures, removal of superseded paths, and real-entrypoint evidence. Reject fallbacks, duplicated state, guessed identity, blind retries and unsupported confidence. M0 approves a coherent design and hard evidence gates; it does not certify unperformed M1/M2/M3 behavior.

Return APPROVE or REQUEST_CHANGES, explicitly **M0 design only**. Give blocking findings first with exact location, scenario, violated invariant, evidence and required correction. Then list mandatory milestone evidence, checks actually performed, limitations, independently available reviewer metadata, and final HEAD/digest/drift status.

Do not edit, install, start provider sessions, mutate user state, expose secrets, bypass restrictions, spawn agents or publish the review. Preserve the existing `GEMINI.md` deletion. If repository/source access is unavailable, state what remains unverified rather than claiming a source-verified review. Both required reviewers must approve this same revision before implementation.
