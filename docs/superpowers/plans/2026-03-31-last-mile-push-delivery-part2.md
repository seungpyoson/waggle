# Last-Mile Push Delivery — Tasks 5-9 (v4 master)

Continuation of `2026-03-31-last-mile-push-delivery.md`.

---

### Task 5: Shell Hook Installer (continued)

**Files:** `internal/install/shell_hook.go`, `internal/install/shell_hook_test.go`

- [ ] **Step 1: Write tests**

```go
package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallShellHook_WritesScript(t *testing.T) {
	home := t.TempDir()
	if err := installShellHook(home); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".waggle", "shell-hook.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "__waggle_check") {
		t.Fatal("missing function")
	}
}

func TestInstallShellHook_ZshenvWithMarkers(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, ".zshenv"), []byte("# existing\n"), 0o644)
	installShellHook(home)
	data, _ := os.ReadFile(filepath.Join(home, ".zshenv"))
	s := string(data)
	if !strings.Contains(s, "WAGGLE-SHELL-HOOK-BEGIN") {
		t.Fatal("missing begin marker")
	}
	if !strings.Contains(s, "WAGGLE-SHELL-HOOK-END") {
		t.Fatal("missing end marker")
	}
	if !strings.Contains(s, "shell-hook.sh") {
		t.Fatal("missing source line")
	}
	if !strings.Contains(s, "# existing") {
		t.Fatal("lost existing content")
	}
}

func TestInstallShellHook_BashrcWithMarkers(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, ".bashrc"), []byte("# bash\n"), 0o644)
	installShellHook(home)
	data, _ := os.ReadFile(filepath.Join(home, ".bashrc"))
	if !strings.Contains(string(data), "WAGGLE-SHELL-HOOK-BEGIN") {
		t.Fatal("missing marker in .bashrc")
	}
}

func TestInstallShellHook_Idempotent(t *testing.T) {
	home := t.TempDir()
	installShellHook(home)
	installShellHook(home)
	data, _ := os.ReadFile(filepath.Join(home, ".zshenv"))
	if strings.Count(string(data), "WAGGLE-SHELL-HOOK-BEGIN") != 1 {
		t.Fatal("marker duplicated")
	}
}

func TestUninstallShellHook_RemovesBlock(t *testing.T) {
	home := t.TempDir()
	installShellHook(home)
	uninstallShellHook(home)
	for _, f := range []string{".zshenv", ".bashrc"} {
		data, _ := os.ReadFile(filepath.Join(home, f))
		if strings.Contains(string(data), "waggle") {
			t.Fatalf("%s still has waggle content", f)
		}
	}
}

func TestUninstallShellHook_PreservesOtherContent(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, ".zshenv"), []byte("# keep this\n"), 0o644)
	installShellHook(home)
	uninstallShellHook(home)
	data, _ := os.ReadFile(filepath.Join(home, ".zshenv"))
	if !strings.Contains(string(data), "# keep this") {
		t.Fatal("lost existing content")
	}
}
```

- [ ] **Step 2: Run tests — expect FAIL**

Run: `go test ./internal/install/ -run TestInstallShellHook -v`

- [ ] **Step 3: Implement shell_hook.go**

