# Task 2: Implement ResolveProjectID

**Files:**
- Modify: `internal/config/config.go` (replace stub)

- [ ] **Step 1: Replace stub with full cascade implementation**

```go
// ResolveProjectID returns a stable project identifier using a 4-level cascade:
//  1. WAGGLE_PROJECT_ID env var (explicit override)
//  2. Git root commit hash (works across clones/worktrees)
//  3. WAGGLE_ROOT env var (non-git fallback, prefixed with "path:")
//  4. Error with guidance
//
// For git repos, resolves worktrees via "git rev-parse --git-common-dir" first.
// If multiple root commits exist (merged unrelated histories), sorts them
// lexicographically and uses the first for determinism.
func ResolveProjectID() (string, error) {
	// 1. Explicit override
	if id := os.Getenv("WAGGLE_PROJECT_ID"); id != "" {
		return id, nil
	}

	// 2. Git root commit (works across clones/worktrees)
	if id, err := gitRootCommit(); err == nil {
		return id, nil
	}

	// 3. WAGGLE_ROOT fallback (non-git projects)
	if root := os.Getenv("WAGGLE_ROOT"); root != "" {
		return "path:" + root, nil
	}

	// 4. Error with guidance
	return "", fmt.Errorf("cannot identify project: not in a git repo; set WAGGLE_PROJECT_ID or WAGGLE_ROOT")
}

// gitRootCommit resolves the project's root commit hash. For worktrees,
// it first resolves to the main repo via --git-common-dir. Returns the
// lexicographically first root commit for repos with merged unrelated histories.
func gitRootCommit() (string, error) {
	// Verify we're in a git repo (also resolves worktrees)
	if _, err := exec.Command("git", "rev-parse", "--git-common-dir").Output(); err != nil {
		return "", fmt.Errorf("not a git repo: %w", err)
	}

	// Get all root commits (commits with no parents)
	out, err := exec.Command("git", "rev-list", "--max-parents=0", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-list: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return "", fmt.Errorf("no root commits found")
	}

	// Sort for determinism when multiple root commits exist
	sort.Strings(lines)
	return lines[0], nil
}
```

Ensure imports include: `"os/exec"`, `"sort"`, `"strings"`.

- [ ] **Step 2: Run ResolveProjectID tests**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/config/ -v -run TestResolveProjectID -count=1`

Expected: All `TestResolveProjectID_*` tests PASS.

- [ ] **Step 3: Run all existing config tests — no regressions**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/config/ -v -count=1`

Expected: All tests PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go
python3 ~/.claude/lib/safe_git.py commit -m "feat: implement ResolveProjectID with 4-level cascade (#37)"
```
