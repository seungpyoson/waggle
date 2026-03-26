# Augment Code Brief: Universal Project Identity (#37)

## Agent Configuration

| Role | Specialist | Model | Purpose |
|------|-----------|-------|---------|
| **Implementer** | `implementor` | `gpt-5.4` | Writes tests + implementation for each subtask |
| **Adversarial Reviewer** | `verifier` | `opus4.6` | Reviews each subtask diff against invariant + acceptance criteria |
| **PR Reviewer** | `pr-reviewer` | `gemini-3.1-pro` | Reviews full PR diff before merge |

Three model families — each catches blind spots the others miss.

---

Read these files completely before doing anything else:
1. `internal/config/config.go` — THE code you're changing
2. `internal/config/config_test.go` — existing tests (ALL must still pass)
3. `internal/broker/lifecycle.go` — StartDaemon function
4. `cmd/root.go` and `cmd/start.go` — the two callers
5. `e2e_test.go` — end-to-end test (needs update)

Source of truth is CODE, not docs. Verify pre-traced code paths still match before changing.

## Core Invariant

**Project identity is derived from content (git root commit), not location (filesystem path).** All clones, worktrees, and sandboxes of the same repo resolve to the same project ID and share the same broker.

## Pre-Traced Code Paths (verified — use as ground truth)

- **`NewPaths(root string)`** — `config.go:114-144`. Takes filesystem root, hashes path with FNV-1a, stores state at `<root>/.waggle/`. THIS CHANGES.
- **`Paths` struct** — `config.go:101-110`. Remove `Root`, `WaggleDir`, `Config`. Add `ProjectID`, `DataDir`.
- **Hash computation** — `config.go:126-128`. FNV-1a, 12 hex chars. Same algorithm, input changes from path to projectID.
- **`FindProjectRoot()`** — `config.go:149-186`. STAYS but decoupled from identity.
- **`StartDaemon`** — `lifecycle.go:137`. `func StartDaemon(waggleDir, socketDir, logFile string, args []string)`. First param renames to `dataDir`, gains `projectID string` param.
- **`StartDaemon` env** — `lifecycle.go:160`. `Env: os.Environ()` → `Env: append(os.Environ(), "WAGGLE_PROJECT_ID="+projectID)`.
- **Caller 1** — `root.go:35-40`. `FindProjectRoot(cwd)` + `NewPaths(root)` → `ResolveProjectID()` + `NewPaths(id)`.
- **Caller 2** — `start.go:32-37`. Same pattern, same change.
- **`paths.WaggleDir` refs** — `start.go:46,92` and `root.go:56,62`. All → `paths.DataDir`.
- **E2E test** — `e2e_test.go:29-33`. Creates fake `.git` dir. Remove; inject `WAGGLE_PROJECT_ID` at line 48.

---

## === PHASE 1: WORKSPACE SETUP ===

Branch already exists with 2 doc commits (spec + plan). Do NOT recreate.

```bash
cd ~/Projects/Claude/waggle
git fetch origin
git checkout feat/37-universal-project-id
git log --oneline main..HEAD   # Should show 2 doc commits
go test ./... -count=1          # 115 PASS, 0 FAIL — record baseline
go vet ./...                    # Must be clean
go build -o waggle .            # Must build
```

## === PHASE 2: IMPLEMENTATION (strict order T1→T6) ===

For EACH subtask:
```
a. Write failing test FIRST — run it, confirm it fails
b. Implement the minimal code to make tests pass
c. Run: go test ./internal/config/ -count=1 — ALL tests pass
d. Run: go vet ./... — zero issues
e. If c or d fails: fix before proceeding
f. Adversarial reviewer (opus4.6) reviews diff against invariant
g. If rejected: fix, re-review. Max 2 rounds per subtask.
h. Commit with conventional commit referencing #37
```

Do NOT skip steps a-h. Do NOT batch subtasks.

---

### T1: ResolveProjectID — Tests + Implementation

**Files**: `internal/config/config.go`, `internal/config/config_test.go`

**Add test helpers** to config_test.go (add `"os/exec"` to imports):

