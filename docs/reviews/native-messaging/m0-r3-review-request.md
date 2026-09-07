# M0 revision 3 review request

Use a reviewer runtime configured as Claude Fable 5.1 (`claude-fable-5-1`) with `xhigh` effort. Record actual dispatch configuration; a model's self-description is not identity evidence. This file is a prepared request, not a dispatched or completed review.

---

Perform an independent read-only M0 design review of Waggle revision 3.

Repository: `/Users/spson/Projects/Claude/waggle`

Read `AGENTS.md` and all of `docs/native-messaging-plan.md`.

Expected source HEAD: `5fac0530a789f54b2a87fb10d9d21a14b2dded91`

Expected document SHA256: `3e73fd0458673f34a7fb7654b27802d08365a61694d6f5ef762802f5c73f41fe`

Verify both before review and before verdict. Report DRIFT and withhold approval if either changes. The revision 2 reports are summarized in `docs/reviews/native-messaging/m0-r2-feedback.md`; evaluate every correction independently. Do not use prior approvals as evidence.

The goal is reliable Codex↔Claude and Codex↔Codex communication between exact, independently usable interactive conversations. M0 decides whether this architecture and its explicit conformance gates authorize implementation; it does not certify working messaging or release readiness.

Review structural guarantees against the actual source. Require a precise invariant, one responsible owner, one canonical production path, explicit valid states, and retirement of superseded paths. Reject fallback chains, guessed identity, duplicate state, blind retries and unsupported capability claims. Unperformed M1/M2 tests can remain later evidence obligations; an undefined or contradictory guarantee blocks M0 now.

Inspect these revision 3 decisions especially:

1. Broker ownership: one database ownership row with monotonic generation and process-lifetime tenure; no takeover of a live owner; transactional checks on every canonical mutation/selection; complete submission/callback/filesystem quiescence before release. Can any stale owner submit, write or remove replacement IPC? Are process identity and shutdown failure limits explicit?
2. Cutover: preserve the same canonical database and singleton schema-version table, bump to 2, change only socket/PID names for protocol v2, and use one decoder that rejects missing/unsupported versions. Does this isolate old timeout cleanup without a parallel database or compatibility listener? Assess machine-wide writer cessation, consistent snapshots, exact project-ID partitioning, interrupted conversion/activation, and the old runtime/installer limitations.
3. Claude trust: no native token reaches Waggle, no auth line or permission-class assertion is sent, and the broker is independent of the receiver's process tree. Independent Waggle credentials authenticate CLI calls. Require real proof that default inbound hold remains effective and external socket outcomes are not inferred from Claude's internal sender notices.
4. Codex topology: one explicitly registered Unix App Server and normal remote-attached TUIs, with no queue CLI or transport fallback. Is ownership clear? Are per-thread credential propagation, thread switching/resume, concurrency and peer provenance correctly gated on real M1 evidence?
5. Retained input: intended, accepted, held and uncertain messages keep the consumption barrier. Cancellation/deadline stops future Waggle dispatches without claiming native withdrawal or interrupting shared human turns. Assess definitive drop/consumption evidence, late receipts, operator retirement, re-enrollment and restart. Can a transition release ordering or restart work without evidence?
6. Scope and evidence: read-only inbox, removal of old delivery/startup paths, two-process fault tests, actual previous-binary tests including suspension/timeouts/installation, all real conversation directions, same-directory identity isolation, full tests and degraded-state help.

Separate direct verification, reported evidence, proposed behavior and not-confirmed capabilities. If documentation cannot establish a guarantee, state precisely what M1 must prove or why the design is blocked. Do not solve a missing capability by inventing another path.

Do not edit files, install software, execute provider sessions, run destructive operations, alter user state, expose secrets, bypass restrictions, or spawn reviewers. Preserve the existing `GEMINI.md` deletion. Read-only source, local help and official documentation inspection are permitted. If reviewing without repository access, identify which source claims you cannot verify and withhold a source-verified verdict.

Return:

- APPROVE or REQUEST_CHANGES, explicitly **M0 design only**.
- Blocking findings first: exact location, scenario, violated invariant, evidence and required structural correction.
- Mandatory evidence before M1, M2 and M3 approvals.
- Checks actually performed, limitations, and any reviewer metadata that is independently available.
- Final HEAD, document SHA256 and drift status.

Do not publish, implement, or treat another reviewer's approval as authorization. Both required reviewers must approve this same revision before implementation proceeds.
