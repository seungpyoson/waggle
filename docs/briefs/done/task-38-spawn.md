# Task 38 Brief: Spawn — Launch Visible Agent Sessions in Terminal Tabs

**Branch:** `feat/38-spawn`
**Goal:** `waggle spawn --name deployer` opens a new terminal tab with an AI agent connected to waggle. One command per agent.

**Dependencies:** Task 43 (direct messaging) must be merged. Task 48 (presence) should be merged but is not strictly required — spawn can work without presence.

**Files to create:**
- `internal/spawn/terminal.go` — terminal detection and tab launch
- `internal/spawn/terminal_test.go` — unit tests
- `internal/spawn/manager.go` — PID tracking and cleanup
- `internal/spawn/manager_test.go` — unit tests
- `cmd/spawn.go` — CLI command

**Files to modify:**
- `internal/broker/broker.go` — add spawn manager to Broker struct
- `internal/broker/router.go` — add spawn tracking to status handler
- `cmd/stop.go` — kill spawned agents on stop
- `internal/config/config.go` — add spawn-related defaults

---

## What to build

### 1. Terminal Detection (`internal/spawn/terminal.go`)

Detect which terminal emulator is running and open a new tab:

```go
type Terminal int

const (
    TerminalApp Terminal = iota  // macOS Terminal.app
    ITerm2                        // macOS iTerm2
    LinuxDefault                  // Linux: xterm, gnome-terminal, etc.
    Unknown
)

func Detect() Terminal
// Check TERM_PROGRAM env var:
//   "Apple_Terminal" → TerminalApp
//   "iTerm.app"     → ITerm2
//   else            → check for common Linux terminals in PATH

func OpenTab(t Terminal, name string, cmd string, env map[string]string) (int, error)
// Opens a new tab and runs cmd with env vars set. Returns PID of spawned process.
```

**macOS Terminal.app:**
```go
// osascript -e 'tell application "Terminal" to do script "cd /path && ENV=val waggle-agent-wrapper deployer"'
```

**macOS iTerm2:**
```go
// osascript -e 'tell application "iTerm2" to tell current window to create tab with default profile command "cd /path && ENV=val waggle-agent-wrapper deployer"'
```

**Linux:**
```go
// Try in order: gnome-terminal -- bash -c "...", xterm -e "...", x-terminal-emulator -e "..."
```

### 2. Agent Config

Default agent config at `~/.waggle/agents.json` (created on first spawn if missing):

```json
{
  "default": "claude",
  "agents": {
    "claude": {
      "cmd": "claude",
      "args": ["-p", "--output-format", "stream-json"]
    },
    "codex": {
      "cmd": "codex"
    },
    "gemini": {
      "cmd": "gemini"
    }
  }
}
```

```go
type AgentConfig struct {
    Default string                    `json:"default"`
    Agents  map[string]AgentDef       `json:"agents"`
}

type AgentDef struct {
    Cmd  string   `json:"cmd"`
    Args []string `json:"args,omitempty"`
}

func LoadAgentConfig() (*AgentConfig, error)
// Read ~/.waggle/agents.json. If not exists, create with defaults.

func (c *AgentConfig) GetAgent(agentType string) (*AgentDef, error)
// Look up by type. If type is "", use c.Default.
```

### 3. Spawn Manager (`internal/spawn/manager.go`)

Tracks spawned agent PIDs:

```go
type Agent struct {
    Name      string `json:"name"`
    Type      string `json:"type"`
    PID       int    `json:"pid"`
    Alive     bool   `json:"alive"`
    SpawnedAt string `json:"spawned_at"`
}

type Manager struct {
    mu     sync.RWMutex
    agents map[string]*Agent
}

func (m *Manager) Add(name, agentType string, pid int)
func (m *Manager) Remove(name string)
func (m *Manager) List() []*Agent
// For each agent, check if PID is still alive (os.FindProcess + signal 0)
func (m *Manager) StopAll() error
// SIGTERM each agent PID, wait up to 5s, then SIGKILL
```

### 4. CLI Command (`cmd/spawn.go`)

```bash
waggle spawn --name deployer              # default agent type (claude)
waggle spawn --name tester --type codex   # specific type
```

What spawn does:
1. Load agent config
2. Resolve agent type (default or --type)
3. Detect terminal
4. Build env: `WAGGLE_PROJECT_ID=<project-hash>`, `WAGGLE_AGENT_NAME=<name>` — agent name is critical: messaging commands (Task 43) use `WAGGLE_AGENT_NAME` to auto-identify. Without it, spawned agents can't send/receive messages.
5. Build command: agent cmd + args
6. Open terminal tab with command
7. Record PID in spawn manager (via broker connection)
8. Print: "Spawned deployer (claude) in new tab — PID 12345"

### 5. Status Integration (`internal/broker/router.go`)

Modify `handleStatus` to include spawned agents:

```go
status["spawned"] = b.spawnMgr.List()
```

### 6. Stop Integration (`cmd/stop.go`)

Modify stop to kill spawned agents before stopping broker:

```go
// Before sending stop command to broker:
// 1. Connect to broker
// 2. Get spawned agent list from status
// 3. SIGTERM each PID
// 4. Then send stop to broker
```

## Invariants

| ID | Invariant | How to verify | Test name |
|----|-----------|---------------|-----------|
| S1 | Spawn opens a visible terminal tab | Manual test: verify tab appears | (manual) |
| S2 | Spawned agent connects to waggle automatically | Spawn, check broker status shows new session | (manual + integration) |
| S3 | WAGGLE_PROJECT_ID env is set in spawned process | Verify env in spawned shell | (manual) |
| S4 | Status shows spawned agents | Spawn, status includes agent info | TestBroker_SpawnStatus |
| S5 | Stop kills all spawned agents | Spawn 2, stop, verify PIDs dead | TestManager_StopAll |
| S6 | Agent config created on first run | Delete config, spawn, verify created | TestLoadAgentConfig_Default |
| S7 | --type selects correct agent | Spawn --type codex, verify codex cmd used | TestGetAgent_Specific |
| S8 | Terminal detection works for current platform | Detect() returns non-Unknown | TestDetect_ReturnsNonUnknown |
| S9 | Spawn with duplicate name returns error | Spawn "worker", spawn "worker" again, error | TestManager_AddDuplicate |
| S10 | StopAll handles already-dead PIDs gracefully | Start process, kill it, StopAll doesn't panic | TestManager_StopAllWithDeadPID |
| S11 | Spawn when broker not running returns clear error | Spawn without broker, error says "broker not running" | TestSpawn_NoBroker |
| S12 | WAGGLE_AGENT_NAME is set in spawned process | Spawned agent auto-identifies for messaging | (manual) |

## Tests (TDD)

### Unit tests (`internal/spawn/terminal_test.go`)
```
TestDetect_ReturnsNonUnknown     — on macOS, detects Terminal.app or iTerm2
TestDetect_EnvOverride           — TERM_PROGRAM controls detection
```

### Unit tests (`internal/spawn/manager_test.go`)
```
TestManager_AddAndList           — add agent, list returns it
TestManager_Remove               — remove agent, list excludes it
TestManager_AddDuplicate         — add same name twice, second returns error
TestManager_AliveCheck           — alive=true for running PID, false for dead PID
TestManager_StopAll              — starts test processes, StopAll kills them all
TestManager_StopAllWithDeadPID   — add already-dead PID, StopAll doesn't crash
TestManager_ListEmpty            — empty manager returns empty list (not nil)
```

### Unit tests (agent config — in `internal/spawn/config_test.go`)
```
TestLoadAgentConfig_Default      — missing file creates default config with claude/codex/gemini
TestLoadAgentConfig_Custom       — custom file loaded correctly
TestLoadAgentConfig_Invalid      — malformed JSON returns error
TestGetAgent_Default             — empty type returns default agent
TestGetAgent_Specific            — "codex" returns codex agent def
TestGetAgent_Unknown             — unknown type returns error
```

### Integration tests (add to `internal/broker/broker_test.go`)
```
TestBroker_SpawnStatus           — add agent to spawn manager, status includes it, stop removes it
```

Note: terminal tab opening is inherently manual-test territory. Automated tests cover the manager, config, and detection. The actual AppleScript/terminal invocation is verified by the live smoke test.

### Edge cases the implementer should add if discovered:
The listed tests are the MINIMUM. Add tests for edge cases (e.g., spawn with very long name, agent config file permissions, terminal detection on CI where no terminal exists). More tests is always better.

## Acceptance criteria

- [ ] All unit tests pass: `go test ./internal/spawn/ -v -count=1`
- [ ] Race detector: `go test ./... -race -count=1 -timeout=120s`
- [ ] `go vet ./...` — zero warnings
- [ ] CLI: `waggle spawn --name w1` opens terminal tab with agent
- [ ] CLI: `waggle status` shows spawned agent
- [ ] CLI: `waggle stop` kills spawned agents + broker

## POST-TASK: Live smoke test

```bash
cd ~/Projects/Claude/waggle && mkdir -p /tmp/waggle-smoke && cd /tmp/waggle-smoke && mkdir .git

# Start broker
waggle start --foreground &
sleep 1

# Spawn an agent
waggle spawn --name worker-1

# Verify in original terminal
waggle status
# Must show worker-1 in spawned agents list

# Send a message to the spawned agent
waggle send worker-1 "hello from orchestrator"

# Stop everything
waggle stop
# All spawned terminals should close or agent should exit
```

If spawn doesn't open a visible tab with an agent that connects to waggle, Task 38 is not done.

- [ ] Commit: `feat(spawn): launch visible agent sessions in terminal tabs`