```go
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil { t.Fatal(err) }
	t.Cleanup(func() { os.Chdir(orig) })
}

func createGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"git", "init"}, {"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"}, {"git", "commit", "--allow-empty", "-m", "root"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil { t.Fatalf("%v: %s\n%s", args, err, out) }
	}
	return dir
}

func createGitRepoWithMergedHistory(t *testing.T) string {
	t.Helper()
	repo1, repo2 := createGitRepo(t), createGitRepo(t)
	for _, args := range [][]string{
		{"git", "remote", "add", "other", repo2}, {"git", "fetch", "other"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repo1
		if out, err := cmd.CombinedOutput(); err != nil { t.Fatalf("%v: %s\n%s", args, err, out) }
	}
	cmd := exec.Command("git", "merge", "other/main", "--allow-unrelated-histories", "--no-edit")
	cmd.Dir = repo1
	if _, err := cmd.CombinedOutput(); err != nil {
		cmd2 := exec.Command("git", "merge", "other/master", "--allow-unrelated-histories", "--no-edit")
		cmd2.Dir = repo1
		if out, err := cmd2.CombinedOutput(); err != nil { t.Fatalf("merge: %s\n%s", err, out) }
	}
	return repo1
}
```

**Add 7 tests** — see `docs/superpowers/plans/tasks/2026-03-25-universal-project-id/task-1-resolve-tests.md` for complete test code. Tests cover: env override, git root commit (40-char SHA), worktree (same ID as main), multiple root commits (sorted, deterministic), WAGGLE_ROOT fallback (`"path:"+value`), error (mentions both env vars), empty repo (falls through).

**Implement in config.go** (add `"os/exec"`, `"sort"`, `"strings"` to imports):

```go
func ResolveProjectID() (string, error) {
	if id := os.Getenv("WAGGLE_PROJECT_ID"); id != "" { return id, nil }
	if id, err := gitRootCommit(); err == nil { return id, nil }
	if root := os.Getenv("WAGGLE_ROOT"); root != "" { return "path:" + root, nil }
	return "", fmt.Errorf("cannot identify project: not in a git repo; set WAGGLE_PROJECT_ID or WAGGLE_ROOT")
}

func gitRootCommit() (string, error) {
	if _, err := exec.Command("git", "rev-parse", "--git-common-dir").Output(); err != nil {
		return "", fmt.Errorf("not a git repo: %w", err)
	}
	out, err := exec.Command("git", "rev-list", "--max-parents=0", "HEAD").Output()
	if err != nil { return "", fmt.Errorf("git rev-list: %w", err) }
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || lines[0] == "" { return "", fmt.Errorf("no root commits found") }
	sort.Strings(lines) // determinism for merged unrelated histories
	return lines[0], nil
}
```

**Verify**: `go test ./internal/config/ -run TestResolveProjectID -count=1 -v` — all 7 PASS.
**Commit**: `feat(config): add ResolveProjectID with 4-level cascade (#37)`

---

### T2: Update Paths Struct + NewPaths(projectID)

**Files**: `internal/config/config.go`, `internal/config/config_test.go`

**Add 6 new tests** — see `task-3-paths-update.md` for complete code. Tests: DataDirUnderHome, SameIDSamePaths, DifferentIDDifferentPaths, ProjectIDStored, AllEmptyWithoutHome, HashLength.

**Replace Paths struct** (lines 101-110) and **NewPaths** (lines 112-144):

```go
type Paths struct {
	ProjectID string; DataDir string; DB string; PID string; Lock string; Log string; Socket string
}

func NewPaths(projectID string) Paths {
	home, err := os.UserHomeDir()
	if err != nil { return Paths{ProjectID: projectID} }
	f := fnv.New64a()
	_, _ = f.Write([]byte(projectID))
	hash := fmt.Sprintf("%012x", f.Sum64()&0xffffffffffff)
	dataDir := filepath.Join(home, Defaults.DirName, "data", hash)
	socketDir := filepath.Join(home, Defaults.DirName, "sockets", hash)
	return Paths{ProjectID: projectID, DataDir: dataDir,
		DB: filepath.Join(dataDir, Defaults.DBFile), PID: filepath.Join(dataDir, Defaults.PIDFile),
		Lock: filepath.Join(dataDir, Defaults.LockFile), Log: filepath.Join(dataDir, Defaults.LogFile),
		Socket: filepath.Join(socketDir, "broker.sock")}
}
```

