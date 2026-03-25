# Task 1: TDD — ResolveProjectID Tests + Stub

**Files:**
- Modify: `internal/config/config_test.go`
- Modify: `internal/config/config.go` (stub only)

Write all failing tests first, then a minimal stub that compiles but fails.

- [ ] **Step 1: Add test helpers and failing tests to config_test.go**

Add test helpers from the main plan (chdir, createGitRepo, createGitRepoWithMergedHistory) plus these tests:

```go
func TestResolveProjectID_EnvOverride(t *testing.T) {
	t.Setenv("WAGGLE_PROJECT_ID", "custom-id-123")
	id, err := ResolveProjectID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "custom-id-123" {
		t.Fatalf("got %q, want %q", id, "custom-id-123")
	}
}

func TestResolveProjectID_GitRootCommit(t *testing.T) {
	t.Setenv("WAGGLE_PROJECT_ID", "")
	t.Setenv("WAGGLE_ROOT", "")
	repo := createGitRepo(t)
	chdir(t, repo)

	id, err := ResolveProjectID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(id) != 40 {
		t.Fatalf("expected 40-char SHA, got %q (len=%d)", id, len(id))
	}
	matched, _ := regexp.MatchString(`^[0-9a-f]{40}$`, id)
	if !matched {
		t.Fatalf("expected hex SHA, got %q", id)
	}
}

func TestResolveProjectID_GitWorktree(t *testing.T) {
	t.Setenv("WAGGLE_PROJECT_ID", "")
	t.Setenv("WAGGLE_ROOT", "")
	repo := createGitRepo(t)

	wt := filepath.Join(t.TempDir(), "worktree")
	cmd := exec.Command("git", "worktree", "add", wt, "-b", "test-branch")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %s\n%s", err, out)
	}
	t.Cleanup(func() {
		exec.Command("git", "-C", repo, "worktree", "remove", wt).Run()
	})

	chdir(t, repo)
	idMain, err := ResolveProjectID()
	if err != nil {
		t.Fatalf("main repo: %v", err)
	}

	chdir(t, wt)
	idWT, err := ResolveProjectID()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}

	if idMain != idWT {
		t.Fatalf("worktree ID %q != main repo ID %q", idWT, idMain)
	}
}

func TestResolveProjectID_MultipleRootCommits(t *testing.T) {
	t.Setenv("WAGGLE_PROJECT_ID", "")
	t.Setenv("WAGGLE_ROOT", "")
	repo := createGitRepoWithMergedHistory(t)
	chdir(t, repo)

	id, err := ResolveProjectID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(id) != 40 {
		t.Fatalf("expected 40-char SHA, got %q (len=%d)", id, len(id))
	}
	// Must be deterministic
	id2, _ := ResolveProjectID()
	if id != id2 {
		t.Fatalf("not deterministic: %q != %q", id, id2)
	}
}

func TestResolveProjectID_WaggleRootFallback(t *testing.T) {
	t.Setenv("WAGGLE_PROJECT_ID", "")
	t.Setenv("WAGGLE_ROOT", "/some/project/root")
	chdir(t, t.TempDir())

	id, err := ResolveProjectID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "path:/some/project/root" {
		t.Fatalf("got %q, want %q", id, "path:/some/project/root")
	}
}

func TestResolveProjectID_Error(t *testing.T) {
	t.Setenv("WAGGLE_PROJECT_ID", "")
	t.Setenv("WAGGLE_ROOT", "")
	chdir(t, t.TempDir())

	_, err := ResolveProjectID()
	if err == nil {
		t.Fatal("expected error when no git and no env vars")
	}
	if !strings.Contains(err.Error(), "WAGGLE_PROJECT_ID") || !strings.Contains(err.Error(), "WAGGLE_ROOT") {
		t.Fatalf("error should mention both env vars, got: %s", err.Error())
	}
}

func TestResolveProjectID_EmptyRepo(t *testing.T) {
	t.Setenv("WAGGLE_PROJECT_ID", "")
	t.Setenv("WAGGLE_ROOT", "")
	tmp := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = tmp
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s\n%s", err, out)
	}
	chdir(t, tmp)

	_, err := ResolveProjectID()
	if err == nil {
		t.Fatal("expected error for empty git repo with no env vars")
	}
}
```

- [ ] **Step 2: Add stub ResolveProjectID to config.go**

Add to imports: `"os/exec"`, `"sort"`, `"strings"`. Add after `FindProjectRoot`:

```go
func ResolveProjectID() (string, error) {
	return "", fmt.Errorf("not implemented")
}
```

- [ ] **Step 3: Run tests — verify they fail**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/config/ -v -run TestResolveProjectID -count=1`

Expected: All `TestResolveProjectID_*` tests FAIL with "not implemented".

- [ ] **Step 4: Commit failing tests + stub**

```bash
git add internal/config/config.go internal/config/config_test.go
python3 ~/.claude/lib/safe_git.py commit -m "test: add failing tests for ResolveProjectID cascade (#37)"
```
