# Last-Mile Push Delivery — Master Plan (v4)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Waggle messages appear automatically in every agent session across all 5 platforms.

**Architecture:** Daemon writes signal files on delivery. Shell hook in `.zshenv` checks signal files before every command (each agent tool call = new shell process, verified). PPID mapping links bootstrap to commands. Claude Code gets additional PreToolUse hook for clean `additionalContext` injection.

**Verified assumptions:**
- Each Bash tool call spawns a new shell (PIDs 50991→51092, PPID=36007 stable)
- `.zshenv` sources fresh each invocation (non-interactive zsh)
- PPID guard prevents overhead in non-agent shells (1 file stat = ns)

**Critical fix in v4 (from GPT-5.4 review):** `os.Getppid()` in bootstrap.go returns the intermediate SHELL pid, not the agent pid. Process tree: `agent→shell→waggle bootstrap`, so `os.Getppid()=shell`, but shell hook checks `$PPID=agent`. Fix: pass agent PID via env var `WAGGLE_AGENT_PPID=$PPID` when calling bootstrap. The `$PPID` in the calling shell IS the agent PID. Same fix for PreToolUse hook: `WAGGLE_PPID=$PPID node waggle-push.js`.

**Review history:** v1: Grok+Gemini. v2: revised. v3: setter pattern, markers, correct tests. v4: PPID generation fix (GPT-5.4 finding).

**Tasks 1-5 here. Tasks 6-9 in part2.**

---

## File Map

| File | Action | What |
|---|---|---|
| `internal/runtime/signal.go` | create | Write + atomic-consume signal files |
| `internal/runtime/signal_test.go` | create | Tests with race-condition coverage |
| `internal/config/config.go` | modify | SignalDirName, SignalMaxBytes, RuntimeSignalDir |
| `internal/runtime/manager.go` | modify | SetSignalDir + call WriteSignal in notifyRecord |
| `cmd/runtime_start.go` | modify | Call manager.SetSignalDir |
| `internal/adapter/bootstrap.go` | modify | WritePPIDMapping + call from Bootstrap |
| `internal/install/shell-hook/shell-hook.sh` | create | Shell hook (go:embed source) |
| `internal/install/shell_hook.go` | create | Installer for .zshenv/.bashrc |
| `internal/install/shell_hook_test.go` | create | Install/uninstall/idempotency tests |
| `integrations/claude-code/waggle-push.js` | modify | Read signal files via PPID mapping |
| `internal/install/claude_code.go` | modify | Copy+register PreToolUse hook |
| `internal/broker/router.go` | modify | Strip -push in presence, dedup |

---

### Task 1: Signal File Writer

**Files:** `internal/runtime/signal.go`, `internal/runtime/signal_test.go`, `internal/config/config.go`

- [ ] **Step 1: Config changes**

In `internal/config/config.go`, add to the Defaults struct definition (after `RuntimeLogFile string`):

```go
SignalDirName  string
SignalMaxBytes int64
```

Add to the Defaults literal (after `RuntimeLogFile`):

```go
SignalDirName:  "signals",
SignalMaxBytes: 65536,
```

Add to Paths struct (after `RuntimeStartLockDir string`):

```go
RuntimeSignalDir string
```

Add to NewPaths (after `RuntimeStartLockDir` assignment):

```go
RuntimeSignalDir: filepath.Join(runtimeDir, Defaults.SignalDirName),
```

- [ ] **Step 2: Write signal_test.go**

