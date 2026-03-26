# Task 4 Brief: Lock Manager — Advisory Coordination

**Branch:** `feat/001-004-foundation` (same branch, commit after Tasks 1-3)
**Goal:** In-memory advisory lock table tied to session identity.

**Files to create:**
- `internal/locks/manager.go`
- `internal/locks/manager_test.go`

**Dependencies:** None (no imports from other internal packages).

---

## What to build

**`Lock` struct** — `Resource string`, `Owner string`, `AcquiredAt string` (RFC3339 UTC). JSON tags on all fields.

**`Manager` struct** — Thread-safe map: `resource → Lock`. Uses `sync.RWMutex`.

**Methods:**
- `NewManager() *Manager`
- `Acquire(resource, owner string) error` — lock resource for owner. Error if held by different owner. Idempotent: same owner re-acquiring is a no-op success.
- `Release(resource, owner string)` — release only if held by this owner. Wrong owner = no-op.
- `ReleaseAll(owner string)` — release ALL locks held by owner (used on session disconnect)
- `List() []Lock` — return all active locks
- `Count() int`

## Invariants

| ID | Invariant | How to verify |
|----|-----------|---------------|
| L1 | Only one owner can hold a resource | Test: acquire by owner-1, acquire by owner-2 → error |
| L2 | Same owner can re-acquire (idempotent) | Test: acquire twice by same owner → success |
| L3 | Release only works for the holding owner | Test: acquire by owner-1, release by owner-2 → lock remains |
| L4 | ReleaseAll only releases that owner's locks | Test: 2 owners hold locks, ReleaseAll(owner-1) → owner-2 unaffected |
| L5 | Error message includes the current holder's name | Test: check error string contains owner name |
| L6 | Concurrent acquire/release doesn't race | Test: run with -race flag |

## Tests

```
TestManager_AcquireAndRelease     — acquire, verify in List, release, verify empty
TestManager_AcquireConflict       — different owner → error containing holder name
TestManager_SameOwnerReacquire    — same owner twice → no error
TestManager_ReleaseWrongOwner     — release by non-holder → lock remains
TestManager_ReleaseAll            — releases only that owner's locks
TestManager_AcquiredAtSet         — verify AcquiredAt is non-empty RFC3339 (NEW)
```

## Acceptance criteria

- [ ] All 6 tests pass: `go test ./internal/locks/ -v -count=1`
- [ ] Race detector passes: `go test ./internal/locks/ -race -count=1`
- [ ] `go vet ./internal/locks/` — zero warnings
- [ ] Only imports `fmt`, `sync`, `time`, `testing`
- [ ] manager.go under 70 lines
- [ ] Commit: `feat(locks): advisory lock manager`
