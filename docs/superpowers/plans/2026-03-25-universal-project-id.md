# Universal Project Identity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Decouple project identity from filesystem paths so all clones, worktrees, and sandboxes of the same repo share one broker.

**Architecture:** New `ResolveProjectID()` implements a 4-level cascade (env var → git root commit → WAGGLE_ROOT → error). `NewPaths()` changes from taking a filesystem root to taking a project ID string. All state moves from `<root>/.waggle/` to `~/.waggle/data/<hash>/`. Daemon fork receives the resolved ID via explicit env injection.

**Tech Stack:** Go, `os/exec` for git commands, FNV-1a hashing, SQLite (unchanged)

**Spec:** `docs/superpowers/specs/2026-03-25-universal-project-id-design.md`

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/config/config.go` | Modify | `ResolveProjectID()`, updated `NewPaths(projectID)`, updated `Paths` struct |
| `internal/config/config_test.go` | Modify | TDD tests for cascade resolution + new path computation |
| `internal/broker/lifecycle.go` | Modify | `StartDaemon` gains `projectID` param for env injection |
| `cmd/start.go` | Modify | Switch to `ResolveProjectID()` + `NewPaths(id)` pattern |
| `cmd/root.go` | Modify | Switch to `ResolveProjectID()` + `NewPaths(id)` pattern |
| `e2e_test.go` | Modify | Update E2E to use WAGGLE_PROJECT_ID env var |

---

## Task Summary

8 tasks, TDD throughout. Detailed steps in `tasks/2026-03-25-universal-project-id/`.

| Task | Description | Files | Detail |
|------|-------------|-------|--------|
| 1 | TDD — ResolveProjectID tests + stub | config.go, config_test.go | [task-1-resolve-tests.md](tasks/2026-03-25-universal-project-id/task-1-resolve-tests.md) |
| 2 | Implement ResolveProjectID cascade | config.go | [task-2-resolve-impl.md](tasks/2026-03-25-universal-project-id/task-2-resolve-impl.md) |
| 3 | TDD — NewPaths + Paths struct update | config.go, config_test.go | [task-3-paths-update.md](tasks/2026-03-25-universal-project-id/task-3-paths-update.md) |
| 4 | StartDaemon explicit ID injection | lifecycle.go | [task-4-daemon-inject.md](tasks/2026-03-25-universal-project-id/task-4-daemon-inject.md) |
| 5 | Update CLI callers | root.go, start.go | [task-5-callers.md](tasks/2026-03-25-universal-project-id/task-5-callers.md) |
| 6 | Update E2E test | e2e_test.go | [task-6-e2e.md](tasks/2026-03-25-universal-project-id/task-6-e2e.md) |
| 7 | Live smoke tests | — | [task-7-smoke.md](tasks/2026-03-25-universal-project-id/task-7-smoke.md) |
| 8 | Final verification | — | [task-8-verify.md](tasks/2026-03-25-universal-project-id/task-8-verify.md) |

## Test Helpers (shared across tasks 1-3)

Add to `internal/config/config_test.go` imports: `"os/exec"`, `"strings"`.

```go
// chdir changes to dir and restores original cwd on cleanup.
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })
}

// createGitRepo creates a temp dir with a real git repo (one commit).
func createGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "commit", "--allow-empty", "-m", "root"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}
	return dir
}

// createGitRepoWithMergedHistory creates a repo with two root commits.
func createGitRepoWithMergedHistory(t *testing.T) string {
	t.Helper()
	repo1 := createGitRepo(t)
	repo2 := createGitRepo(t)
	for _, args := range [][]string{
		{"git", "remote", "add", "other", repo2},
		{"git", "fetch", "other"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repo1
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}
	// Merge — try main first, fall back to master
	cmd := exec.Command("git", "merge", "other/main", "--allow-unrelated-histories", "--no-edit")
	cmd.Dir = repo1
	if _, err := cmd.CombinedOutput(); err != nil {
		cmd2 := exec.Command("git", "merge", "other/master", "--allow-unrelated-histories", "--no-edit")
		cmd2.Dir = repo1
		if out, err := cmd2.CombinedOutput(); err != nil {
			t.Fatalf("merge: %s\n%s", err, out)
		}
	}
	return repo1
}
```
