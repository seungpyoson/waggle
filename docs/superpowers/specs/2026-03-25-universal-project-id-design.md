# Universal Project Identity — Design Spec

**Issue:** #37
**Branch:** `feat/37-universal-project-id`
**Date:** 2026-03-25

## Problem

Waggle identifies projects by hashing the `.git` directory path. Agents in different worktrees, clones, or sandboxes get different brokers even though they're working on the same project. They can't coordinate.

The root cause: project identity is coupled to filesystem location. The fix decouples identity from location by anchoring it to content (git root commit hash).

## Core Invariant

**Project identity is derived from content (git root commit), not location (filesystem path).** All clones, worktrees, and sandboxes of the same repo resolve to the same project ID and share the same broker.

## Design

### 1. `ResolveProjectID()` — Cascade Resolution

Returns a stable string identifier. Four levels, first match wins:

| Priority | Method | Resolves to |
|----------|--------|-------------|
| 1 | `WAGGLE_PROJECT_ID` env var | Use the value directly (cross-repo, sandboxes) |
| 2 | Git root commit: `git rev-list --max-parents=0 HEAD` | Same hash for all clones/worktrees of same repo |
| 3 | `WAGGLE_ROOT` env var | `"path:" + value` (non-git projects) |
| 4 | Nothing available | Error with guidance |

**Worktree resolution:** Before computing the root commit, resolve to the main repo using `git rev-parse --git-common-dir`.

**Multiple root commits:** Repos with merged unrelated histories can have multiple root commits (`git rev-list --max-parents=0 HEAD` returns multiple lines). Resolution sorts them lexicographically and takes the first one. This ensures determinism across machines. The behavior is documented in the function's godoc.

**Git command failure:** If `.git` exists but git commands fail (binary missing, corrupted repo, empty repo with no commits), level 2 falls through gracefully to level 3 (`WAGGLE_ROOT`) or level 4 (error). No panic, no partial state — git failure is treated the same as "not in a git repo".

```go
func ResolveProjectID() (string, error) {
    // 1. Explicit override
    if id := os.Getenv("WAGGLE_PROJECT_ID"); id != "" {
        return id, nil
    }

    // 2. Git root commit (works across clones/worktrees)
    // First resolve worktrees: git rev-parse --git-common-dir
    // Then: git rev-list --max-parents=0 HEAD
    // If multiple root commits (merged unrelated histories), sort and take first
    // Returns the same 40-char SHA for every clone of the same repo

    // 3. WAGGLE_ROOT fallback (non-git)
    if root := os.Getenv("WAGGLE_ROOT"); root != "" {
        return "path:" + root, nil
    }

    // 4. Error
    return "", fmt.Errorf("cannot identify project: not in a git repo, set WAGGLE_PROJECT_ID or WAGGLE_ROOT")
}
```

### 2. `NewPaths(projectID)` — Data Under `~/.waggle/`

Takes a project ID string (not a filesystem path). Computes hash and derives all paths.

- Hash: FNV-1a of projectID, 12 hex chars (same algorithm as before, different input)
- DB/PID/Lock/Log: `~/.waggle/data/<hash>/`
- Socket: `~/.waggle/sockets/<hash>/broker.sock`

```go
func NewPaths(projectID string) Paths {
    home, _ := os.UserHomeDir()
    hash := computeHash(projectID) // FNV-1a, 12 hex chars
    dataDir := filepath.Join(home, ".waggle", "data", hash)
    socketDir := filepath.Join(home, ".waggle", "sockets", hash)

    return Paths{
        ProjectID: projectID,
        DataDir:   dataDir,
        DB:        filepath.Join(dataDir, "state.db"),
        PID:       filepath.Join(dataDir, Defaults.PIDFile),
        Lock:      filepath.Join(dataDir, Defaults.LockFile),
        Log:       filepath.Join(dataDir, Defaults.LogFile),
        Socket:    filepath.Join(socketDir, "broker.sock"),
    }
}
```

