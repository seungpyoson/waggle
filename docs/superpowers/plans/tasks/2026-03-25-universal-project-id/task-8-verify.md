# Task 8: Final Verification

- [ ] **Step 1: Full test suite**

Run: `cd ~/Projects/Claude/waggle && go test ./... -v -count=1`

Expected: ALL tests pass including E2E.

- [ ] **Step 2: Verify clean diff**

Run: `cd ~/Projects/Claude/waggle && git log --oneline main..HEAD && git diff --stat main..HEAD`

Expected: Clean commit history, changes in: config.go, config_test.go, lifecycle.go, root.go, start.go, e2e_test.go, spec, plan docs.

- [ ] **Step 3: Build final binary**

Run: `cd ~/Projects/Claude/waggle && go build -o waggle .`

Expected: Clean build.
