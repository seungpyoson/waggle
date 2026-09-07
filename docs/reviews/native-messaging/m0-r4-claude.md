# M0 Claude review — revision 4

Verdict: **APPROVE — M0 design only. Blocking findings: none.**

Provenance: the user supplied the completed interactive Claude review and subsequently confirmed “it was Claude.” This file summarizes that report; it is not a response from our unsuccessful CLI dispatch.

Reviewed document: `docs/native-messaging-plan.md`.

Document SHA256: `e18636f22a2b970cc5a75384fe29abd6eb6a21361920f109a5435ffee62473b4`.

Base HEAD: `5fac0530a789f54b2a87fb10d9d21a14b2dded91`.

Branch: `feat/native-agent-messaging`.

The report states that both pins matched before review and before verdict, with no drift. The recording agent independently verified the same current HEAD and document digest. The approved document has not been edited to update its historical status text; current review status lives in `milestones.json`.

## Assessment reported by Claude

Revision 4 resolves the fresh-idle enrollment contradiction: endpoint and conversation verification plus observed native availability establish receive-binding without a prior tool call, test send, acknowledgement or human prompt. Continued observation of the same endpoint can finish verification after the bounded hook returns.

The reviewer found the exact Codex thread binding, per-call sender authentication, recipient-specific acknowledgements, process-lifetime broker ownership, bounded shutdown, verified-predecessor cleanup, machine-wide cutover census, executable retirement and canonical activation state coherent. It also accepted Claude token exclusion, independent broker ancestry, preserved inbound holds and the retained-input ordering barrier. Deadline and stop do not claim native withdrawal.

Four nonblocking clarifications belong in the M1 implementation diff:

1. Define the send result for a pending recipient and a configured bound on pending verification, with explicit enrollment-failure diagnostics.
2. Include the acknowledgement command in the Waggle-authored envelope header itself. Claude's native reply address is not a Waggle receipt path.
3. State a configured default message deadline, including stored messages whose recipient never reconnects.
4. A first owner with no recorded predecessor must fail on leftover endpoint files rather than remove them.

These are tracked in `m1-preparation.md`; they do not amend the approved document bytes.

## Required evidence reported by Claude

- M1: fresh sessions with zero prior tool calls receive and acknowledge their first message; ordinary tool credential propagation; selected Codex Unix/remote-TUI topology, endpoint membership, plain-session rejection, busy behavior and fork/resume/switch isolation; service-started broker and default hold in bypass-mode Claude; observed external socket outcomes for delivery, hold, refusal, rate limits, duplicates and queue capacity; missing-enrollment diagnostics.
- M2: acquisition races, suspended owner, stale handles/generations, submission during shutdown, join timeout, interrupted cleanup and no former-owner endpoint deletion; cessation census rejection of surviving handles and incomplete inspection; actual previous-binary startup, timeout cleanup, replay, ack, reservation, runtime, bootstrap and installer scenarios; executable replacement, interrupted conversion/activation, read-only inbox and removal of superseded paths.
- M3: all three conversation directions and correlated replies, same-directory session pairs, fresh-idle and expired-hold/operator-retirement cases, full build/tests under an allowed short temporary root, degraded CLI help and verified reviewer dispatch metadata.

## Checks and metadata

The reviewer reports direct source inspection, a line-by-line comparison with the pinned revision 3 snapshot, pinned local CLI help, official Claude and OpenAI documentation, presence of relevant identifiers in the Claude binary, inspection of the sys dependency, and presence of the census tool. No build, tests or provider session was run. No files were edited; the existing `GEMINI.md` deletion was preserved. Prior Astra reports were not read.

The review ran interactively in Claude Code, not through the recorded CLI dispatch. Its harness reportedly identified `claude-fable-5-1`. This recording agent has not independently verified that session's model configuration. The report explicitly says effort was not observable and was not asserted. Requested effort was `xhigh`; actual effort remains **not confirmed**. The user's provider clarification does not establish effort.

Both revision 4 design verdicts are now APPROVE at the same digest. The requirement for verified reviewer configuration remains unresolved for the interactive Claude session; `milestones.json` records this separately from its affirmative design verdict. Runtime conformance, cutover and release correctness remain **not confirmed**.