```go
package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteSignal_CreatesFileWithContent(t *testing.T) {
	dir := t.TempDir()
	if err := WriteSignal(dir, "agent-1", "alice", "hello", 65536); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "agent-1"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "alice") || !strings.Contains(s, "hello") {
		t.Fatalf("content = %q", s)
	}
}

func TestWriteSignal_Appends(t *testing.T) {
	dir := t.TempDir()
	WriteSignal(dir, "a", "alice", "one", 65536)
	WriteSignal(dir, "a", "bob", "two", 65536)
	data, _ := os.ReadFile(filepath.Join(dir, "a"))
	if c := strings.Count(string(data), "\n"); c != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", c, data)
	}
}

func TestWriteSignal_DropsWhenOverCap(t *testing.T) {
	dir := t.TempDir()
	// First write: long body to exceed small cap
	WriteSignal(dir, "a", "alice", strings.Repeat("x", 100), 50)
	// File is now ~133 bytes > 50 cap
	WriteSignal(dir, "a", "bob", "should-be-dropped", 50)
	data, _ := os.ReadFile(filepath.Join(dir, "a"))
	if strings.Contains(string(data), "bob") {
		t.Fatalf("second write should be dropped, got: %q", data)
	}
}

func TestWriteSignal_DirPermissions(t *testing.T) {
	sigDir := filepath.Join(t.TempDir(), "signals")
	WriteSignal(sigDir, "a", "alice", "test", 65536)
	info, err := os.Stat(sigDir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("dir perm = %o, want 700", perm)
	}
}

func TestConsumeSignal_AtomicReadAndDelete(t *testing.T) {
	dir := t.TempDir()
	WriteSignal(dir, "a", "alice", "hello", 65536)
	content, err := ConsumeSignal(dir, "a")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "alice") {
		t.Fatalf("content = %q", content)
	}
	// Original gone
	if _, err := os.Stat(filepath.Join(dir, "a")); !os.IsNotExist(err) {
		t.Fatal("original should be deleted")
	}
	// No temp files left
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "consuming") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestConsumeSignal_NoFile_Empty(t *testing.T) {
	content, err := ConsumeSignal(t.TempDir(), "nope")
	if err != nil || content != "" {
		t.Fatalf("expected empty, got %q err=%v", content, err)
	}
}

func TestConsumeSignal_NewWriteDuringConsume(t *testing.T) {
	dir := t.TempDir()
	WriteSignal(dir, "a", "alice", "first", 65536)
	// Simulate atomic rename (what ConsumeSignal does internally)
	orig := filepath.Join(dir, "a")
	tmp := orig + ".consuming-test"
	os.Rename(orig, tmp)
	// Daemon writes new message to original path AFTER rename
	WriteSignal(dir, "a", "bob", "second", 65536)
	// Consumed content has first message only
	data, _ := os.ReadFile(tmp)
	os.Remove(tmp)
	if !strings.Contains(string(data), "alice") || strings.Contains(string(data), "bob") {
		t.Fatalf("consumed = %q", data)
	}
	// New message survives at original path
	data2, _ := os.ReadFile(orig)
	if !strings.Contains(string(data2), "bob") {
		t.Fatalf("new message lost, orig = %q", data2)
	}
}
```

- [ ] **Step 3: Run tests — expect FAIL**

Run: `go test ./internal/runtime/ -run "TestWriteSignal|TestConsumeSignal" -v`

- [ ] **Step 4: Implement signal.go**

```go
package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WriteSignal appends a formatted message to the agent's signal file.
// Drops the write silently if the file already exceeds maxBytes.
func WriteSignal(signalDir, agentName, fromName, body string, maxBytes int64) error {
	if err := os.MkdirAll(signalDir, 0o700); err != nil {
		return fmt.Errorf("create signal dir: %w", err)
	}
	path := filepath.Join(signalDir, agentName)
	if maxBytes > 0 {
		if info, err := os.Stat(path); err == nil && info.Size() >= maxBytes {
			return nil
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open signal: %w", err)
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\xf0\x9f\x93\xa8 waggle message from %s: %s\n", fromName, body)
	return err
}

// ConsumeSignal atomically reads and removes the signal file.
// Rename-then-read ensures no data loss if the daemon writes during consume.
func ConsumeSignal(signalDir, agentName string) (string, error) {
	path := filepath.Join(signalDir, agentName)
	tmp := fmt.Sprintf("%s.consuming-%d", path, time.Now().UnixNano())
	if err := os.Rename(path, tmp); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	data, err := os.ReadFile(tmp)
	os.Remove(tmp)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// PruneStaleFiles removes files matching prefix older than maxAge.
func PruneStaleFiles(dir, prefix string, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
```

Note: `\xf0\x9f\x93\xa8` is the raw UTF-8 bytes for the 📨 emoji.

