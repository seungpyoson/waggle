# Task 6: Update E2E Test

**Files:**
- Modify: `e2e_test.go`

The E2E test creates a fake `.git` dir with no git history. Since `ResolveProjectID` now needs a real git repo (or env var), use `WAGGLE_PROJECT_ID` explicitly.

- [ ] **Step 1: Update E2E to use WAGGLE_PROJECT_ID**

In `e2e_test.go`:

1. Remove the fake `.git` creation (lines 29-33):
```go
// DELETE:
// Create fake project with .git
project := t.TempDir()
if err := os.Mkdir(filepath.Join(project, ".git"), 0755); err != nil {
    t.Fatalf("create .git: %v", err)
}
```

2. Replace with just a temp dir:
```go
project := t.TempDir()
```

3. Update startCmd env (line 48) to inject WAGGLE_PROJECT_ID:
```go
startCmd.Env = append(os.Environ(), "HOME="+tmpHome, "WAGGLE_PROJECT_ID=e2e-test-project")
```

Socket discovery (lines 64-77) stays the same — it scans `~/.waggle/sockets/`.

- [ ] **Step 2: Build and run E2E**

Run: `cd ~/Projects/Claude/waggle && go build -o waggle . && go test -v -run TestE2E -count=1`

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add e2e_test.go
python3 ~/.claude/lib/safe_git.py commit -m "test: update E2E to use WAGGLE_PROJECT_ID (#37)"
```
