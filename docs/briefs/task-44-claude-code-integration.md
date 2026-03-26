# Task 44 Brief: Claude Code Integration — Zero-Knowledge Waggle Participation

**Issue:** #44
**Branch:** `feat/44-claude-code-integration`
**Goal:** Claude Code sessions participate in waggle automatically. No manual instructions, no CLI discovery, no forgotten heartbeats. One `waggle install claude-code` command sets everything up.

**Dependencies:** Task 43 (messaging), Task 48 (ack/presence), Task 38 (spawn) merged. Task 41 (task lifecycle) should be merged first.

**This task is different from previous tasks.** Previous tasks modified the waggle Go codebase. This task creates:
- Shell scripts (hook, heartbeat)
- Markdown files (skills)
- One Go command (`waggle install`)
- An installer that writes to `~/.claude/` (Claude Code's config directory)

**Read these files first:**
- `~/.claude/settings.json` — understand the SessionStart hook structure (line 199+). Your hook must be added to the existing array, not replace it.
- `~/.claude/skills/` — look at any existing skill directory for the markdown format. Skills have YAML frontmatter (`name`, `description`) and content.
- `cmd/spawn.go` — example of a CLI command that interacts with external systems
- `internal/config/config.go` — Defaults struct for any new constants
- `cmd/send.go` — the exact CLI syntax for `waggle send` (the skill must match this exactly)
- `cmd/inbox.go` — the exact CLI syntax for `waggle inbox`
- `cmd/ack.go` — the exact CLI syntax for `waggle ack`
- `cmd/presence.go` — the exact CLI syntax for `waggle presence`
- `cmd/task_claim.go` — the exact CLI syntax for `waggle task claim`
- `cmd/task_complete.go` — the exact CLI syntax for `waggle task complete`

---

## Scope

**In:**
- `waggle install claude-code` CLI command — creates hook + skills + registers in settings.json
- `waggle install claude-code --uninstall` — removes everything cleanly
- SessionStart hook that auto-checks inbox and pending tasks, outputs markdown context
- `/waggle` skill with subcommands: send, inbox, status, claim, done, presence, ack
- Auto-heartbeat background script for claimed tasks

**Out:**
- Codex/Gemini/Augment integrations (separate issues)
- Health reporting (#42, #45, #46)
- Modifications to waggle broker or protocol — this is purely client-side
- Auto-claim on task assignment (future protocol extension)
- Incoming message push injection (requires persistent connection — future work)

---

## What to Build

### 1. File Layout

All source files live in the waggle repo under `integrations/claude-code/`:

```
integrations/claude-code/
├── hook.sh              # SessionStart hook
├── heartbeat.sh         # Background heartbeat for claimed tasks
└── skills/
    ├── waggle.md        # Main skill router (/waggle)
    ├── send.md          # /waggle send
    ├── inbox.md         # /waggle inbox
    ├── status.md        # /waggle status
    ├── claim.md         # /waggle claim
    ├── done.md          # /waggle done
    ├── presence.md      # /waggle presence
    └── ack.md           # /waggle ack
```

The installer copies these to `~/.claude/hooks/` and `~/.claude/skills/waggle/`.

### 2. SessionStart Hook (`integrations/claude-code/hook.sh`)

```bash
#!/bin/bash
# waggle-connect.sh — SessionStart hook for Claude Code
# Outputs markdown context if there are pending messages or tasks.
# Silent exit (<2s) if waggle not available or no pending work.

set -euo pipefail

# 1. Check if waggle is available
command -v waggle >/dev/null 2>&1 || exit 0

# 2. Check if broker is running (waggle status is the cheapest check)
STATUS=$(timeout 2 waggle status 2>/dev/null) || exit 0

# 3. Resolve agent name
AGENT_NAME="${WAGGLE_AGENT_NAME:-claude-$$}"

# 4. Check inbox (2s timeout)
INBOX=$(timeout 2 bash -c "WAGGLE_AGENT_NAME='$AGENT_NAME' waggle inbox" 2>/dev/null) || INBOX=""
INBOX_COUNT=0
if [ -n "$INBOX" ]; then
    INBOX_COUNT=$(echo "$INBOX" | jq -r '.data | length' 2>/dev/null || echo "0")
fi

# 5. Check pending tasks (2s timeout)
TASKS=$(timeout 2 waggle task list --state pending 2>/dev/null) || TASKS=""
TASK_COUNT=0
if [ -n "$TASKS" ]; then
    TASK_COUNT=$(echo "$TASKS" | jq -r '.data | length' 2>/dev/null || echo "0")
fi

# 6. Output context only if there's something to report
if [ "$INBOX_COUNT" != "0" ] || [ "$TASK_COUNT" != "0" ]; then
    echo ""
    echo "## Waggle Agent: ${AGENT_NAME}"
    echo ""
    if [ "$INBOX_COUNT" != "0" ]; then
        echo "### Inbox (${INBOX_COUNT} messages)"
        echo "$INBOX" | jq -r '.data[] | "- **\(.from):** \(.body)"' 2>/dev/null || true
        echo ""
    fi
    if [ "$TASK_COUNT" != "0" ]; then
        echo "### Pending Tasks (${TASK_COUNT})"
        echo "$TASKS" | jq -r '.data[] | "- #\(.ID) [\(.Type // "untyped")]: \(.Payload)"' 2>/dev/null || true
        echo ""
    fi
    echo "Use \`/waggle\` commands to interact. Agent name: \`${AGENT_NAME}\`"
fi
```

**Critical properties:**
- Every external command wrapped in `timeout 2` — total hook must complete in <6s worst case
- `exit 0` on every failure path — never block Claude Code session start
- Outputs markdown to stdout (Claude Code injects this as session context)
- Uses `jq` for JSON parsing — `jq` is standard on macOS/Linux

### 3. Skills (`integrations/claude-code/skills/`)

Each skill is a markdown file with YAML frontmatter. The skill encodes the **exact CLI syntax** so the agent executes in one bash call.

**`waggle.md` (main router):**
```markdown
---
name: waggle
description: Interact with waggle agent coordination. Subcommands: send, inbox, status, claim, done, presence, ack
---

Available commands:
- `/waggle send <recipient> <message>` — send a message
- `/waggle inbox` — check your messages
- `/waggle ack <id>` — acknowledge a message
- `/waggle status` — broker status and queue health
- `/waggle claim` — claim next available task
- `/waggle done <task_id> <result>` — complete a claimed task
- `/waggle presence` — who's connected

Your agent name is `${WAGGLE_AGENT_NAME}` (set by waggle spawn or environment).
```

**`send.md`:**
```markdown
---
name: waggle-send
description: Send a message to another waggle agent. Use when told to message, notify, or communicate with another agent.
---

Execute this command:

```bash
WAGGLE_AGENT_NAME="${WAGGLE_AGENT_NAME:-claude-$$}" waggle send "<recipient>" "<message>"
```

Replace `<recipient>` with the agent name and `<message>` with the text.

Options (append to command):
- `--priority critical` — urgent message
- `--priority bulk` — low priority
- `--ttl 300` — message expires after 300 seconds
- `--await-ack --timeout 30` — block until receiver acknowledges (max 30s)

Example:
```bash
WAGGLE_AGENT_NAME=orchestrator waggle send worker-1 "implement the auth module" --await-ack --timeout 60
```
```

**`inbox.md`:**
```markdown
---
name: waggle-inbox
description: Check your waggle inbox for messages from other agents
---

Execute this command:

```bash
WAGGLE_AGENT_NAME="${WAGGLE_AGENT_NAME:-claude-$$}" waggle inbox
```

Returns JSON with messages. Each message has `id`, `from`, `body`, `priority`, `state`.
After reading, acknowledge important messages with `/waggle ack <id>`.
```

**`ack.md`:**
```markdown
---
name: waggle-ack
description: Acknowledge a waggle message (confirms receipt to sender)
---

Execute this command:

```bash
WAGGLE_AGENT_NAME="${WAGGLE_AGENT_NAME:-claude-$$}" waggle ack <message_id>
```

Replace `<message_id>` with the numeric ID from your inbox.
If the sender used `--await-ack`, this unblocks them.
```

**`status.md`:**
```markdown
---
name: waggle-status
description: Check waggle broker status — connected agents, task queue, spawned agents
---

Execute this command:

```bash
waggle status
```

No agent name needed. Returns JSON with sessions, tasks, spawned agents, queue health.
```

**`claim.md`:**
```markdown
---
name: waggle-claim
description: Claim the next available task from the waggle queue
---

Execute these commands:

```bash
# Connect and claim
waggle connect --name "${WAGGLE_AGENT_NAME:-claude-$$}"
RESULT=$(waggle task claim --type "<type>")
TASK_ID=$(echo "$RESULT" | jq -r '.data.ID')
TOKEN=$(echo "$RESULT" | jq -r '.data.ClaimToken')
echo "Claimed task $TASK_ID with token $TOKEN"
```

Replace `<type>` with the task type to claim, or omit `--type` for any task.

**IMPORTANT:** After claiming, the task has a 5-minute lease. Start a heartbeat:
```bash
# Keep lease alive (run in background)
while true; do sleep 120; waggle task heartbeat $TASK_ID --token $TOKEN 2>/dev/null || break; done &
```

When done, complete the task:
```bash
waggle task complete $TASK_ID '{"result": "what you did"}' --token $TOKEN
```
```

**`done.md`:**
```markdown
---
name: waggle-done
description: Complete a claimed waggle task with a result
---

Execute this command:

```bash
waggle connect --name "${WAGGLE_AGENT_NAME:-claude-$$}"
waggle task complete <task_id> '<result_json>' --token <claim_token>
```

Replace:
- `<task_id>` — the task ID from when you claimed it
- `<result_json>` — JSON describing what was accomplished
- `<claim_token>` — the token from the claim response

Example:
```bash
waggle task complete 5 '{"status": "done", "commit": "abc123"}' --token e4f7a2b1c3d5
```
```

**`presence.md`:**
```markdown
---
name: waggle-presence
description: List connected waggle agents and their status
---

Execute this command:

```bash
WAGGLE_AGENT_NAME="${WAGGLE_AGENT_NAME:-claude-$$}" waggle presence
```

Returns JSON array of connected agents with name and state (online).
```

### 4. Auto-Heartbeat Script (`integrations/claude-code/heartbeat.sh`)

```bash
#!/bin/bash
# waggle-heartbeat.sh — background heartbeat for claimed tasks
# Usage: waggle-heartbeat.sh <task_id> <claim_token> [interval_seconds]
# Runs until heartbeat fails (task completed/failed/lease lost) or killed.

set -euo pipefail

TASK_ID="${1:?task_id required}"
CLAIM_TOKEN="${2:?claim_token required}"
INTERVAL="${3:-120}"  # default 2 minutes (lease is 5 minutes)

while true; do
    sleep "$INTERVAL"
    if ! waggle task heartbeat "$TASK_ID" --token "$CLAIM_TOKEN" 2>/dev/null; then
        # Heartbeat failed — task completed, failed, or lease lost
        exit 0
    fi
done
```

### 5. Installer (`cmd/install.go` + `internal/install/claude_code.go`)

**`cmd/install.go`:**
```go
package cmd

import (
    "fmt"

    "github.com/seungpyoson/waggle/internal/install"
    "github.com/spf13/cobra"
)

var installUninstall bool

func init() {
    installCmd.Flags().BoolVar(&installUninstall, "uninstall", false, "Remove integration")
    rootCmd.AddCommand(installCmd)
}

var installCmd = &cobra.Command{
    Use:   "install <platform>",
    Short: "Install waggle integration for a platform",
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        platform := args[0]
        switch platform {
        case "claude-code":
            if installUninstall {
                if err := install.UninstallClaudeCode(); err != nil {
                    printErr("INSTALL_ERROR", err.Error())
                    return nil
                }
                printJSON(map[string]any{"ok": true, "message": "Claude Code integration removed"})
            } else {
                if err := install.InstallClaudeCode(); err != nil {
                    printErr("INSTALL_ERROR", err.Error())
                    return nil
                }
                printJSON(map[string]any{"ok": true, "message": "Claude Code integration installed. Restart Claude Code to activate."})
            }
        default:
            printErr("INVALID_REQUEST", fmt.Sprintf("unknown platform: %s (supported: claude-code)", platform))
        }
        return nil
    },
}
```

**`internal/install/claude_code.go`:**

```go
package install

import (
    "embed"
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
)

//go:embed all:claude-code
var claudeCodeFiles embed.FS

func InstallClaudeCode() error {
    home, err := os.UserHomeDir()
    if err != nil {
        return fmt.Errorf("getting home dir: %w", err)
    }

    claudeDir := filepath.Join(home, ".claude")

    // 1. Copy hook
    hookDir := filepath.Join(claudeDir, "hooks")
    if err := os.MkdirAll(hookDir, 0755); err != nil {
        return fmt.Errorf("creating hooks dir: %w", err)
    }
    hookData, _ := claudeCodeFiles.ReadFile("claude-code/hook.sh")
    if err := os.WriteFile(filepath.Join(hookDir, "waggle-connect.sh"), hookData, 0755); err != nil {
        return fmt.Errorf("writing hook: %w", err)
    }

    // 2. Copy heartbeat script
    heartbeatData, _ := claudeCodeFiles.ReadFile("claude-code/heartbeat.sh")
    if err := os.WriteFile(filepath.Join(hookDir, "waggle-heartbeat.sh"), heartbeatData, 0755); err != nil {
        return fmt.Errorf("writing heartbeat: %w", err)
    }

    // 3. Copy skills
    skillDir := filepath.Join(claudeDir, "skills", "waggle")
    if err := os.MkdirAll(skillDir, 0755); err != nil {
        return fmt.Errorf("creating skills dir: %w", err)
    }
    skillFiles := []string{"waggle.md", "send.md", "inbox.md", "ack.md", "status.md", "claim.md", "done.md", "presence.md"}
    for _, name := range skillFiles {
        data, _ := claudeCodeFiles.ReadFile("claude-code/skills/" + name)
        if err := os.WriteFile(filepath.Join(skillDir, name), data, 0644); err != nil {
            return fmt.Errorf("writing skill %s: %w", name, err)
        }
    }

    // 4. Register hook in settings.json
    if err := registerSessionStartHook(claudeDir); err != nil {
        return fmt.Errorf("registering hook: %w", err)
    }

    return nil
}

func UninstallClaudeCode() error {
    home, err := os.UserHomeDir()
    if err != nil {
        return fmt.Errorf("getting home dir: %w", err)
    }

    claudeDir := filepath.Join(home, ".claude")

    // Remove hook files
    os.Remove(filepath.Join(claudeDir, "hooks", "waggle-connect.sh"))
    os.Remove(filepath.Join(claudeDir, "hooks", "waggle-heartbeat.sh"))

    // Remove skill directory
    os.RemoveAll(filepath.Join(claudeDir, "skills", "waggle"))

    // Deregister hook from settings.json
    if err := deregisterSessionStartHook(claudeDir); err != nil {
        return fmt.Errorf("deregistering hook: %w", err)
    }

    return nil
}

// registerSessionStartHook adds the waggle hook to settings.json SessionStart array.
// Uses JSON parsing to safely merge without overwriting existing hooks.
func registerSessionStartHook(claudeDir string) error {
    settingsPath := filepath.Join(claudeDir, "settings.json")

    // Read existing settings
    var settings map[string]interface{}
    data, err := os.ReadFile(settingsPath)
    if err != nil {
        if os.IsNotExist(err) {
            settings = make(map[string]interface{})
        } else {
            return fmt.Errorf("reading settings: %w", err)
        }
    } else {
        if err := json.Unmarshal(data, &settings); err != nil {
            return fmt.Errorf("parsing settings: %w", err)
        }
    }

    // Get or create hooks section
    hooks, _ := settings["hooks"].(map[string]interface{})
    if hooks == nil {
        hooks = make(map[string]interface{})
    }

    // Get or create SessionStart array
    sessionStart, _ := hooks["SessionStart"].([]interface{})

    // Check if waggle hook already registered
    waggleHook := map[string]interface{}{
        "hooks": []interface{}{
            map[string]interface{}{
                "type":    "command",
                "command": "bash $HOME/.claude/hooks/waggle-connect.sh",
            },
        },
    }

    // Check for existing waggle entry
    for _, entry := range sessionStart {
        entryMap, ok := entry.(map[string]interface{})
        if !ok {
            continue
        }
        entryHooks, _ := entryMap["hooks"].([]interface{})
        for _, h := range entryHooks {
            hMap, _ := h.(map[string]interface{})
            if cmd, _ := hMap["command"].(string); cmd == "bash $HOME/.claude/hooks/waggle-connect.sh" {
                return nil // already registered
            }
        }
    }

    // Add waggle hook
    sessionStart = append(sessionStart, waggleHook)
    hooks["SessionStart"] = sessionStart
    settings["hooks"] = hooks

    // Write back
    out, err := json.MarshalIndent(settings, "", "  ")
    if err != nil {
        return fmt.Errorf("marshaling settings: %w", err)
    }

    return os.WriteFile(settingsPath, out, 0644)
}

// deregisterSessionStartHook removes the waggle hook from settings.json.
func deregisterSessionStartHook(claudeDir string) error {
    settingsPath := filepath.Join(claudeDir, "settings.json")

    data, err := os.ReadFile(settingsPath)
    if err != nil {
        return nil // no settings = nothing to deregister
    }

    var settings map[string]interface{}
    if err := json.Unmarshal(data, &settings); err != nil {
        return fmt.Errorf("parsing settings: %w", err)
    }

    hooks, _ := settings["hooks"].(map[string]interface{})
    if hooks == nil {
        return nil
    }

    sessionStart, _ := hooks["SessionStart"].([]interface{})
    if sessionStart == nil {
        return nil
    }

    // Filter out waggle entries
    var filtered []interface{}
    for _, entry := range sessionStart {
        entryMap, ok := entry.(map[string]interface{})
        if !ok {
            filtered = append(filtered, entry)
            continue
        }
        entryHooks, _ := entryMap["hooks"].([]interface{})
        isWaggle := false
        for _, h := range entryHooks {
            hMap, _ := h.(map[string]interface{})
            if cmd, _ := hMap["command"].(string); cmd == "bash $HOME/.claude/hooks/waggle-connect.sh" {
                isWaggle = true
                break
            }
        }
        if !isWaggle {
            filtered = append(filtered, entry)
        }
    }

    hooks["SessionStart"] = filtered
    settings["hooks"] = hooks

    out, err := json.MarshalIndent(settings, "", "  ")
    if err != nil {
        return fmt.Errorf("marshaling settings: %w", err)
    }

    return os.WriteFile(settingsPath, out, 0644)
}
```

**Embed directive:** The `embed.FS` requires the source files to be at `internal/install/claude-code/`. Copy or symlink `integrations/claude-code/` → `internal/install/claude-code/` so the embed works. Alternatively, embed from the `integrations/` directory by adjusting the package location.

---

## Invariants

| ID | Invariant | How to verify | Test name |
|----|-----------|---------------|-----------|
| I1 | Hook exits 0 when waggle not installed | Remove waggle from PATH, run hook, check exit code | TestHook_NoWaggle |
| I2 | Hook exits 0 when broker not running | Stop broker, run hook, check exit code + no output | TestHook_NoBroker |
| I3 | Hook outputs inbox when messages pending | Send message, run hook, verify markdown output | TestHook_InboxOutput |
| I4 | Hook outputs tasks when tasks pending | Create task, run hook, verify markdown output | TestHook_TaskOutput |
| I5 | Hook completes in <6s | Time the hook with broker running, verify | TestHook_Timeout |
| I6 | Install creates hook file | Run install, verify file at ~/.claude/hooks/waggle-connect.sh | TestInstall_HookCreated |
| I7 | Install creates skill directory | Run install, verify 8 files at ~/.claude/skills/waggle/ | TestInstall_SkillsCreated |
| I8 | Install is idempotent | Run install twice, no error, files same | TestInstall_Idempotent |
| I9 | Install registers SessionStart hook in settings.json | Run install, parse settings.json, verify waggle hook entry | TestInstall_HookRegistered |
| I10 | Install doesn't duplicate hook entry | Run install twice, verify exactly 1 waggle entry in SessionStart | TestInstall_NoDuplicate |
| I11 | Install doesn't overwrite existing hooks | Create settings.json with existing hooks, install, verify all hooks present | TestInstall_PreservesExisting |
| I12 | Uninstall removes hook + skills | Run uninstall, verify files gone | TestUninstall_Clean |
| I13 | Uninstall removes hook from settings.json | Run uninstall, parse settings.json, verify no waggle entry | TestUninstall_DeregistersHook |
| I14 | Uninstall preserves other hooks | Create settings with waggle + other hooks, uninstall, verify other hooks intact | TestUninstall_PreservesOther |
| I15 | Heartbeat script exits on task complete | Start heartbeat, complete task, verify script exits | TestHeartbeat_ExitsOnComplete |

---

## Tests (TDD)

### Go tests (`internal/install/claude_code_test.go`)

All tests use `t.TempDir()` as HOME — never test against real `~/.claude/`.

```
TestInstall_HookCreated          — install to tmpdir, verify hook.sh exists + executable
TestInstall_SkillsCreated        — install to tmpdir, verify 8 skill files exist
TestInstall_HeartbeatCreated     — install to tmpdir, verify heartbeat.sh exists + executable
TestInstall_Idempotent           — install twice, no error, files same content
TestInstall_HookRegistered       — install, read settings.json, verify SessionStart contains waggle
TestInstall_NoDuplicate          — install twice, verify exactly 1 waggle entry
TestInstall_PreservesExisting    — write settings.json with existing hooks, install, verify all hooks present
TestInstall_CreatesSettingsIfMissing — no settings.json exists, install creates it with waggle hook
TestUninstall_Clean              — install then uninstall, verify hook + skills removed
TestUninstall_DeregistersHook    — install then uninstall, verify settings.json has no waggle entry
TestUninstall_PreservesOther     — settings.json with waggle + other, uninstall, other still present
TestUninstall_IdempotentNoFiles  — uninstall when never installed, no error
```

### Shell integration tests (`integrations/claude-code/test/`)

These test the hook script end-to-end with a real broker:

```bash
# test_hook_no_waggle.sh
PATH=/usr/bin bash hook.sh
# exit code must be 0, no output

# test_hook_no_broker.sh
waggle stop 2>/dev/null  # ensure no broker
bash hook.sh
# exit code must be 0, no output

# test_hook_inbox.sh
waggle start --foreground &
sleep 2
WAGGLE_AGENT_NAME=alice waggle send test-agent "hello from test"
OUTPUT=$(WAGGLE_AGENT_NAME=test-agent bash hook.sh)
waggle stop
echo "$OUTPUT" | grep -q "hello from test" || exit 1

# test_hook_tasks.sh
waggle start --foreground &
sleep 2
waggle connect --name cli
waggle task create '{"desc":"test task"}' --type test
OUTPUT=$(WAGGLE_AGENT_NAME=test-agent bash hook.sh)
waggle stop
echo "$OUTPUT" | grep -q "test task" || exit 1
```

---

## Acceptance Criteria

- [ ] `waggle install claude-code` creates hook + skills + registers hook
- [ ] `waggle install claude-code --uninstall` removes everything cleanly
- [ ] Hook auto-detects waggle and injects inbox/task context as markdown
- [ ] Hook exits silently (<6s) when waggle not available
- [ ] Each skill file contains exact CLI syntax for its command
- [ ] Heartbeat script runs in background and exits on task completion
- [ ] `go test ./... -race -count=1 -timeout=120s` passes
- [ ] `go vet ./...` clean
- [ ] Install is idempotent, uninstall is idempotent
- [ ] Install preserves existing hooks in settings.json

## Smoke Test

```bash
# 1. Build and install waggle
cd ~/Projects/Claude/waggle
go install .

# 2. Install integration
waggle install claude-code
ls ~/.claude/hooks/waggle-connect.sh     # must exist, executable
ls ~/.claude/skills/waggle/              # must have 8 .md files

# 3. Verify settings.json has waggle hook
cat ~/.claude/settings.json | jq '.hooks.SessionStart[] | select(.hooks[].command | contains("waggle"))'
# must return the waggle hook entry

# 4. Start broker and test hook
cd $(mktemp -d) && git init
WAGGLE_PROJECT_ID=smoke-44 waggle start --foreground &
sleep 2

# 5. Send a message and verify hook picks it up
WAGGLE_AGENT_NAME=orchestrator waggle send test-agent "implement feature X"
WAGGLE_AGENT_NAME=test-agent bash ~/.claude/hooks/waggle-connect.sh
# Must output: "## Waggle Agent: test-agent" + inbox with message

# 6. Create task and verify hook picks it up
waggle connect --name cli
waggle task create '{"desc":"write tests"}' --type test
WAGGLE_AGENT_NAME=test-agent bash ~/.claude/hooks/waggle-connect.sh
# Must output: "### Pending Tasks" section

# 7. Test idempotent install
waggle install claude-code
cat ~/.claude/settings.json | jq '[.hooks.SessionStart[] | select(.hooks[].command | contains("waggle"))] | length'
# Must be 1 (not 2)

# 8. Test uninstall
waggle install claude-code --uninstall
ls ~/.claude/hooks/waggle-connect.sh 2>/dev/null && echo "FAIL: hook still exists" || echo "OK: hook removed"
ls ~/.claude/skills/waggle/ 2>/dev/null && echo "FAIL: skills still exist" || echo "OK: skills removed"

# 9. v2 regression
WAGGLE_AGENT_NAME=alice waggle send bob "regression test"
WAGGLE_AGENT_NAME=bob waggle inbox

waggle stop
```

## Do NOT

- Modify waggle broker or protocol — this is purely client-side integration
- Install anything outside `~/.claude/` — that's Claude Code's config directory
- Overwrite `settings.json` — merge into the existing JSON structure
- Test against real `~/.claude/` — always use `t.TempDir()` as HOME
- Hardcode paths — use `os.UserHomeDir()` + filepath.Join
- Add Go dependencies for JSON merging — `encoding/json` is sufficient
- Break existing hooks — the installer adds to the array, never replaces it