- [ ] **Step 5: Run tests — expect PASS**

Run: `go test ./internal/runtime/ -run "TestWriteSignal|TestConsumeSignal" -v`

- [ ] **Step 6: Commit**

```bash
git add internal/runtime/signal.go internal/runtime/signal_test.go internal/config/config.go
git commit -m "feat: signal file writer with atomic consumption and size cap"
```

---

### Task 2: Wire into Delivery Pipeline

**Files:** `internal/runtime/manager.go`, `cmd/runtime_start.go`

- [ ] **Step 1: Add signalDir field and setter to Manager**

In `internal/runtime/manager.go`, add field to Manager struct:

```go
signalDir string // set via SetSignalDir; enables shell-hook signal files
```

Add setter method (after NewManager):

```go
// SetSignalDir enables signal file writing for shell-hook delivery.
func (m *Manager) SetSignalDir(dir string) {
	m.signalDir = dir
}
```

- [ ] **Step 2: Add signal writing to notifyRecord**

In `notifyRecord` (line 466), after the OS notification block and before `store.MarkNotified`, add:

```go
if m.signalDir != "" {
	_ = WriteSignal(m.signalDir, agentName, senderFromTitle(title), body, config.Defaults.SignalMaxBytes)
}
```

Add helper (after `notificationTitle`):

```go
func senderFromTitle(title string) string {
	const prefix = "Message from "
	if strings.HasPrefix(title, prefix) {
		return strings.TrimPrefix(title, prefix)
	}
	return "unknown"
}
```

- [ ] **Step 3: Add stale PPID cleanup to maintenance loop**

In `runMaintenanceLoop`, inside the ticker case, after `PruneDeliveryRecords`:

```go
if m.signalDir != "" {
	PruneStaleFiles(filepath.Dir(m.signalDir), "agent-ppid-", 24*time.Hour)
}
```

