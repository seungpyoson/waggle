# Zombie Broker Timeout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate indefinite CLI hangs caused by zombie brokers by adding connect timeouts and a health probe that triggers auto-recovery.

**Architecture:** Three layers: (1) `client.Connect()` uses `net.DialTimeout` so no dial blocks forever, (2) `connectToBroker()` scopes a deadline around the handshake and clears it for streaming, (3) `broker.IsResponding()` does a send+read probe to detect zombies before commands connect, triggering auto-restart.

**Tech Stack:** Go stdlib (`net`, `time`, `bufio`), Cobra CLI, Unix domain sockets

**Spec:** `docs/superpowers/specs/2026-03-26-zombie-broker-timeout-design.md`

---

## File Structure

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/config/config.go` | Modify | Add `ConnectTimeout` (5s) and `HealthCheckTimeout` (1s) to Defaults |
| `internal/client/client.go` | Modify | Add timeout param to `Connect()`, add `ClearDeadline()` |
| `internal/client/client_test.go` | Modify | Timeout tests (zombie socket, healthy socket) |
| `internal/broker/lifecycle.go` | Modify | Add `IsResponding()` function |
| `internal/broker/lifecycle_test.go` | Modify | IsResponding unit tests (zombie, healthy, missing) |
| `cmd/root.go` | Modify | Deadline-scoped handshake, zombie recovery flow, `autoStartBroker()` helper |
| `e2e_zombie_test.go` | Create | Zombie recovery E2E, healthy broker, --help regression guard |
| `help_test.go` | Delete | Replaced by `e2e_zombie_test.go` |

---

### Task 1: Config Defaults

**Files:** Modify `internal/config/config.go:56-136`

- [ ] **Step 1:** Add to Defaults struct (after `SpawnKillPollInterval`):
```go
ConnectTimeout     time.Duration
HealthCheckTimeout time.Duration
```
And values (after `AgentConfigFile`):
```go
ConnectTimeout:     5 * time.Second,
HealthCheckTimeout: 1 * time.Second,
```

- [ ] **Step 2:** Run: `go test ./internal/config/ -v -count=1` — PASS (reflection validation covers new fields)

- [ ] **Step 3:** Commit: `feat(config): add ConnectTimeout and HealthCheckTimeout defaults (#65)`

---

### Task 2: client.Connect() with Timeout

**Files:** Modify `internal/client/client.go:21-36`, `internal/client/client_test.go`

- [ ] **Step 1: Write failing test — timeout on zombie socket**
```go
func TestConnect_TimeoutOnZombieSocket(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "zombie.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil { t.Fatal(err) }
	defer ln.Close()

	timeout := 500 * time.Millisecond
	start := time.Now()
	_, err = Connect(sockPath, timeout)
	elapsed := time.Since(start)

	if err == nil { t.Fatal("expected timeout error") }
	if elapsed > 2*timeout { t.Errorf("took %v, expected ~%v", elapsed, timeout) }
}
```

- [ ] **Step 2:** Run: `go test ./internal/client/ -v -run TestConnect_TimeoutOnZombieSocket -count=1` — FAIL (signature mismatch)

- [ ] **Step 3: Write failing test — success with timeout**
```go
func TestConnect_SuccessWithTimeout(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "healthy.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil { t.Fatal(err) }
	defer ln.Close()
	go func() {
		conn, _ := ln.Accept()
		if conn != nil { defer conn.Close(); conn.Write([]byte("{\"ok\":true}\n")) }
	}()

	c, err := Connect(sockPath, 5*time.Second)
	if err != nil { t.Fatalf("expected success: %v", err) }
	c.Close()
}
```

- [ ] **Step 4: Implement — change Connect signature**
```go
func Connect(socketPath string, timeout time.Duration) (*Client, error) {
	conn, err := net.DialTimeout("unix", socketPath, timeout)
	if err != nil { return nil, fmt.Errorf("connect to broker: %w", err) }
	scanner := bufio.NewScanner(conn)
	bufSize := int(config.Defaults.MaxMessageSize)
	scanner.Buffer(make([]byte, bufSize), bufSize)
	return &Client{conn: conn, scanner: scanner}, nil
}
```

- [ ] **Step 5: Add ClearDeadline method**
```go
func (c *Client) ClearDeadline() error {
	return c.conn.SetDeadline(time.Time{})
}
```

- [ ] **Step 6: Fix caller** — `cmd/root.go:124`: `client.Connect(paths.Socket, config.Defaults.ConnectTimeout)`

- [ ] **Step 7:** Run: `go test ./internal/client/ -v -run TestConnect_ -count=1` — PASS

- [ ] **Step 8:** Commit: `feat(client): add dial timeout to Connect (#65)`

---

### Task 3: broker.IsResponding() — Zombie Detection

**Files:** Modify `internal/broker/lifecycle.go` (after line 56), `internal/broker/lifecycle_test.go`

- [ ] **Step 1: Write failing test — zombie returns false**
```go
func TestIsResponding_ZombieSocket(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "zombie.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil { t.Fatal(err) }
	defer ln.Close()

	start := time.Now()
	result := IsResponding(sockPath, 500*time.Millisecond)
	elapsed := time.Since(start)

	if result { t.Error("expected false for zombie") }
	if elapsed > 2*time.Second { t.Errorf("took %v, expected ~500ms", elapsed) }
}
```

- [ ] **Step 2:** Run: `go test ./internal/broker/ -v -run TestIsResponding_Zombie -count=1` — FAIL (undefined)

- [ ] **Step 3: Write failing test — healthy broker returns true**
```go
func TestIsResponding_HealthyBroker(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := fmt.Sprintf("/tmp/waggle-responding-test-%d.sock", time.Now().UnixNano())
	dbPath := filepath.Join(tmpDir, "state.db")
	defer os.Remove(sockPath)

	b, err := New(Config{SocketPath: sockPath, DBPath: dbPath})
	if err != nil { t.Fatal(err) }
	go b.Serve()
	defer b.Shutdown()
	time.Sleep(100 * time.Millisecond)

	if !IsResponding(sockPath, 1*time.Second) { t.Error("expected true") }
}
```

- [ ] **Step 4: Write failing test — missing socket returns false**
```go
func TestIsResponding_MissingSocket(t *testing.T) {
	if IsResponding("/tmp/nonexistent-waggle.sock", 500*time.Millisecond) {
		t.Error("expected false for missing socket")
	}
}
```

- [ ] **Step 5: Implement IsResponding** — add to `lifecycle.go` with imports `"bufio"`, `"encoding/json"`, `"net"`:
```go
func IsResponding(socketPath string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("unix", socketPath, timeout)
	if err != nil { return false }
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))
	req := struct{ Cmd string `json:"cmd"` }{Cmd: "status"}
	data, _ := json.Marshal(req)
	if _, err := conn.Write(append(data, '\n')); err != nil { return false }
	scanner := bufio.NewScanner(conn)
	return scanner.Scan()
}
```

- [ ] **Step 6:** Run: `go test ./internal/broker/ -v -run TestIsResponding -count=1` — all 3 PASS

- [ ] **Step 7:** Commit: `feat(broker): add IsResponding zombie detection probe (#65)`

---

### Task 4: connectToBroker() — Deadline-Scoped Handshake

**Files:** Modify `cmd/root.go:119-144`

- [ ] **Step 1: Replace connectToBroker with deadline-scoped version**
```go
func connectToBroker(name string) (*client.Client, error) {
	if name == "" { name = "cli-" + strconv.Itoa(os.Getpid()) }

	c, err := client.Connect(paths.Socket, config.Defaults.ConnectTimeout)
	if err != nil { return nil, err }

	if err := c.SetDeadline(config.Defaults.ConnectTimeout); err != nil {
		c.Close(); return nil, fmt.Errorf("set handshake deadline: %w", err)
	}
	resp, err := c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: name})
	if err != nil {
		c.Close(); cleanupStaleFiles(); return nil, err
	}
	if !resp.OK {
		c.Close(); return nil, fmt.Errorf("%s: %s", resp.Code, resp.Error)
	}
	if err := c.ClearDeadline(); err != nil {
		c.Close(); return nil, fmt.Errorf("clear deadline: %w", err)
	}
	return c, nil
}

func cleanupStaleFiles() {
	os.Remove(paths.Socket)
	os.Remove(paths.PID)
}
```

- [ ] **Step 2:** Run: `go build ./...` — Success

- [ ] **Step 3:** Run: `go test ./... -count=1 -short` — PASS

- [ ] **Step 4:** Commit: `feat(cmd): deadline-scoped handshake in connectToBroker (#65)`

---

### Task 5: PersistentPreRunE — Zombie Recovery Flow

**Files:** Modify `cmd/root.go:24-79`

- [ ] **Step 1: Replace auto-start logic with zombie-aware flow**

The new `PersistentPreRunE` checks `IsRunning` → `IsResponding` → cleanup → auto-start. Extract `autoStartBroker()` helper. See spec section 5 for the flow diagram.

Key changes:
- After `brokerIndependent` check and path resolution
- If `IsRunning(PID)` is true, call `IsResponding(socket, HealthCheckTimeout)`
- If not responding: warn to stderr, remove socket+PID, set `needsStart = true`
- If not running: set `needsStart = true`
- If `needsStart`: call `autoStartBroker()` (extracted from existing code)

`autoStartBroker()` contains the existing `CleanupStale` → `EnsureDirs` → `StartDaemon` → `WaitForReady` logic. `CleanupStale` error is ignored (files may already be cleaned).

- [ ] **Step 2:** Run: `go build ./...` — Success

- [ ] **Step 3:** Run: `go test ./... -count=1 -short` — PASS

- [ ] **Step 4:** Commit: `feat(cmd): zombie detection and auto-recovery in PersistentPreRunE (#65)`

---

### Task 6: E2E Tests

**Files:** Create `e2e_zombie_test.go`, delete `help_test.go`

- [ ] **Step 1: Create e2e_zombie_test.go** with tests:
- `TestE2E_ZombieAutoRecovery` — zombie broker + `waggle sessions` → detects zombie, starts fresh, returns JSON, completes <5s
- `TestE2E_ZombieFailFast_NoAutoStart` — zombie + `--no-auto-start status` → fails within ConnectTimeout, not hang
- `TestE2E_HealthyBrokerUnaffected` — real broker + `waggle sessions` → returns data <2s, no warnings
- `TestE2E_HelpFromNonGitDir` — all subcommands' --help from /tmp with fake HOME → exit 0

Helper: `createZombieBroker(t, tmpHome, projectID)` — builds binary, resolves socket path, creates zombie listener + PID file.

- [ ] **Step 2: Delete help_test.go** (replaced by e2e_zombie_test.go)

- [ ] **Step 3:** Run: `go test -v -run "TestE2E_(Zombie|Healthy|Help)" -count=1 -timeout 120s` — PASS

- [ ] **Step 4:** Commit: `test: add zombie recovery and regression E2E tests (#65)`

---

### Task 7: Final Verification

- [ ] **Step 1:** Run: `go test ./... -count=1 -timeout 300s` — All PASS

- [ ] **Step 2: Manual smoke test** — build binary, create zombie with Python, run `waggle sessions` → zombie warning + valid JSON. Run `waggle listen --help` from `/tmp` → help text, exit 0.

- [ ] **Step 3:** Run `/audit --branch` to review all changes as a coordinated set.

- [ ] **Step 4:** Commit any audit fixes: `fix: address audit findings (#65)`