(Use proper formatting in actual code — compressed here for brevity.)

**Rewrite existing tests** — exact list in `task-3-paths-update.md`:

| Test | Action |
|------|--------|
| `TestPaths_PathNormalization` (line 136) | DELETE |
| `TestPaths_AllFieldsAbsolute` (line 174) | REWRITE → `TestPaths_AllFieldsPopulated` |
| `TestPaths_SocketEmptyWithoutHome` (line 194) | DELETE (replaced by AllEmptyWithoutHome) |
| `TestPaths_DerivedFromRoot` (line 203) | REWRITE → `TestPaths_DerivedFromProjectID` |
| `TestPaths_SocketHashDeterministic` (line 238) | REWRITE: `"/tmp/x"` → `"project-x"` |
| `TestPaths_SocketHashDiffers` (line 247) | REWRITE: `"/tmp/a"` → `"project-a"` |
| `TestPaths_SocketHashLength` (line 257) | REWRITE: `"/tmp/proj"` → `"test-project"` |

**Verify**: `go test ./internal/config/ -count=1 -v` — all PASS.
**Commit**: `feat(config): NewPaths takes projectID, data moves to ~/.waggle/data/ (#37)`

---

### T3: StartDaemon Explicit ID Injection

**Files**: `internal/broker/lifecycle.go`

**Before**: `func StartDaemon(waggleDir, socketDir, logFile string, args []string) error`
**After**: `func StartDaemon(dataDir, socketDir, logFile, projectID string, args []string) error`

Change `Env: os.Environ()` → `Env: append(os.Environ(), "WAGGLE_PROJECT_ID="+projectID)`

**Verify**: `go build ./internal/broker/` — PASS.
**Commit**: `feat(broker): StartDaemon injects WAGGLE_PROJECT_ID into forked env (#37)`

---

### T4: Update CLI Callers

**Files**: `cmd/root.go`, `cmd/start.go`

Both: `FindProjectRoot(cwd)` + `NewPaths(root)` → `ResolveProjectID()` + `NewPaths(id)`.

Key changes: `paths.WaggleDir` → `paths.DataDir`; `paths.Socket == ""` → `paths.DataDir == ""`; add `projectID` param to `StartDaemon` calls; remove `os.Getwd()` calls.

See `task-5-callers.md` for complete replacement code.

**Verify**: `go build .` — PASS. `go test ./... -count=1 -short` — all PASS.
**Verify**: `grep -n "WaggleDir\|\.Root\b\|FindProjectRoot" cmd/root.go cmd/start.go` — ZERO matches.
**Commit**: `feat(cmd): switch CLI to ResolveProjectID + NewPaths(id) (#37)`

---

### T5: Update E2E Test

**Files**: `e2e_test.go`

Remove fake `.git` creation (lines 29-33). Replace with `project := t.TempDir()`. Update env (line 48): `startCmd.Env = append(os.Environ(), "HOME="+tmpHome, "WAGGLE_PROJECT_ID=e2e-test-project")`

**Verify**: `go build -o waggle . && go test -v -run TestE2E -count=1` — PASS.
**Commit**: `test: update E2E to use WAGGLE_PROJECT_ID (#37)`

---

### T6: Live Smoke Tests