**Paths struct changes:**
- Removed: `Root`, `WaggleDir`, `Config` (no longer meaningful — identity isn't path-based)
- Added: `ProjectID`, `DataDir`

**HOME unset:** If `os.UserHomeDir()` fails, all paths that depend on `~/.waggle/` (DataDir, DB, PID, Lock, Log, Socket) will be empty. Callers must check before use — same behavior as current Socket-empty handling, extended to all home-dependent paths.

**Local `.waggle/` no longer used for state:** `<project-root>/.waggle/` is no longer created or used for DB, PID, lock, or log files. All state moves to `~/.waggle/data/<hash>/`. The local `.waggle/` directory may still exist for per-clone config in the future, but this change does not create or depend on it.

### 3. `FindProjectRoot()` Preserved but Decoupled

Still exists for finding the git root (cwd context, per-clone config). No longer feeds into `NewPaths`. Can be used independently for things like locating `.waggle/config.json` per-clone config.

### 4. Daemon Fork: Explicit ID Injection

The parent resolves the project ID once, then passes it explicitly to the daemon as an env var:

```go
// Parent resolves
projectID, _ := config.ResolveProjectID()

// Fork daemon with explicit ID
env := append(os.Environ(), "WAGGLE_PROJECT_ID=" + projectID)
proc, err := os.StartProcess(exe, args, &os.ProcAttr{Env: env, ...})
```

Since `WAGGLE_PROJECT_ID` is priority 1 in the cascade, the daemon uses it directly — no git commands, no cwd dependency, no ambiguity. This eliminates fork-related identity drift for one line of code.

`StartDaemon` gains a `projectID` parameter for env injection.

### 5. Caller Updates

`cmd/root.go` and `cmd/start.go`:
- `FindProjectRoot()` + `NewPaths(root)` → `ResolveProjectID()` + `NewPaths(id)`
- `EnsureDirs` calls use `paths.DataDir` and `filepath.Dir(paths.Socket)` instead of `paths.WaggleDir`

## What Changes

| File | Change |
|------|--------|
| `internal/config/config.go` | New `ResolveProjectID()`, `NewPaths` takes `projectID`, `Paths` struct updated |
| `internal/config/config_test.go` | Tests for all 4 cascade levels + worktree resolution + multiple root commits |
| `internal/broker/lifecycle.go` | `StartDaemon` gains `projectID` param, injects into forked env |
| `cmd/start.go` | Uses `ResolveProjectID()` + `NewPaths(id)`, passes `projectID` to `StartDaemon` |
| `cmd/root.go` | Uses `ResolveProjectID()` + `NewPaths(id)`, passes `projectID` to `StartDaemon` |

## What Doesn't Change

- Broker, session, router, tasks, events, locks, client, CLI commands — untouched
- Wire protocol — untouched
- CLI interface — untouched

## Tests (TDD)

```
TestResolveProjectID_EnvOverride          — WAGGLE_PROJECT_ID set → returns it
TestResolveProjectID_GitRootCommit        — in git repo → returns root commit hash
TestResolveProjectID_GitWorktree          — in worktree → same ID as main repo
TestResolveProjectID_MultipleRootCommits  — merged unrelated histories → sorted, first used
TestResolveProjectID_WaggleRootFallback   — WAGGLE_ROOT set, no git → returns path-based ID
TestResolveProjectID_Error                — no git, no env → error with guidance
TestResolveProjectID_GitCommandFails      — .git exists but git binary fails → falls through to level 3/4
TestResolveProjectID_EmptyRepo            — git repo with no commits → falls through to level 3/4
TestNewPaths_AllEmptyWithoutHome          — HOME unset → all home-dependent paths empty
TestNewPaths_DataDirUnderHome             — all paths under ~/.waggle/data/<hash>/
TestNewPaths_SameIDSamePaths              — same project ID → same paths
TestNewPaths_DifferentIDDifferentPaths    — different ID → different paths
```

## Acceptance Criteria

- [ ] Two different directories, same git repo → same broker (verified live)
- [ ] `WAGGLE_PROJECT_ID=x` in two different repos → same broker
- [ ] Existing single-directory workflow still works
- [ ] All existing tests pass
- [ ] `waggle status` works from any worktree of the same repo

## Smoke Tests

1. Build waggle, start broker from repo root, verify `waggle status` works
2. Create a worktree, run `waggle status` from it — same broker (same PID)
3. Set `WAGGLE_PROJECT_ID=test-id` in two unrelated dirs — same broker
4. Run in a non-git dir without env vars — error with guidance message