```go
package install

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed shell-hook/shell-hook.sh
var shellHookFS embed.FS

const (
	shellHookBegin = "# <!-- WAGGLE-SHELL-HOOK-BEGIN -->"
	shellHookEnd   = "# <!-- WAGGLE-SHELL-HOOK-END -->"
)

var shellHookBlock = strings.Join([]string{
	shellHookBegin,
	`[ -f "$HOME/.waggle/shell-hook.sh" ] && source "$HOME/.waggle/shell-hook.sh"`,
	`export BASH_ENV="$HOME/.waggle/shell-hook.sh"`,
	shellHookEnd,
}, "\n")

// InstallShellHook installs the universal shell hook.
func InstallShellHook() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return installShellHook(home)
}

// UninstallShellHook removes the shell hook.
func UninstallShellHook() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return uninstallShellHook(home)
}

func installShellHook(homeDir string) error {
	waggleDir := filepath.Join(homeDir, ".waggle")
	if err := os.MkdirAll(waggleDir, 0o700); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	hookData, err := shellHookFS.ReadFile("shell-hook/shell-hook.sh")
	if err != nil {
		return fmt.Errorf("read embedded hook: %w", err)
	}
	if err := os.WriteFile(filepath.Join(waggleDir, "shell-hook.sh"), hookData, 0o644); err != nil {
		return fmt.Errorf("write hook: %w", err)
	}
	for _, rc := range []string{".zshenv", ".bashrc"} {
		if err := upsertShellHookBlock(filepath.Join(homeDir, rc)); err != nil {
			return fmt.Errorf("update %s: %w", rc, err)
		}
	}
	return nil
}

func uninstallShellHook(homeDir string) error {
	for _, rc := range []string{".zshenv", ".bashrc"} {
		removeShellHookBlock(filepath.Join(homeDir, rc))
	}
	os.Remove(filepath.Join(homeDir, ".waggle", "shell-hook.sh"))
	return nil
}

func upsertShellHookBlock(path string) error {
	existing, _ := os.ReadFile(path)
	content := string(existing)
	if strings.Contains(content, shellHookBegin) {
		return nil // already present
	}
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += shellHookBlock + "\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

func removeShellHookBlock(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	var filtered []string
	inBlock := false
	for _, line := range lines {
		if strings.Contains(line, "WAGGLE-SHELL-HOOK-BEGIN") {
			inBlock = true
			continue
		}
		if strings.Contains(line, "WAGGLE-SHELL-HOOK-END") {
			inBlock = false
			continue
		}
		if !inBlock {
			filtered = append(filtered, line)
		}
	}
	os.WriteFile(path, []byte(strings.Join(filtered, "\n")), 0o644)
}
```

- [ ] **Step 4: Wire into platform installers**

In each platform's install function (`installClaudeCode`, `installCodex`, `installGemini`, `installAugment`, `installAuggie`), add as the final step:

```go
if err := installShellHook(homeDir); err != nil {
	return fmt.Errorf("installing shell hook: %w", err)
}
```

- [ ] **Step 5: Run tests — expect PASS**

Run: `go test ./internal/install/ -run "TestInstallShellHook|TestUninstallShellHook" -v`

- [ ] **Step 6: Commit**

```bash
git add internal/install/shell_hook.go internal/install/shell_hook_test.go
git commit -m "feat: marker-based shell hook installer"
```

---

### Task 6: Claude Code PreToolUse Hook (v4 — PPID env var fix)

**Files:** `integrations/claude-code/waggle-push.js`, `internal/install/claude_code.go`

**Critical context:** Like bootstrap.go, the Node hook's `process.ppid` returns the intermediate shell PID, not Claude Code's PID. Fix: hook command uses `WAGGLE_PPID=$PPID` prefix so `$PPID` (= Claude Code PID) is passed via env var.

- [ ] **Step 1: Rewrite waggle-push.js**

Replace contents of `integrations/claude-code/waggle-push.js`:

```javascript
#!/usr/bin/env node
// waggle-push.js — PreToolUse hook for Claude Code.
// Reads signal files via PPID mapping. Atomic consume via rename.
// WAGGLE_PPID env var provides the real agent PID (set by hook command).
const fs = require('fs');
const path = require('path');

const home = process.env.HOME;
if (!home) process.exit(0);

const rtDir = path.join(home, '.waggle', 'runtime');
// Use WAGGLE_PPID (agent PID) not process.ppid (intermediate shell PID)
const ppid = process.env.WAGGLE_PPID || String(process.ppid);
const mapFile = path.join(rtDir, 'agent-ppid-' + ppid);

try {
    if (!fs.existsSync(mapFile)) process.exit(0);

    const agent = fs.readFileSync(mapFile, 'utf8').trim().split('\n')[0];
    if (!agent) process.exit(0);

    const sigFile = path.join(rtDir, 'signals', agent);
    if (!fs.existsSync(sigFile)) process.exit(0);

    // Atomic: rename then read (daemon writes to original path are safe)
    const tmpFile = sigFile + '.c-' + process.pid;
    try { fs.renameSync(sigFile, tmpFile); } catch { process.exit(0); }

    const content = fs.readFileSync(tmpFile, 'utf8').trim();
    try { fs.unlinkSync(tmpFile); } catch {}

    if (!content) process.exit(0);

    console.log(JSON.stringify({
        additionalContext: '\n' + content +
            '\nRespond to waggle messages using: ' +
            'WAGGLE_AGENT_NAME="' + agent + '" waggle send <sender> "<reply>"\n'
    }));
} catch {
    process.exit(0);
}
```

- [ ] **Step 2: Copy to go:embed location**

```bash
cp integrations/claude-code/waggle-push.js internal/install/claude-code/waggle-push.js
```

- [ ] **Step 3: Add PreToolUse registration to claude_code.go**