Add `"path/filepath"` to imports if not already present (check: it's not in manager.go imports currently, but `strings` and `time` are).

- [ ] **Step 4: Set signal dir in daemon startup**

In `cmd/runtime_start.go`, after `rt.NewManager(store, rt.NewBrokerListenerFactory(), rt.NewCommandNotifier())` (line 44), add:

```go
manager.SetSignalDir(paths.RuntimeSignalDir)
```

Ensure `paths` is resolved before this line. Check the existing code to find where paths are computed.

- [ ] **Step 5: Run all tests**

Run: `go test ./internal/runtime/ -v && go test ./cmd/ -v`
Expected: PASS — no test breakage since NewManager signature unchanged.

- [ ] **Step 6: Commit**

```bash
git add internal/runtime/manager.go cmd/runtime_start.go
git commit -m "feat: wire signal files into delivery pipeline via setter"
```

---

### Task 3: PPID Mapping (v4 — env var fix)

**Files:** `internal/adapter/bootstrap.go`, `integrations/claude-code/hook.sh`, all adapter blocks

**Critical context:** `os.Getppid()` returns the intermediate shell PID, NOT the agent PID. Process tree: `agent(X) → shell(Y) → waggle bootstrap(Z)`. `os.Getppid()=Y`, but shell hook checks `$PPID=X`. Fix: callers pass agent PID via `WAGGLE_AGENT_PPID=$PPID`.

- [ ] **Step 1: Add WritePPIDMapping with env var override**

Add to `internal/adapter/bootstrap.go` (add `"strconv"` to imports):

```go
// WritePPIDMapping writes agent name keyed by PID for shell hook discovery.
func WritePPIDMapping(runtimeDir string, ppid int, agentName string) error {
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(
		filepath.Join(runtimeDir, fmt.Sprintf("agent-ppid-%d", ppid)),
		[]byte(agentName+"\n"),
		0o600,
	)
}

// resolveAgentPPID returns the agent process PID for PPID mapping.
// Prefers WAGGLE_AGENT_PPID env var (set by callers who know the real agent PID)
// over os.Getppid() (which returns the intermediate shell, not the agent).
func resolveAgentPPID() int {
	if ep := os.Getenv("WAGGLE_AGENT_PPID"); ep != "" {
		if p, err := strconv.Atoi(ep); err == nil && p > 0 {
			return p
		}
	}
	return os.Getppid()
}
```

- [ ] **Step 2: Call from Bootstrap function**

After watch registration, before return:

```go
_ = WritePPIDMapping(runtimePaths.RuntimeDir, resolveAgentPPID(), result.AgentName)
```

- [ ] **Step 3: Update hook.sh to pass WAGGLE_AGENT_PPID**

In `integrations/claude-code/hook.sh`, change the bootstrap call to:

```bash
OUTPUT=$($TIMEOUT_CMD 3 env WAGGLE_AGENT_PPID=$PPID waggle adapter bootstrap claude-code --format markdown 2>/dev/null) || OUTPUT=""
```

`$PPID` in hook.sh = Claude Code PID (the hook's parent). This passes through to bootstrap.go.

- [ ] **Step 4: Update all adapter instruction blocks**

Each adapter's instruction must include `WAGGLE_AGENT_PPID=$PPID`:

`integrations/codex/AGENTS-block.md`:
```
WAGGLE_AGENT_PPID=$PPID waggle adapter bootstrap codex --format markdown
```

Same for `gemini/GEMINI-block.md`, `auggie/RULE-block.md`, `augment/SKILL-block.md`.

- [ ] **Step 5: Write test**

Add to `internal/adapter/bootstrap_test.go`:

```go
func TestWritePPIDMapping(t *testing.T) {
	dir := t.TempDir()
	if err := WritePPIDMapping(dir, 12345, "claude-99"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "agent-ppid-12345"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != "claude-99" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveAgentPPID_PrefersEnvVar(t *testing.T) {
	t.Setenv("WAGGLE_AGENT_PPID", "99999")
	got := resolveAgentPPID()
	if got != 99999 {
		t.Fatalf("got %d, want 99999", got)
	}
}

func TestResolveAgentPPID_FallsBackToGetppid(t *testing.T) {
	t.Setenv("WAGGLE_AGENT_PPID", "")
	got := resolveAgentPPID()
	if got != os.Getppid() {
		t.Fatalf("got %d, want %d", got, os.Getppid())
	}
}
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/adapter/ -run "TestWritePPIDMapping|TestResolveAgentPPID" -v`

- [ ] **Step 7: Commit**

```bash
git add internal/adapter/bootstrap.go integrations/
git commit -m "feat: PPID mapping via WAGGLE_AGENT_PPID env var (fixes generation mismatch)"
```

---

### Task 4: Shell Hook Script

**Files:** `internal/install/shell-hook/shell-hook.sh`

- [ ] **Step 1: Create directory and script**

```bash
mkdir -p internal/install/shell-hook
```

Create `internal/install/shell-hook/shell-hook.sh`:

```bash
# waggle shell-hook — surfaces messages on every agent command.
# Sourced from .zshenv/.bashrc. Each agent tool call = new shell = fresh source.
# Cost when no messages: 1 file stat (~ns). Guarded by PPID mapping.
__waggle_check() {
    local _wd="$HOME/.waggle/runtime"
    local _wm="$_wd/agent-ppid-$PPID"
    [ -f "$_wm" ] || return 0
    local _wa
    read -r _wa < "$_wm" 2>/dev/null || return 0
    [ -n "$_wa" ] || return 0
    local _ws="$_wd/signals/$_wa"
    [ -f "$_ws" ] || return 0
    # Atomic: rename then read. If daemon writes after mv, new file at original path.
    local _wt="$_ws.c-$$"
    mv "$_ws" "$_wt" 2>/dev/null || return 0
    cat "$_wt" >&2 2>/dev/null
    rm -f "$_wt" 2>/dev/null
}
__waggle_check
```

- [ ] **Step 2: Commit**

```bash
git add internal/install/shell-hook/shell-hook.sh
git commit -m "feat: shell hook script for universal message delivery"
```

---

### Task 5: Shell Hook Installer

**Files:** `internal/install/shell_hook.go`, `internal/install/shell_hook_test.go`

Continued in `2026-03-31-last-mile-push-delivery-part2.md`, Task 5 (full test code + implementation).
