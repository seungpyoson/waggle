# Task 5: Update CLI Callers

**Files:**
- Modify: `cmd/root.go:23-74`
- Modify: `cmd/start.go:25-111`

- [ ] **Step 1: Update cmd/root.go PersistentPreRunE**

Replace the identity resolution block (lines 29-70):

```go
PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
	if cmd.Name() == "start" {
		return nil
	}

	projectID, err := config.ResolveProjectID()
	if err != nil {
		return err
	}

	paths = config.NewPaths(projectID)

	if paths.DataDir == "" {
		return fmt.Errorf("cannot determine data paths: HOME not set")
	}

	if !broker.IsRunning(paths.PID) {
		if err := broker.CleanupStale(paths.PID, paths.Socket); err != nil {
			return fmt.Errorf("cleaning up stale files: %w", err)
		}

		socketDir := filepath.Dir(paths.Socket)
		if err := broker.EnsureDirs(paths.DataDir, socketDir); err != nil {
			return fmt.Errorf("creating directories: %w", err)
		}

		args := []string{os.Args[0], "start", "--foreground"}
		if err := broker.StartDaemon(paths.DataDir, socketDir, paths.Log, projectID, args); err != nil {
			return fmt.Errorf("starting broker daemon: %w", err)
		}

		if err := broker.WaitForReady(paths.PID, config.Defaults.StartupTimeout, config.Defaults.StartupPollInterval); err != nil {
			return fmt.Errorf("auto-start broker: %w", err)
		}
	}

	return nil
},
```

Remove `os.Getwd()` usage (no longer needed — `ResolveProjectID` handles cwd internally via git commands).

- [ ] **Step 2: Update cmd/start.go RunE**

Replace the identity resolution block (lines 26-37):

```go
RunE: func(cmd *cobra.Command, args []string) error {
	projectID, err := config.ResolveProjectID()
	if err != nil {
		return err
	}

	paths = config.NewPaths(projectID)

	if paths.DataDir == "" {
		return fmt.Errorf("cannot determine data paths: HOME not set")
	}

	if foreground {
		socketDir := filepath.Dir(paths.Socket)
		if err := broker.EnsureDirs(paths.DataDir, socketDir); err != nil {
			return fmt.Errorf("creating directories: %w", err)
		}

		b, err := broker.New(broker.Config{
			SocketPath: paths.Socket,
			DBPath:     paths.DB,
		})
		if err != nil {
			return fmt.Errorf("creating broker: %w", err)
		}

		if err := broker.WritePID(paths.PID); err != nil {
			return fmt.Errorf("writing PID file: %w", err)
		}

		if err := b.Serve(); err != nil {
			return fmt.Errorf("serving: %w", err)
		}

		broker.RemovePID(paths.PID)
		return nil
	}

	if broker.IsRunning(paths.PID) {
		pid, _ := broker.ReadPID(paths.PID)
		printJSON(map[string]any{
			"ok":      true,
			"message": fmt.Sprintf("broker already running (PID %d)", pid),
		})
		return nil
	}

	if err := broker.CleanupStale(paths.PID, paths.Socket); err != nil {
		return fmt.Errorf("cleaning up stale files: %w", err)
	}

	socketDir := filepath.Dir(paths.Socket)
	daemonArgs := []string{os.Args[0], "start", "--foreground"}
	if err := broker.StartDaemon(paths.DataDir, socketDir, paths.Log, projectID, daemonArgs); err != nil {
		return fmt.Errorf("starting daemon: %w", err)
	}

	if err := broker.WaitForReady(paths.PID, config.Defaults.StartupTimeout, config.Defaults.StartupPollInterval); err != nil {
		return fmt.Errorf("broker failed to start (check %s): %w", paths.Log, err)
	}

	pid, err := broker.ReadPID(paths.PID)
	if err != nil {
		return fmt.Errorf("broker started but cannot read PID: %w", err)
	}
	printJSON(map[string]any{
		"ok":      true,
		"message": fmt.Sprintf("broker started (PID %d)", pid),
	})
	return nil
},
```

- [ ] **Step 3: Compile check**

Run: `cd ~/Projects/Claude/waggle && go build .`

Expected: PASS.

- [ ] **Step 4: Run all unit tests**

Run: `cd ~/Projects/Claude/waggle && go test ./... -v -count=1 -short`

Expected: All tests PASS (skip E2E with -short).

- [ ] **Step 5: Commit**

```bash
git add cmd/root.go cmd/start.go
python3 ~/.claude/lib/safe_git.py commit -m "feat: switch CLI to ResolveProjectID + NewPaths(id) (#37)

Uses project ID cascade instead of filesystem root.
EnsureDirs uses paths.DataDir instead of paths.WaggleDir."
```
