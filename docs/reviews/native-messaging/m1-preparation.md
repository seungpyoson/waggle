# M1 preparation at approved revision 4

This is implementation preparation, not M1 approval or a design revision. No runtime source has changed. The M0 Claude and Astra design verdicts are APPROVE at document SHA256 `e18636f22a2b970cc5a75384fe29abd6eb6a21361920f109a5435ffee62473b4` and HEAD `5fac0530a789f54b2a87fb10d9d21a14b2dded91`. Claude interactive review effort remains not confirmed; current gate status is recorded in `milestones.json`.

## Clarifications to implement and review in M1

The invariant is that readiness, scheduling eligibility and receipts describe canonical broker state supported by observed facts. The broker enrollment and message lifecycle components enforce it; hooks, CLI clients and drivers cannot independently promote a session or acknowledge a message. Numeric defaults belong in `internal/config/` and remain to be selected in the implementation diff.

1. A direct send to a pending recipient fails explicitly before message persistence, identifying incomplete enrollment. It must not silently become offline enqueue. The separately requested offline-enqueue operation may report storage only; it grants no dispatch eligibility. Pending verification has a configured deadline. If that deadline wins the broker's transition against readiness, retire that pending incarnation with a verification-timeout reason and contextual diagnostics. Never require a tool call to become receive-bound, and never promote an expired incarnation from a late callback.
2. The broker-authored envelope header includes the authenticated acknowledgement command as well as message ID, attempt ID, recipient incarnation, correlation, provenance and deadline. Neither the peer body nor a provider-native reply address supplies the receipt authority. Installed receiver instructions and the envelope use the same command contract; no secret enters either.
3. Message admission resolves an omitted deadline from one configured default and persists the resulting deadline once. Scheduling reads that canonical value. Expiry before intent prevents dispatch even if the recipient never reconnects; expiry after intent preserves the retained-input barrier and does not claim withdrawal.
4. Ownership acquisition with no recorded predecessor cannot explain pre-existing socket or PID files. Startup fails with the conflicting path and leaves it intact. Only verified exit of a recorded predecessor, followed by ownership acquisition and the required file validation, permits predecessor cleanup. There is no timeout-based cleanup branch.

These are requirements for the implementation diff and its review, not claims that code currently enforces them. The existing production-path failures and their source evidence remain in the pinned plan. Before changing runtime code, trace the affected current entrypoints again and identify the superseded paths removed by the same change.

## Baseline build evidence

The unchanged baseline build was attempted with Go build/work caches in an allowed task-specific temporary root and dependency networking disabled:

```sh
waggle_m1_root=/private/var/folders/y5/syb9vj4n3l10028h1jst4_tm0000gn/T/waggle-m1
TMPDIR="$waggle_m1_root/tmp" GOTMPDIR="$waggle_m1_root/tmp" \
GOCACHE="$waggle_m1_root/cache" GOPROXY=off \
go build -o "$waggle_m1_root/waggle-baseline" .
```

It exited 1 while resolving missing `github.com/spf13/cobra v1.10.2` and `modernc.org/sqlite v1.47.0`. The default module cache was not writable: the output reported `operation not permitted` for creating the modernc download directory and opening the Cobra version lock under `/Users/spson/go/pkg/mod/cache/download/`.

A subsequent dependency preparation attempt assigned a writable module cache and used the standard Go proxy, without a direct-download fallback:

```sh
TMPDIR="$waggle_m1_root/tmp" GOTMPDIR="$waggle_m1_root/tmp" \
GOCACHE="$waggle_m1_root/cache" GOMODCACHE="$waggle_m1_root/modules" \
GOPROXY=https://proxy.golang.org go mod download
```

The execution tool rejected that attempt with: `Network access to "proxy.golang.org" was blocked: domain is not on the allowlist for the current sandbox mode.` No alternate proxy or dependency source was attempted. Required module availability and a successful full baseline build remain **not confirmed**.

No full tests, real native sessions, broker-path conformance, ownership fault tests or migration tests were completed by these preparation commands. Existing tests that require the denied `/tmp` root remain an additional environment constraint. Subsequent entrypoint checks established that a local Unix socket bind is denied even under the allowed temporary root, and that both interactive CLI startup attempts fail before native conformance can be tested. See `m1-entrypoint-findings.md` and its command evidence for the precise observations and limits.

Implementation and conformance require the pending reviewer-configuration evidence and an environment that can supply the pinned dependencies and required runtime capabilities. Missing evidence is not replaced with additional transport paths, retries, fabricated receipts or skipped-test passes.
