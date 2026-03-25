# Task 4: StartDaemon Explicit ID Injection

**Files:**
- Modify: `internal/broker/lifecycle.go:137`

- [ ] **Step 1: Update StartDaemon signature and inject WAGGLE_PROJECT_ID**

Change first param from `waggleDir` to `dataDir`, add `projectID` param, inject env:

```go
// StartDaemon forks the broker as a background process.
// It injects WAGGLE_PROJECT_ID into the forked process env so the daemon
// resolves the same project identity as the parent — no re-resolution race.
func StartDaemon(dataDir, socketDir, logFile, projectID string, args []string) error {
	if err := EnsureDirs(dataDir, socketDir); err != nil {
		return err
	}

	log, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("opening log file: %w", err)
	}
	defer log.Close()

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("getting executable path: %w", err)
	}

	// Inject WAGGLE_PROJECT_ID so daemon doesn't re-resolve
	env := append(os.Environ(), "WAGGLE_PROJECT_ID="+projectID)

	procAttr := &os.ProcAttr{
		Files: []*os.File{nil, log, log},
		Dir:   "",
		Env:   env,
	}

	process, err := os.StartProcess(exe, args, procAttr)
	if err != nil {
		return fmt.Errorf("starting daemon: %w", err)
	}

	if err := process.Release(); err != nil {
		return fmt.Errorf("releasing daemon process: %w", err)
	}

	return nil
}
```

- [ ] **Step 2: Verify broker package compiles**

Run: `cd ~/Projects/Claude/waggle && go build ./internal/broker/`

Expected: PASS (callers in cmd/ will break — expected, fixed in Task 5).

- [ ] **Step 3: Commit**

```bash
git add internal/broker/lifecycle.go
python3 ~/.claude/lib/safe_git.py commit -m "feat: StartDaemon injects WAGGLE_PROJECT_ID into forked env (#37)"
```
