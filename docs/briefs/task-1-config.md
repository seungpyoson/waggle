# Task 1 Brief: Config Module — Path Resolution and Defaults

**Branch:** `feat/001-004-foundation`
**Goal:** Build the foundation module that every other module depends on. One source of truth for all paths and configurable values.

**Files to create:**
- `internal/config/config.go`
- `internal/config/config_test.go`

**Dependencies:** None. First task.

---

## What to build

**`Defaults` var** — Single struct holding every configurable value:
- Dir/file names: `.waggle`, `state.db`, `config.json`, `broker.pid`, `start.lock`, `broker.log`, `.waggle/sockets`, `broker.sock`
- Durations: LeaseDuration (5m), IdleTimeout (30m), LeaseCheckPeriod (30s)
- Ints: MaxRetries (3), DefaultPriority (0)

**`FindProjectRoot(startDir string) (string, error)`**
- If `WAGGLE_ROOT` env var is set → `filepath.EvalSymlinks(filepath.Clean(override))` → return
- Otherwise, `filepath.Abs(startDir)` → `filepath.EvalSymlinks` → walk up looking for `.git` (file or directory — `os.Stat` matches both)
- Error if no `.git` found. Error message MUST mention `WAGGLE_ROOT`.

**`NewPaths(root string) Paths`**
- Computes ALL derived paths from root. This is the ONLY place paths are constructed.
- Socket path: `~/.waggle/sockets/<hash>/broker.sock` where `<hash>` = first 12 hex chars of SHA-256 of root
- All other paths under `<root>/.waggle/`

**`Paths` struct** — Root, WaggleDir, DB, Config, PID, Lock, Log, Socket

## Invariants (must ALL hold)

| ID | Invariant | How to verify |
|----|-----------|---------------|
| C1 | No hardcoded paths in config.go | `grep -n '"/.*"' internal/config/config.go` returns only `"WAGGLE_ROOT"` and format strings |
| C2 | Same root → same socket path | Test: deterministic |
| C3 | Different roots → different socket paths | Test: differs |
| C4 | Symlinked paths resolve to same root | Test: symlink |
| C5 | WAGGLE_ROOT overrides .git detection | Test: env override |
| C6 | Socket hash is exactly 12 hex chars `[0-9a-f]` | Test: regex |
| C7 | All Paths fields are non-empty and absolute | Test: iterate fields |
| C8 | Error for no .git mentions WAGGLE_ROOT | Test: check error string |

## Tests (TDD — write these first, then implement)

```
TestFindProjectRoot_FromSubdir        — .git in parent, call from child → returns parent
TestFindProjectRoot_NoGitDir          — no .git anywhere → error containing "WAGGLE_ROOT"
TestFindProjectRoot_EnvOverride       — WAGGLE_ROOT set → ignores startDir, returns override
TestFindProjectRoot_SymlinkResolved   — symlink to project dir → returns canonical path
TestPaths_DerivedFromRoot             — all fields computed correctly from root
TestPaths_SocketHashDeterministic     — same root → same socket
TestPaths_SocketHashDiffers           — different root → different socket
TestPaths_SocketHashLength            — hash portion is exactly 12 hex chars
TestPaths_AllFieldsAbsolute           — every field is non-empty and absolute
```

## Acceptance criteria

- [ ] All 9 tests pass: `go test ./internal/config/ -v -count=1`
- [ ] `go vet ./internal/config/` — zero warnings
- [ ] No imports outside stdlib (`crypto/sha256`, `fmt`, `os`, `path/filepath`, `time`)
- [ ] config.go under 120 lines
- [ ] Commit: `feat(config): path resolution and defaults`
