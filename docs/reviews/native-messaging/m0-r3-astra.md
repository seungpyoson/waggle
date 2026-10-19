# M0 Codex review — revision 3

Historical approval, superseded by revision 4. The later user-supplied Claude REQUEST_CHANGES report is recorded in `m0-r3-feedback.md`; this file does not authorize the current design.

Verdict: **APPROVE — M0 design only. Blocking findings: none.**

Dispatch identity: fresh collaboration reviewer `m0_r3_astra_review`, explicitly configured with `model=gpt-6-astra`, `reasoning_effort=xhigh` and `fork_turns=none`. This is tool dispatch evidence, not self-reported model identity.

Final reviewed document: `docs/native-messaging-plan.md`.

Document SHA256: `3e73fd0458673f34a7fb7654b27802d08365a61694d6f5ef762802f5c73f41fe`.

Base HEAD: `5fac0530a789f54b2a87fb10d9d21a14b2dded91`.

The initial request covered digest `c49b61d1481b2553e2ea4f561648311d0bf9b1921037849af1a631797b82d07f`. Before verdict, the reviewer was explicitly notified of two clarifications: process exit cannot prove retained input absent across resume; native Unix transport uses WebSocket framing and retains upstream experimental limitations. It withheld its verdict, inspected the completed revision, and re-pinned to the final digest above. It reported no drift against the updated request.

The reviewer found coherent boundaries for process-lifetime broker ownership, guarded canonical transactions, complete shutdown quiescence, same-database cutover with distinct IPC names, retained-input ordering, explicit stop/deadline limits, safe operator resolution, and the single native path for each provider. It confirmed the previous schema rejection and IPC cleanup paths against source. The retirement list includes inbox receipt mutations.

Mandatory later evidence:

1. M1: actual Unix WebSocket wire behavior, concurrent normal TUI use, authenticated CLI round trips and per-thread credential isolation across same-directory sessions, switching, forks and resume. Bind to concrete `thread.id`; `thread.sessionId` can be shared within a session tree. Demonstrate peer provenance and unchanged permissions through the actual selected native input.
2. M1: Claude endpoint binding without token export, independent broker ancestry, default hold in a bypass-mode receiver, and the actual external delivered/held/refused/drop outcomes. Internal sender-tool notices are not driver receipts.
3. M2: generation fencing, complete shutdown quiescence, retained input through interruption/deadlines/late receipts/retirement/resume, and actual old-binary data/IPC isolation including suspension/timeouts and installer side effects.
4. M3: the real conversation matrix, successful build/full tests, degraded-state help, installation checks, and both required final reviews.

The reviewer performed source inspection, local Codex-help checks and official OpenAI-documentation checks. Its Claude reference fetch and router diagnostic failed with PermissionError; it did not independently refresh that reference. It ran no builds, tests, provider sessions or migration. It edited no files. Native conformance is **not confirmed**.

This approval covers only the pinned design. Claude Fable 5.1 xhigh approval of the same revision is still required before implementation.
