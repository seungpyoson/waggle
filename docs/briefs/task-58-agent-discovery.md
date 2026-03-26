# Task 58 Brief: Expose Connected Agents for Session Discovery

**Branch:** `feat/58-agent-discovery`
**Goal:** Agents can discover who else is connected to the broker. The SessionStart hook shows connected agents alongside pending tasks.

**Files to create:**
- `cmd/sessions.go` — new CLI command

**Files to modify:**
- `~/.claude/hooks/waggle-connect.sh` — add connected agents section to output

**Files to verify (must NOT change):**
- `internal/broker/router.go` — `handlePresence` already returns agent list (line 648)
- `internal/protocol/codes.go` — `CmdPresence` already exists (line 29)

**Dependencies:** None technically, but #57 (auto-start) makes testing smoother.

---

## What Exists

`handlePresence` (router.go:648-660) already returns all connected agents:
```go
func handlePresence(s *Session) protocol.Response {
    s.broker.mu.RLock()
    agents := make([]map[string]string, 0, len(s.broker.sessions))
    for name := range s.broker.sessions {
        agents = append(agents, map[string]string{"name": name, "state": "online"})
    }
    s.broker.mu.RUnlock()
    sort.Slice(agents, func(i, j int) bool { return agents[i]["name"] < agents[j]["name"] })
    return protocol.OKResponse(mustMarshal(agents))
}
```

The wire protocol command `CmdPresence = "presence"` exists (codes.go:29). There's already a `waggle presence` command (`cmd/presence.go`), but it requires `WAGGLE_AGENT_NAME` which is awkward for discovery.

## What to build

### 1. `waggle sessions` CLI command (cmd/sessions.go)

A simpler alias that doesn't require agent name. It connects with an ephemeral name, runs presence, disconnects.

```bash
waggle sessions
# Output:
# {"ok":true,"data":[{"name":"claude-87952","state":"online"},{"name":"claude-90814","state":"online"}]}
```

Follow the Cobra pattern from `cmd/status.go` — connect anonymously is not possible (connect requires a name), so use a short-lived name like `_discovery-<pid>`.

```go
// cmd/sessions.go
var sessionsCmd = &cobra.Command{
    Use:   "sessions",
    Short: "List connected agent sessions",
    RunE: func(cmd *cobra.Command, args []string) error {
        c, err := connectToBroker(fmt.Sprintf("_discovery-%d", os.Getpid()))
        if err != nil {
            printErr("BROKER_NOT_RUNNING", err.Error())
            return nil
        }
        defer disconnectAndClose(c)

        resp, err := c.Send(protocol.Request{Cmd: protocol.CmdPresence})
        if err != nil {
            printErr("INTERNAL_ERROR", err.Error())
            return nil
        }
        printJSON(resp)
        return nil
    },
}
```

Register in `init()`: `rootCmd.AddCommand(sessionsCmd)`

### 2. Hook enhancement (waggle-connect.sh)

After the existing inbox and tasks sections, add a connected agents section:

```bash
# 7. Check connected agents
SESSIONS=$($TIMEOUT_CMD 2 waggle sessions 2>/dev/null) || SESSIONS=""
SESSION_COUNT=0
if [ -n "$SESSIONS" ]; then
    SESSION_COUNT=$(echo "$SESSIONS" | jq -r '.data | length' 2>/dev/null || echo "0")
fi

# In the output section:
if [ "$SESSION_COUNT" != "0" ]; then
    echo "### Connected Agents (${SESSION_COUNT})"
    echo "$SESSIONS" | jq -r '.data[] | select(.name | startswith("_") | not) | "- \(.name) (\(.state))"' 2>/dev/null || true
    echo ""
fi
```

