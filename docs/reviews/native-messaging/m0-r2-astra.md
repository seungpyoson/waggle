# M0 Codex review — revision 2

Historical review, superseded by revision 3 and retained for provenance. It does not approve the current design; later user-supplied revision 2 REQUEST_CHANGES reports are recorded separately in `m0-r2-feedback.md`.

Verdict: **APPROVE — revision 2, M0 design only. No blocking findings.**

Dispatch identity: existing collaboration reviewer `m0_astra_review`, originally explicitly configured with `model=gpt-6-astra`, `reasoning_effort=xhigh`, and a fresh context; revision 2 was dispatched with `collaboration.followup_task` to that same reviewer. This records tool dispatch configuration, not a model's self-reported identity. No fallback model was requested.

Reviewed document: `docs/native-messaging-plan.md`.

Document SHA256: `35aaac843cec2790983ad21ea5d7327a2aa52b1433783716e2e517ff75486366`.

Base HEAD: `5fac0530a789f54b2a87fb10d9d21a14b2dded91`.

The reviewer re-pinned both values before its verdict and reported **no DRIFT**. The revision 1 approval is superseded. No document changes were required for M0 approval.

The review independently confirmed the existing receipt split in source and found that the proposed broker ownership, single durable queue, removal of the runtime delivery ledger, and explicit retirement list address that structural failure.

Required subsequent evidence:

1. M1: both hosts propagate distinct session credentials into ordinary agent CLI calls through supported mechanisms. Prove isolation for two sessions in one directory, delivery into the actual interactive conversation, and preservation of native inbound controls and tool policies. This is not confirmed.
2. M1/M2: prove native acceptance separately from correlated acknowledgement, held-message transitions, uncertainty after lost responses, and blocked later submissions while an earlier acceptance remains unresolved. Deadline or cancellation must not imply retraction of already-submitted provider work without evidence.
3. M2: use the actual previous binary to prove the cutover fence. `internal/tasks/store.go:110` provides an existing schema-version rejection point before message-store initialization. Include old startup and health behavior: `internal/broker/lifecycle.go:98` accepts a complete error response, whereas silence can trigger socket cleanup. Verify data preservation and removal of all superseded messaging paths.
4. M3: complete the real conversation matrix, interruption/fault scenarios, full tests, degraded-state help, and terminal checks.

This review was source inspection only. The reviewer executed no provider sessions or runtime tests and did not rerun the previously reported hook fixture. It edited no files. Runtime implementation still requires Claude Fable 5.1 xhigh approval of this same document digest; that review has no response because the authorized dispatch was blocked by the network allowlist.
