# Plan for Issue #35: Eliminate remaining hardcoded constants
## Approved Option: A - Complete sweep — flat config + shared startup helper + validation

**Approach:** Add 5 new fields to `config.Defaults` (BusyTimeout, LeaseCheckPeriod, IdleCheckInterval, StartupPollInterval, StartupTimeout). Use `fmt.Sprintf` with `int()` casts for SQL schema DEFAULTs. Extract duplicated startup polling into `broker.WaitForReady()`. Fix error messages to reference config values. Add `config.ValidateDefaults() error` called from NewStore() and broker.New().

**8 files touched:**
1. `config.go` — 5 new fields + ValidateDefaults()
2. `store.go` — PRAGMA + schema DEFAULTs from config
3. `broker.go` — lease check + idle tick from config
4. `lifecycle.go` — new WaitForReady() helper
5. `cmd/root.go` — replace inline loop with WaitForReady()
6. `cmd/start.go` — replace inline loop with WaitForReady()
7. `router.go` — dynamic error messages from config
8. `config_test.go` — test ValidateDefaults + new fields

**Pros:**
- Eliminates the entire class of problem — no hardcoded tunables remain outside config.go
- Startup helper removes 100% duplicated polling code
- Error messages stay truthful if limits change
- ValidateDefaults() prevents zero-value catastrophes
- StartupTimeout (2s) replaces magic `20 * interval`

**Cons:**
- 19 fields in flat struct (from 14)
- 8 files touched — moderate blast radius
- fmt.Sprintf schema generation slightly reduces SQL readability

**Blast radius:** Zero-value config -> ValidateDefaults returns error -> NewStore/New() returns error -> broker fails to start with descriptive message. Fail-loud, no silent corruption.

**Implementation notes from audit:**
- `time.Duration.Seconds()` returns `float64` — must use `int()` cast in `fmt.Sprintf("%d", ...)`
- ValidateDefaults() returns `error`, not panic — callers (NewStore, broker.New) already return errors
- Call ValidateDefaults() from NewStore() and broker.New(), not from init()
- SQL DEFAULTs are safety nets — Go Create() always provides explicit values. Add code comment.
- config is a leaf package (imports only stdlib) — no circular dependency risk

## Approved: 2026-03-25
Audit: APPROVE (Kimi K2.5 via openrouter)