**Note:** Filter out `_discovery-*` ephemeral sessions from the display (they'll disconnect immediately anyway).

## Invariants

| ID | Invariant | How to verify | Test name |
|----|-----------|---------------|-----------|
| D1 | `waggle sessions` returns connected agent names | Connect two agents, run sessions, both listed | TestBroker_SessionsCommand |
| D2 | Disconnected agents are not listed | Connect, disconnect, sessions excludes them | TestBroker_SessionsAfterDisconnect |
| D3 | Discovery session (`_discovery-*`) doesn't appear in results | Run sessions, the discovery connection itself isn't listed (it disconnects before output) | TestBroker_SessionsExcludesEphemeral |
| D4 | Output is sorted alphabetically | Connect bob then alice, alice appears first | TestBroker_SessionsSorted |
| D5 | Empty broker returns empty list | No agents connected, sessions returns `[]` | TestBroker_SessionsEmpty |
| D6 | Hook shows connected agents | Two agents connected, hook output includes "Connected Agents" section | test_hook_shows_agents |
| D7 | Hook filters ephemeral sessions | Run hook, `_discovery-*` names not shown | test_hook_filters_ephemeral |

## Tests (TDD)

### Integration tests (add to `internal/broker/broker_test.go`)

```
TestBroker_SessionsCommand              — connect "alice" and "bob", send CmdPresence, response data contains both names
TestBroker_SessionsAfterDisconnect      — connect alice, connect bob, disconnect alice, presence only shows bob
TestBroker_SessionsSorted               — connect "bob" then "alice", presence returns alice before bob
TestBroker_SessionsEmpty                — no agents connected, presence returns empty array (not null)
```

**Note:** D3 (ephemeral exclusion) is tested at the CLI level, not broker level — the broker correctly lists all sessions. The CLI command disconnects before printing, so it won't appear. Verified by smoke test.

### Shell tests (add to `docs/briefs/tests/test-58-discovery.sh`)

```bash
#!/bin/bash
set -euo pipefail

PASS=0
FAIL=0

# test_sessions_command — D1
echo "TEST: sessions_command"
waggle start >/dev/null 2>&1 || true
sleep 0.5
# Connect a persistent subscriber as "agent-a"
WAGGLE_AGENT_NAME=agent-a waggle events subscribe task.events &
SUB_PID=$!
sleep 0.5
OUTPUT=$(waggle sessions 2>/dev/null)
kill $SUB_PID 2>/dev/null || true
if echo "$OUTPUT" | jq -e '.data[] | select(.name == "agent-a")' >/dev/null 2>&1; then
    echo "  PASS"; ((PASS++))
else
    echo "  FAIL: agent-a not in sessions output"; ((FAIL++))
fi

# test_hook_shows_agents — D6
echo "TEST: hook_shows_agents"
WAGGLE_AGENT_NAME=agent-b waggle events subscribe task.events &
SUB_PID=$!
sleep 0.5
HOOK_OUTPUT=$(WAGGLE_AGENT_NAME=test-hook bash ~/.claude/hooks/waggle-connect.sh)
kill $SUB_PID 2>/dev/null || true
if echo "$HOOK_OUTPUT" | grep -q "Connected Agents"; then
    echo "  PASS"; ((PASS++))
else
    echo "  FAIL: hook output missing Connected Agents section"; ((FAIL++))
fi

# test_hook_filters_ephemeral — D7
echo "TEST: hook_filters_ephemeral"
if echo "$HOOK_OUTPUT" | grep -q "_discovery"; then
    echo "  FAIL: ephemeral sessions shown in hook"; ((FAIL++))
else
    echo "  PASS"; ((PASS++))
fi

waggle stop 2>/dev/null || true

echo ""
echo "Results: $PASS pass, $FAIL fail"
[ "$FAIL" -eq 0 ] || exit 1
```

## Implementation Order

```
Phase A: Invariants (TDD)
  1. Add Go tests to broker_test.go (D1-D5) — D1-D4 should pass already (presence works), D5 should pass
  2. Run: go test ./internal/broker/ -v -run "TestBroker_Sessions" -count=1
  3. If any fail, investigate — presence command already exists

Phase B: Implementation
  4. Create cmd/sessions.go — new Cobra command
  5. go build -o waggle . — verify it compiles
  6. Modify waggle-connect.sh — add connected agents section
  7. Run: go test ./... -race -count=1 -timeout=120s
  8. Run: go vet ./...

Phase C: Smoke Test
  9. Run shell tests
  10. Run live smoke test (below)
```

## POST-TASK: Live Smoke Test

```bash
cd ~/Projects/Claude/waggle && go build -o waggle .
waggle start --foreground &
sleep 1

# Connect two persistent agents (subscribers stay connected)
WAGGLE_AGENT_NAME=alice waggle events subscribe task.events &
ALICE_PID=$!
sleep 0.5
WAGGLE_AGENT_NAME=bob waggle events subscribe task.events &
BOB_PID=$!
sleep 0.5

# Discovery
waggle sessions
# Must show alice and bob (sorted), NOT show _discovery-*

# Hook output
WAGGLE_AGENT_NAME=carol bash ~/.claude/hooks/waggle-connect.sh
# Must show:
# ## Waggle Agent: carol
# ### Connected Agents (2+)
# - alice (online)
# - bob (online)

# Cleanup
kill $ALICE_PID $BOB_PID 2>/dev/null
waggle stop
```

## Cross-Issue Regression

After merging, verify #56 and #57:
```bash
# #57: auto-start
waggle stop 2>/dev/null || true
WAGGLE_AGENT_NAME=test bash ~/.claude/hooks/waggle-connect.sh
waggle status  # broker should be running

# #56: custom events
waggle events subscribe chat.test &
SUB_PID=$!
sleep 1
waggle events publish chat.test '{"msg":"regression"}'
sleep 1
kill $SUB_PID
# Must show full Event

# #58: discovery
waggle sessions  # should work

waggle stop
```

- [ ] Commit: `feat(discovery): expose connected agents via waggle sessions + hook`