```bash
cd ~/Projects/Claude/waggle && go build -o waggle .

# 1. Normal single-directory → broker starts
./waggle stop 2>/dev/null || true; ./waggle status  # Record PID

# 2. Subdirectory → SAME PID
cd internal && ~/Projects/Claude/waggle/waggle status && cd ..

# 3. Worktree → SAME PID
git worktree add /tmp/waggle-wt-test -b smoke-wt HEAD
cd /tmp/waggle-wt-test && ~/Projects/Claude/waggle/waggle status && cd ~/Projects/Claude/waggle
git worktree remove /tmp/waggle-wt-test && git branch -D smoke-wt

# 4. WAGGLE_PROJECT_ID → shared broker across dirs
./waggle stop 2>/dev/null || true
WAGGLE_PROJECT_ID=smoke-test ./waggle status
cd /tmp && WAGGLE_PROJECT_ID=smoke-test ~/Projects/Claude/waggle/waggle status

# 5. Non-git dir, no env → error mentioning both env vars
cd /tmp && unset WAGGLE_PROJECT_ID && unset WAGGLE_ROOT
~/Projects/Claude/waggle/waggle status 2>&1  # Must mention WAGGLE_PROJECT_ID and WAGGLE_ROOT

# Cleanup
cd ~/Projects/Claude/waggle && ./waggle stop; WAGGLE_PROJECT_ID=smoke-test ./waggle stop 2>/dev/null || true
```

**ALL 5 must pass.** Fix before PR.

---

## === PHASE 3: FINAL VERIFICATION ===

```bash
go test ./... -count=1    # >= 115 PASS, 0 FAIL
go vet ./...              # Clean
go build -o waggle .      # Clean
git log --oneline main..HEAD && git diff --stat main..HEAD
```

## === PHASE 4: PR CREATION + REVIEW ===

```bash
git push origin feat/37-universal-project-id
gh pr create --title "#37: Universal project identity — git root commit hash + cascading fallback" \
  --body "## Summary
- 4-level cascade: WAGGLE_PROJECT_ID → git root commit → WAGGLE_ROOT → error
- Data moved from <root>/.waggle/ to ~/.waggle/data/<hash>/
- Daemon fork injects WAGGLE_PROJECT_ID (no re-resolution race)
- All clones/worktrees of same repo share one broker

## Verified: two dirs same repo → same broker, worktree → same broker, env override works, error guidance works, all 115+ tests pass

Closes #37"
```

PR reviewer (`gemini-3.1-pro`) checks: invariant holds, no removed-field refs in cmd/, daemon env injection, multi-root sort, git failure fallthrough, FindProjectRoot preserved.

## Traps to Avoid

- Do NOT modify or delete `FindProjectRoot()` or its 6 tests (`TestFindProjectRoot_*`). It stays unchanged — decoupled from identity, not removed.
- Do NOT remove `Defaults.ConfigFile` from the Defaults struct. It becomes unused by `NewPaths` but removing it changes struct layout and breaks `ValidateDefaults` reflection.
- Do NOT use `t.Parallel()` in any test that calls `chdir()` or `t.Setenv()` — they mutate shared process state.
- Do NOT modify broker core, session, router, tasks, events, locks, client, or wire protocol.
- Do NOT use `FindProjectRoot()` to feed into `NewPaths()` — that's the old pattern.
- Do NOT leave references to `paths.Root`, `paths.WaggleDir`, or `paths.Config` anywhere in cmd/.
- Do NOT skip live smoke tests — they ARE the acceptance criteria.
- Do NOT use `os.Environ()` alone in StartDaemon — must append `WAGGLE_PROJECT_ID`.
- Do NOT batch subtasks into one commit.
- Do NOT use `filepath.Abs`/`EvalSymlinks` on WAGGLE_ROOT in ResolveProjectID — just `"path:" + root`.

## Must-Read Task Detail Files

The brief compresses some code for brevity. Complete test code and replacement tests are in:
- `docs/superpowers/plans/tasks/2026-03-25-universal-project-id/task-1-resolve-tests.md` — full 7 test functions
- `docs/superpowers/plans/tasks/2026-03-25-universal-project-id/task-3-paths-update.md` — full 6 new tests + all 7 rewritten tests
- `docs/superpowers/plans/tasks/2026-03-25-universal-project-id/task-5-callers.md` — full replacement code for root.go and start.go

Read these BEFORE implementing each subtask. The brief's inline code is a summary; the task files are the source of truth.
