# Revision 3 Claude review feedback and disposition

Source: the user supplied an interactive Claude Code review on 2026-09-05. This file summarizes the report; the verbatim report remains in the conversation.

Verdict: **REQUEST_CHANGES — M0 design only**.

Reviewed HEAD: `5fac0530a789f54b2a87fb10d9d21a14b2dded91`.

Reviewed document SHA256: `3e73fd0458673f34a7fb7654b27802d08365a61694d6f5ef762802f5c73f41fe`. The exact document is preserved in `m0-r3-plan.md`. The reviewer reports drift NONE before and after review.

Provenance: the report identifies an interactive Claude Code harness with model `claude-fable-5-1`; effort is unobservable and not asserted. This is user-supplied review evidence, not output from our blocked CLI dispatch. Neither model nor effort was independently observed by this agent for that interactive session. The report describes source/help/documentation and dependency inspection; no builds, tests or provider sessions. The reviewer edited no files and preserved `GEMINI.md`.

## Blocking finding

Revision 3 required an ordinary tool call to demonstrate outbound credentials before enrollment activation. A fresh idle session had not made such a call, so its first message could not reach it. This contradicted idle delivery and the M1 fresh-session case.

Revision 4 assigns pending/receive-bound transitions to broker endpoint/conversation verification and native readiness. Receive-binding requires no ordinary tool call or human prompt. Each outbound call authenticates independently; credential propagation is a one-time M1 host/version conformance obligation, not a per-session activation gate. M1 explicitly begins with zero prior ordinary tool calls and verifies that the first delivery contains the identifiers needed for the first authenticated acknowledgement/reply.

## Other requested clarifications

- The acquiring owner may remove predecessor v2 endpoint leftovers only after verified predecessor exit and generation acquisition, with path/type/ownership validation. Shutdown join has a configured deadline; failure retains ownership and closed admission.
- Cutover names OS process identity plus an open-file census of database/WAL/SHM device/inodes, requires complete visibility, and replaces old supported PATH/service executables. M2 must prove the check; no current cessation claim is made.
- Per-project prepared/active cutover state and provenance live in the canonical database. Prepared stores cannot enroll or dispatch.
- Enrollment occurs at SessionStart. A session started before the broker must restart through the supported entrypoint; the error must say so. Reconnection restores only prior verified enrollment.
- Codex must verify concrete thread membership on the registered endpoint; plain/unattached sessions are explicitly rejected.
- Native envelopes preserve message/attempt/correlation identifiers. Installed receiver instructions require authenticated consumption acknowledgement or a correlated reply; transport callbacks and inbox reads cannot fabricate it.
- Default inbound hold for a bypass-mode Claude receiver is a supported-configuration limitation, with no Waggle mode change. An expired hold lacking definitive drop evidence retains its barrier until reconciliation or retirement.
- M1/M2/M3 add fresh-idle delivery, bounded join failure, crash leftovers, cessation-check failure cases, PATH retirement, database activation interruption and expired-hold/operator-retirement coverage.

The reviewer found revision 3's other structural choices coherent; that does not approve revision 4. All corrections remain proposed until fresh dual M0 approval. Runtime code and native conformance remain unimplemented/unconfirmed.
