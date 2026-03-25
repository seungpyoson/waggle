# Task 7: Live Smoke Tests

Manual verifications against the real waggle repo. These prove the acceptance criteria.

- [ ] **Step 1: Build waggle**

Run: `cd ~/Projects/Claude/waggle && go build -o waggle .`

- [ ] **Step 2: Existing single-directory workflow**

```bash
cd ~/Projects/Claude/waggle
./waggle stop 2>/dev/null || true
./waggle status
```

Expected: Broker starts (auto-start via git root commit cascade) and status shows running.

- [ ] **Step 3: Same repo, different subdirectory**

```bash
cd ~/Projects/Claude/waggle/internal
~/Projects/Claude/waggle/waggle status
```

Expected: Same broker PID as step 2.

- [ ] **Step 4: Worktree resolves to same broker**

```bash
cd ~/Projects/Claude/waggle
git worktree add /tmp/waggle-wt-test -b smoke-test-wt HEAD
cd /tmp/waggle-wt-test
~/Projects/Claude/waggle/waggle status
# Cleanup
cd ~/Projects/Claude/waggle
git worktree remove /tmp/waggle-wt-test
git branch -D smoke-test-wt
```

Expected: Same broker PID.

- [ ] **Step 5: WAGGLE_PROJECT_ID forces shared broker**

```bash
WAGGLE_PROJECT_ID=smoke-test ~/Projects/Claude/waggle/waggle status
cd /tmp && WAGGLE_PROJECT_ID=smoke-test ~/Projects/Claude/waggle/waggle status
```

Expected: Both connect to the same broker.

- [ ] **Step 6: Error with guidance in non-git dir**

```bash
cd /tmp
unset WAGGLE_PROJECT_ID
unset WAGGLE_ROOT
~/Projects/Claude/waggle/waggle status 2>&1
```

Expected: Error mentioning both `WAGGLE_PROJECT_ID` and `WAGGLE_ROOT`.

- [ ] **Step 7: Stop smoke test brokers**

```bash
cd ~/Projects/Claude/waggle && ./waggle stop
WAGGLE_PROJECT_ID=smoke-test ./waggle stop 2>/dev/null || true
```

- [ ] **Step 8: Commit any fixes from smoke testing**

If issues found, fix, re-run failing smoke test, commit.