In `internal/install/claude_code.go`, add constant — note `WAGGLE_PPID=$PPID` prefix:

```go
const wagglePushCommand = "WAGGLE_PPID=$PPID node $HOME/.claude/hooks/waggle-push.js"
```

Add registration function:

```go
func registerPreToolUseHook(claudeDir string) error {
	settingsPath := filepath.Join(claudeDir, "settings.json")
	settings, _ := readSettingsJSON(settingsPath)

	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = make(map[string]interface{})
	}

	preToolUse, _ := hooks["PreToolUse"].([]interface{})
	for _, entry := range preToolUse {
		em, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		hs, _ := em["hooks"].([]interface{})
		for _, h := range hs {
			hm, _ := h.(map[string]interface{})
			if cmd, _ := hm["command"].(string); cmd == wagglePushCommand {
				return nil // already registered
			}
		}
	}

	preToolUse = append(preToolUse, map[string]interface{}{
		"hooks": []interface{}{
			map[string]interface{}{"type": "command", "command": wagglePushCommand},
		},
	})
	hooks["PreToolUse"] = preToolUse
	settings["hooks"] = hooks

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath, out, 0o644)
}
```

- [ ] **Step 4: Wire into installClaudeCode**

In `installClaudeCode`, after copying the heartbeat script, add:

```go
// Copy PreToolUse push hook
pushData, err := claudeCodeFiles.ReadFile("claude-code/waggle-push.js")
if err != nil {
	return fmt.Errorf("reading waggle-push.js: %w", err)
}
if err := os.WriteFile(filepath.Join(hookDir, "waggle-push.js"), pushData, 0o755); err != nil {
	return fmt.Errorf("writing push hook: %w", err)
}
```

After `registerSessionStartHook`, add:

```go
if err := registerPreToolUseHook(claudeDir); err != nil {
	return fmt.Errorf("registering push hook: %w", err)
}
```

- [ ] **Step 5: Run install tests**

Run: `go test ./internal/install/ -run TestInstallClaudeCode -v`

- [ ] **Step 6: Commit**

```bash
git add integrations/claude-code/waggle-push.js internal/install/claude_code.go internal/install/claude-code/waggle-push.js
git commit -m "feat: Claude Code PreToolUse hook for signal file delivery"
```

---

### Task 7: Presence Fix

**Files:** `internal/broker/router.go`, `internal/broker/broker_test.go`

- [ ] **Step 1: Write test**

Add to `internal/broker/broker_test.go`:

```go
func TestPresence_StripsPushAndDeduplicates(t *testing.T) {
	sockPath, _, cleanup := startTestBroker(t)
	defer cleanup()

	c1 := connectClient(t, sockPath)
	defer c1.Close()
	c1.Send(protocol.Request{Cmd: protocol.CmdConnect, Args: map[string]any{"name": "alice-push"}})

	c2 := connectClient(t, sockPath)
	defer c2.Close()
	c2.Send(protocol.Request{Cmd: protocol.CmdConnect, Args: map[string]any{"name": "bob"}})

	c3 := connectClient(t, sockPath)
	defer c3.Close()
	c3.Send(protocol.Request{Cmd: protocol.CmdConnect, Args: map[string]any{"name": "probe"}})

	resp, _ := c3.Send(protocol.Request{Cmd: protocol.CmdPresence})
	var agents []map[string]string
	json.Unmarshal(resp.Data, &agents)

	names := make(map[string]bool)
	for _, a := range agents {
		if strings.HasSuffix(a["name"], "-push") {
			t.Fatalf("name %q should not have -push suffix", a["name"])
		}
		names[a["name"]] = true
	}
	if !names["alice"] {
		t.Fatal("expected alice (stripped from alice-push)")
	}
	if !names["bob"] {
		t.Fatal("expected bob")
	}
}
```

Add `"strings"` to the test file imports if not present.

- [ ] **Step 2: Run test — expect FAIL**

Run: `go test ./internal/broker/ -run TestPresence_StripsPush -v`

- [ ] **Step 3: Fix handlePresence**

Replace the `handlePresence` function in `internal/broker/router.go`:

```go
func handlePresence(s *Session) protocol.Response {
	s.broker.mu.RLock()
	seen := make(map[string]bool)
	agents := make([]map[string]string, 0, len(s.broker.sessions))
	for name := range s.broker.sessions {
		displayName := strings.TrimSuffix(name, "-push")
		if seen[displayName] {
			continue
		}
		seen[displayName] = true
		agents = append(agents, map[string]string{"name": displayName, "state": "online"})
	}
	s.broker.mu.RUnlock()

	sort.Slice(agents, func(i, j int) bool { return agents[i]["name"] < agents[j]["name"] })

	return protocol.OKResponse(mustMarshal(agents))
}
```

`strings` and `sort` are already imported in router.go.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/broker/ -run TestPresence -v && go test ./internal/broker/ -v`

- [ ] **Step 5: Commit**

```bash
git add internal/broker/router.go internal/broker/broker_test.go
git commit -m "fix: presence strips -push suffix, deduplicates"
```

---

### Task 8: E2E Verification

- [ ] **Step 1: Rebuild and install**

```bash
cd ~/Projects/Claude/waggle
go build -o waggle . && cp waggle ~/bin/waggle
waggle install claude-code
waggle install codex
```

- [ ] **Step 2: Verify installation**

```bash
cat ~/.waggle/shell-hook.sh | head -3
grep WAGGLE ~/.zshenv
ls ~/.claude/hooks/waggle-push.js
```

- [ ] **Step 3: Test signal delivery end-to-end**

```bash
cd ~/Projects/Claude/waggle
waggle stop 2>/dev/null; waggle start
A=$(waggle adapter bootstrap claude-code --format json | jq -r .agent_name)
B=$(waggle adapter bootstrap codex --format json | jq -r .agent_name)
echo "Claude=$A Codex=$B"
WAGGLE_AGENT_NAME=$A waggle send $B "e2e test message"
sleep 2
cat ~/.waggle/runtime/signals/$B  # should show message
ls ~/.waggle/runtime/agent-ppid-*  # should show mappings
```

- [ ] **Step 4: Test atomic consumption**

```bash
SIG=~/.waggle/runtime/signals/$B
TMP="$SIG.c-$$"
mv "$SIG" "$TMP" && cat "$TMP" && rm "$TMP"
# Message displayed, signal consumed, no file left
```

- [ ] **Step 5: Test presence**

```bash
WAGGLE_AGENT_NAME=probe waggle presence
# Names without -push, sorted
```

- [ ] **Step 6: Full test suite**

```bash
go test ./... -count=1
```

- [ ] **Step 7: Commit any E2E fixes**

---

### Task 9: Adapter Instruction Updates

**Files:** `integrations/codex/AGENTS-block.md`, `integrations/gemini/GEMINI-block.md`, `integrations/auggie/RULE-block.md`, `integrations/augment/SKILL-block.md`

- [ ] **Step 1: Add delivery explanation to each block**

After bootstrap instruction in each file, add:

```markdown
Messages from other agents appear automatically in your shell command output,
prefixed with 📨. When you see a waggle message, respond to the sender:

    WAGGLE_AGENT_NAME="<your-agent>" waggle send <sender-name> "<response>"

Use `waggle presence` to discover connected agents.
```

- [ ] **Step 2: Commit**

```bash
git add integrations/
git commit -m "docs: adapter instructions for automatic message delivery"
```

---

## Known Limitations

1. **New shell per command assumption.** The shell hook relies on each agent tool call spawning a fresh shell process (verified for Claude Code). If a platform reuses a persistent shell, messages only surface on the first command. Mitigation: add `precmd`/`PROMPT_COMMAND` in a future PR if needed.

2. **PPID relies on env var propagation.** The `WAGGLE_AGENT_PPID=$PPID` pattern requires the calling shell to expand `$PPID` correctly. This works for standard `sh`/`bash`/`zsh` invocations. If an agent bypasses the shell entirely (direct exec without shell), the env var won't be set, and fallback to `os.Getppid()` gives the wrong PID. Mitigation: all known agent platforms use shell invocation.

3. **PID reuse.** A stale PPID mapping file could match a recycled PID. Mitigation: mtime-based 24h cleanup in daemon maintenance loop. PID reuse in <24h is rare for long-lived agent processes.

4. **Message content in shell output.** Arbitrary message text is written to stderr. Very long messages or control characters could disrupt output. Mitigation: 64KB signal file cap. Future: sanitize/truncate message content.

5. **Single machine scope.** PPID mapping and signal files are local. Remote agents cannot use this mechanism. The runtime daemon handles cross-machine delivery; this is the local presentation layer.

6. **Two consumers for Claude Code.** PreToolUse hook and shell hook both check signals. No race because PreToolUse fires before Bash tool (atomic consume clears signal before shell hook runs). Non-Bash tools (Read, Write) only trigger PreToolUse — no shell hook involvement.
