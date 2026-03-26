# Task 59 Brief: Surface Pushed Messages in Agent Sessions via Persistent Listener

**Branch:** `feat/59-push-messages`
**Goal:** Messages sent to an agent appear in its Claude Code session automatically on the next tool call. No manual inbox check.

**Files to create:**
- `cmd/listen.go` — new CLI command (persistent connection, outputs pushed messages)
- `~/.claude/hooks/waggle-push.js` — PreToolUse hook (reads pushed messages, injects via additionalContext)

**Files to modify:**
- `internal/client/client.go` — add `ReadMessages()` method that reads pushed `Response` objects (not Events)
- `~/.claude/hooks/waggle-connect.sh` — spawn `waggle listen` in background on session start

**Files to verify (must NOT change):**
- `internal/broker/router.go` — push delivery in `handleSend` (lines 554-583) already works
- `internal/protocol/message.go` — no struct changes

**Dependencies:** #57 (auto-start broker), #58 (agent discovery — agents need to find each other to send messages)

---

## Architecture

```
Session Start:
  waggle-connect.sh spawns: waggle listen --name claude-87952 --output /tmp/waggle-claude-87952.jsonl &

During session (every tool call):
  waggle-push.js (PreToolUse hook):
    1. Reads /tmp/waggle-claude-87952.jsonl
    2. If new lines exist since last read → inject via additionalContext
    3. Truncates file after reading (clear-after-read)

Message flow:
  Session A: waggle send claude-87952 "review is done"
    → broker handleSend pushes to claude-87952's listen connection
    → waggle listen writes to /tmp/waggle-claude-87952.jsonl
    → Next tool call: waggle-push.js reads file, injects:
      "📨 Message from session-A: review is done"
    → Claude sees it in context and can act on it
```

## What to build

### 1. `waggle listen` command (cmd/listen.go)

Keeps a persistent connection to the broker. Receives pushed messages and writes them as JSON lines to stdout (or a file via `--output`).

```go
var listenCmd = &cobra.Command{
    Use:   "listen",
    Short: "Listen for pushed messages (persistent connection)",
    RunE: func(cmd *cobra.Command, args []string) error {
        name, err := resolveAgentName(cmd)
        // ... connect with name ...
        // ... read pushed messages in a loop ...
        // ... write JSON lines to output (stdout or --output file) ...
    },
}
```

**Wire protocol:** Pushed messages from `handleSend` arrive as `protocol.Response` with `Data` containing:
```json
{"ok":true,"data":{"type":"message","id":7,"from":"alice","body":"hello","sent_at":"2026-..."}}
```

The listener must:
1. Connect with the agent name
2. Enter a read loop (using `client.Receive()` or a new `ReadMessages()`)
3. For each pushed message, extract the data and write a JSON line:
   ```json
   {"id":7,"from":"alice","body":"hello","sent_at":"2026-...","received_at":"2026-..."}
   ```
4. If `--output <file>` is specified, append to file instead of stdout
5. Handle signals (SIGTERM, SIGINT) for clean shutdown
6. Exit cleanly when broker disconnects

**Important:** The listen connection will also receive the initial `connect` response. The read loop must skip non-message responses (check for `data.type == "message"`).

### 2. `ReadMessages()` in client.go

Add a method that reads from the scanner and filters for pushed message responses:

```go
func (c *Client) ReadMessages() (<-chan PushedMessage, error) {
    ch := make(chan PushedMessage)
    go func() {
        defer close(ch)
        for c.scanner.Scan() {
            var resp Response
            if err := json.Unmarshal(c.scanner.Bytes(), &resp); err != nil {
                continue
            }
            if !resp.OK || len(resp.Data) == 0 {
                continue
            }
            var msg struct {
                Type   string `json:"type"`
                ID     int64  `json:"id"`
                From   string `json:"from"`
                Body   string `json:"body"`
                SentAt string `json:"sent_at"`
            }
            if err := json.Unmarshal(resp.Data, &msg); err != nil || msg.Type != "message" {
                continue
            }
            ch <- PushedMessage{
                ID:     msg.ID,
                From:   msg.From,
                Body:   msg.Body,
                SentAt: msg.SentAt,
            }
        }
    }()
    return ch, nil
}

type PushedMessage struct {
    ID     int64  `json:"id"`
    From   string `json:"from"`
    Body   string `json:"body"`
    SentAt string `json:"sent_at"`
}
```

### 3. SessionStart hook modification (waggle-connect.sh)

After existing logic, spawn the listener in background:

```bash
# 8. Start background listener for push messages
LISTEN_FILE="/tmp/waggle-${AGENT_NAME}.jsonl"
# Kill any existing listener for this agent
pkill -f "waggle listen.*--name ${AGENT_NAME}" 2>/dev/null || true
# Start fresh listener
waggle listen --name "${AGENT_NAME}-listener" --output "$LISTEN_FILE" &
LISTEN_PID=$!
# Store PID for cleanup
echo "$LISTEN_PID" > "/tmp/waggle-${AGENT_NAME}.pid"
```

**Note:** The listener connects with `<name>-listener` so it doesn't conflict with the agent's own session name. Messages sent to `<name>` are pushed to all sessions with that name... wait, no — messages are pushed to `broker.sessions[req.Name]` which is a single session. The listener needs to connect with the SAME name as the agent, or messages won't be pushed to it.

**Resolution:** The listener connects with the agent's name. But then the agent's own CLI commands (waggle send, waggle inbox) will use short-lived connections with the same name — and `handleConnect` will register the new session, replacing the listener's session in `broker.sessions`. This means the listener loses its registration.

**Better approach:** The listener IS the agent's persistent connection. All other CLI commands from this agent use a separate ephemeral name (e.g., `<name>-cli-<pid>`). OR: modify the hook to not connect with the agent name for one-shot commands.

**Simplest approach for MVP:** The listener connects as the agent name. One-shot CLI commands (send, inbox) connect with `<name>-cmd-<pid>`. The hook sets `WAGGLE_AGENT_NAME=<name>` for the AI session (existing behavior) but one-shot commands internally append a suffix. This is a bigger change.

**Actually simplest:** Don't change one-shot commands. Accept that when a one-shot command connects with the same name, it temporarily replaces the listener in `broker.sessions`. The listener's connection stays open — it just won't receive pushes during the brief moment the CLI command is running. Since CLI commands take <100ms, this is acceptable for MVP. The listener re-registers on the next connect? No — there's no re-registration.

**Correct simplest approach:** The listener connects with `<name>-push` as a dedicated push receiver. Messages sent to `<name>` are delivered to `broker.sessions["<name>"]`. But the listener is registered as `<name>-push`, so it won't receive them.

**This is the core design problem.** The broker pushes to exactly one session — `broker.sessions[req.Name]`. The listener must own that session name to receive pushes.

**Solution:** The listener connects as the agent name. One-shot CLI commands connect with a different name (`<name>-cli`). Modify `waggle-connect.sh` to:
- Set `WAGGLE_AGENT_NAME=<name>` for display purposes
- The listener connects as `<name>` (owns the session)
- One-shot commands use `WAGGLE_AGENT_NAME=<name>-cli` internally

This requires changing how `resolveAgentName` works for send/inbox commands. OR: the hook exports a second env var like `WAGGLE_CLI_NAME=<name>-cli`.

**For MVP, the cleanest path:**
1. Listener connects as `<name>` (receives pushes)
2. Send/inbox commands connect as `<name>-cli-<pid>` — add a `--cli` flag or auto-detect
3. The `send` command's `from` field already comes from `s.name` which is set by connect, so sending as `<name>-cli-123` means the "from" field shows that. This is ugly.

**Revised simplest approach:**
1. Listener connects as `<name>`
2. Send command: sender connects as their own name (already works — short-lived, reconnects after listener)
3. After the send command disconnects, the listener's connection is still open but NOT in `broker.sessions` (it was replaced)
4. **Fix: modify broker to support multiple sessions per name** — `broker.sessions` becomes `map[string][]*Session`. Push delivers to ALL sessions with that name.

This is a broker change but a small one. Change `sessions map[string]*Session` to `sessions map[string][]*Session` and update handleConnect, cleanup, handleSend to iterate.

**NO — this is scope creep. For MVP:**
1. The listener connects as `<name>`
2. One-shot commands that need a connection (send, inbox, ack) get a `--from` flag or use a temp name
3. Accept the limitation that the listener temporarily loses registration when a CLI command runs

Actually, re-reading `handleConnect`:
```go
s.name = req.Name
s.broker.mu.Lock()
s.broker.sessions[s.name] = s
s.broker.mu.Unlock()
```

When a CLI command connects with the same name, it OVERWRITES the listener's session in the map. When the CLI command disconnects, cleanup removes the name from the map entirely. The listener is still connected but invisible — it will never receive pushes again.

**This means: the listener and CLI commands CANNOT share the same name.**

**Final design for MVP:**
1. Listener connects as `<name>` — this is the persistent session that receives pushes
2. One-shot CLI commands connect as `<name>-cmd` — different session name
3. `waggle-connect.sh` sets TWO env vars:
   - `WAGGLE_AGENT_NAME=<name>` — used by listener and for display
   - `WAGGLE_CMD_NAME=<name>-cmd` — used by one-shot commands
4. Modify `resolveAgentName()` in CLI to check `WAGGLE_CMD_NAME` first, then `WAGGLE_AGENT_NAME`
5. `waggle send` connects as `<name>-cmd`, but the `from` in the stored message is the sender's connected name. This means messages show "from: claude-87952-cmd" which is ugly.

**Even simpler:** Don't change CLI commands at all. The listener connects as `<name>-push`. Modify `handleSend` to also push to `<name>-push` if that session exists. One line of broker code.

**THIS IS THE DESIGN:**
- Listener connects as `<name>-push`
- `handleSend` checks for both `sessions[recipient]` and `sessions[recipient + "-push"]`
- CLI commands work unchanged — they connect as `<name>` for one-shot operations
- No env var changes, no CLI changes, one small broker change

### Broker modification (router.go — handleSend)

After the existing push block (line 554-583), add:

```go
// Also push to persistent listener (<name>-push) if connected
pushName := req.Name + "-push"
s.broker.mu.RLock()
pushRecipient, pushOnline := s.broker.sessions[pushName]
s.broker.mu.RUnlock()

if pushOnline && pushRecipient != s {
    pushRecipient.writeMu.Lock()
    if err := pushRecipient.enc.Encode(pushMsg); err != nil {
        pushRecipient.writeMu.Unlock()
        log.Printf("session %s: failed to push message %d to %s: %v", s.name, msg.ID, pushName, err)
    } else {
        pushRecipient.writeMu.Unlock()
    }
}
```

### 4. PreToolUse hook (waggle-push.js)

```javascript
#!/usr/bin/env node
// waggle-push.js — PreToolUse hook
// Reads pushed messages from waggle listener and injects via additionalContext

const fs = require('fs');
const path = require('path');

const agentName = process.env.WAGGLE_AGENT_NAME;
if (!agentName) process.exit(0);

const listenFile = `/tmp/waggle-${agentName}.jsonl`;

try {
    if (!fs.existsSync(listenFile)) process.exit(0);

    const content = fs.readFileSync(listenFile, 'utf8').trim();
    if (!content) process.exit(0);

    // Clear the file immediately (atomic: write empty, not unlink)
    fs.writeFileSync(listenFile, '');

    // Parse messages
    const messages = content.split('\n')
        .filter(line => line.trim())
        .map(line => {
            try { return JSON.parse(line); }
            catch { return null; }
        })
        .filter(Boolean);

    if (messages.length === 0) process.exit(0);

    // Format for injection
    const formatted = messages.map(m =>
        `[waggle] Message from ${m.from}: ${m.body}`
    ).join('\n');

    // Output additionalContext
    console.log(JSON.stringify({
        additionalContext: `\n📨 Waggle: ${messages.length} new message(s):\n${formatted}\n`
    }));
} catch (e) {
    // Silent failure — don't block tool calls
    process.exit(0);
}
```

### 5. Hook installation

The PreToolUse hook must be registered in Claude Code settings. Add to `waggle-connect.sh` or document as manual step.

For MVP: document as manual addition to `~/.claude/settings.json`:
```json
{
  "hooks": {
    "PreToolUse": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "node ~/.claude/hooks/waggle-push.js"
          }
        ]
      }
    ]
  }
}
```

Or: extend `waggle install claude-code` (from Task 44) to install this hook.

## Invariants

| ID | Invariant | How to verify | Test name |
|----|-----------|---------------|-----------|
| L1 | `waggle listen` receives pushed messages | Connect listener, send message to it, listener outputs JSON line | TestBroker_ListenReceivesPush |
| L2 | `waggle listen --output <file>` writes to file | Start listener with --output, send message, file contains JSON line | test_listen_file_output |
| L3 | Listener handles broker disconnect gracefully | Start listener, stop broker, listener exits cleanly (exit 0) | test_listen_broker_disconnect |
| L4 | Listener handles SIGTERM gracefully | Start listener, send SIGTERM, listener exits 0 | test_listen_sigterm |
| L5 | Push to `<name>-push` session works | Connect as "alice-push", send to "alice", alice-push receives it | TestBroker_PushToListenerSession |
| L6 | PreToolUse hook reads messages and clears file | Write JSON to listen file, run hook, file is empty after, stdout has additionalContext | test_hook_reads_and_clears |
| L7 | PreToolUse hook is silent when no messages | Empty listen file, run hook, exits 0 with no stdout | test_hook_silent_when_empty |
| L8 | End-to-end: send from A, B sees it on tool call | Full integration: listener + hook + message send | test_e2e_push_message |
| L9 | Multiple messages accumulate correctly | Send 3 messages before hook reads, all 3 appear in additionalContext | test_multiple_messages |
| L10 | `ReadMessages` filters non-message responses | Send a connect response then a push, only push appears in channel | TestClient_ReadMessagesFilters |

## Tests (TDD)

### Go integration tests (add to `internal/broker/broker_test.go`)

```
TestBroker_ListenReceivesPush       — connect "alice-push", send message to "alice", alice-push receives Response with message data
TestBroker_PushToListenerSession    — verify handleSend pushes to both "alice" (if connected) and "alice-push" (if connected)
TestClient_ReadMessagesFilters      — send a mix of Response types to a connection, ReadMessages only emits message-type
```

### Shell tests (`docs/briefs/tests/test-59-push.sh`)

```bash
#!/bin/bash
set -euo pipefail

cd ~/Projects/Claude/waggle && go build -o waggle .

PASS=0
FAIL=0

waggle start >/dev/null 2>&1 || true
sleep 0.5

# test_listen_file_output — L2
echo "TEST: listen_file_output"
LISTEN_FILE="/tmp/waggle-test-listen-$$.jsonl"
waggle listen --name test-receiver-push --output "$LISTEN_FILE" &
LISTEN_PID=$!
sleep 0.5
WAGGLE_AGENT_NAME=test-sender waggle send test-receiver "hello push test"
sleep 1
if [ -f "$LISTEN_FILE" ] && grep -q "hello push test" "$LISTEN_FILE"; then
    echo "  PASS"; ((PASS++))
else
    echo "  FAIL: message not in listen file"; ((FAIL++))
fi
kill $LISTEN_PID 2>/dev/null || true
rm -f "$LISTEN_FILE"

# test_listen_sigterm — L4
echo "TEST: listen_sigterm"
waggle listen --name sigterm-test-push --output /dev/null &
LISTEN_PID=$!
sleep 0.5
kill -TERM $LISTEN_PID
wait $LISTEN_PID 2>/dev/null
EXIT_CODE=$?
if [ "$EXIT_CODE" -eq 0 ] || [ "$EXIT_CODE" -eq 143 ]; then
    echo "  PASS (exit $EXIT_CODE)"; ((PASS++))
else
    echo "  FAIL: exit code $EXIT_CODE"; ((FAIL++))
fi

# test_hook_reads_and_clears — L6
echo "TEST: hook_reads_and_clears"
HOOK_FILE="/tmp/waggle-hook-test-$$.jsonl"
echo '{"id":1,"from":"alice","body":"test message","sent_at":"2026-01-01T00:00:00Z"}' > "$HOOK_FILE"
WAGGLE_AGENT_NAME=hook-test-$$ WAGGLE_LISTEN_FILE="$HOOK_FILE" node ~/.claude/hooks/waggle-push.js > /tmp/hook-output-$$.json 2>/dev/null
HOOK_OUTPUT=$(cat /tmp/hook-output-$$.json)
FILE_AFTER=$(cat "$HOOK_FILE")
if echo "$HOOK_OUTPUT" | grep -q "additionalContext" && [ -z "$FILE_AFTER" ]; then
    echo "  PASS"; ((PASS++))
else
    echo "  FAIL: hook output='$HOOK_OUTPUT', file after='$FILE_AFTER'"; ((FAIL++))
fi
rm -f "$HOOK_FILE" /tmp/hook-output-$$.json

# test_hook_silent_when_empty — L7
echo "TEST: hook_silent_when_empty"
WAGGLE_AGENT_NAME=empty-test-$$ node ~/.claude/hooks/waggle-push.js > /tmp/hook-empty-$$.json 2>/dev/null
if [ ! -s /tmp/hook-empty-$$.json ]; then
    echo "  PASS"; ((PASS++))
else
    echo "  FAIL: hook produced output on empty file"; ((FAIL++))
fi
rm -f /tmp/hook-empty-$$.json

# test_e2e_push_message — L8
echo "TEST: e2e_push_message"
E2E_FILE="/tmp/waggle-e2e-$$.jsonl"
waggle listen --name e2e-receiver-push --output "$E2E_FILE" &
LISTEN_PID=$!
sleep 0.5
WAGGLE_AGENT_NAME=e2e-sender waggle send e2e-receiver "end to end test"
sleep 1
if [ -f "$E2E_FILE" ] && grep -q "end to end test" "$E2E_FILE"; then
    # Now test the hook reads it
    WAGGLE_AGENT_NAME=e2e-receiver WAGGLE_LISTEN_FILE="$E2E_FILE" node ~/.claude/hooks/waggle-push.js > /tmp/e2e-hook-$$.json 2>/dev/null
    if grep -q "end to end test" /tmp/e2e-hook-$$.json; then
        echo "  PASS"; ((PASS++))
    else
        echo "  FAIL: hook didn't surface the message"; ((FAIL++))
    fi
else
    echo "  FAIL: message not in listen file"; ((FAIL++))
fi
kill $LISTEN_PID 2>/dev/null || true
rm -f "$E2E_FILE" /tmp/e2e-hook-$$.json

waggle stop 2>/dev/null || true

echo ""
echo "Results: $PASS pass, $FAIL fail"
[ "$FAIL" -eq 0 ] || exit 1
```

## Implementation Order

```
Phase A: Invariants (TDD)
  1. Add Go tests (L1, L5, L10) — must fail
  2. Add shell test script — L2, L4, L6-L9 must fail (commands don't exist yet)
  3. Log failures

Phase B: Broker Change
  4. Modify handleSend in router.go — add push to <name>-push session
  5. Run Go tests — L5 must pass
  6. Run: go test ./internal/broker/ -race -count=1

Phase C: Client + CLI
  7. Add ReadMessages() to client.go
  8. Create cmd/listen.go
  9. go build -o waggle .
  10. Run shell tests L2, L4 — must pass

Phase D: Hook
  11. Create waggle-push.js
  12. Run shell tests L6, L7 — must pass

Phase E: Integration
  13. Modify waggle-connect.sh — spawn listener in background
  14. Run shell test L8 (e2e) — must pass
  15. Run full suite: go test ./... -race -count=1 -timeout=120s
  16. go vet ./...

Phase F: Smoke Test
  17. Live test (below)
```

## POST-TASK: Live Smoke Test

```bash
cd ~/Projects/Claude/waggle && go build -o waggle .

# Clean state
waggle stop 2>/dev/null || true
rm -f /tmp/waggle-smoke-*.jsonl

# Start broker
waggle start --foreground &
sleep 1

# Start listener for agent "bob"
waggle listen --name bob-push --output /tmp/waggle-smoke-bob.jsonl &
BOB_LISTEN_PID=$!
sleep 0.5

# Alice sends to bob
WAGGLE_AGENT_NAME=alice waggle send bob "the auth module is ready for review"
sleep 1

# Verify listener captured it
cat /tmp/waggle-smoke-bob.jsonl
# Must contain: {"id":...,"from":"alice","body":"the auth module is ready for review",...}

# Verify hook would surface it
WAGGLE_AGENT_NAME=bob WAGGLE_LISTEN_FILE=/tmp/waggle-smoke-bob.jsonl node ~/.claude/hooks/waggle-push.js
# Must output: {"additionalContext":"...Message from alice: the auth module is ready for review..."}

# Verify file is cleared after hook read
cat /tmp/waggle-smoke-bob.jsonl
# Must be empty

# Cleanup
kill $BOB_LISTEN_PID 2>/dev/null
waggle stop
```

## Cross-Issue Regression

```bash
waggle stop 2>/dev/null || true

# #57: auto-start
WAGGLE_AGENT_NAME=test bash ~/.claude/hooks/waggle-connect.sh
waggle status  # must be running

# #56: custom events
waggle events subscribe chat.test &
SUB_PID=$!; sleep 1
waggle events publish chat.test '{"msg":"regression"}'
sleep 1; kill $SUB_PID
# Must show full Event with non-empty fields

# #58: discovery
waggle sessions  # must list connected agents

# #59: push messages (this task)
waggle listen --name regression-push --output /tmp/waggle-regression.jsonl &
LP=$!; sleep 0.5
WAGGLE_AGENT_NAME=sender waggle send regression "full regression"
sleep 1
grep "full regression" /tmp/waggle-regression.jsonl
# Must find the message
kill $LP 2>/dev/null

waggle stop
rm -f /tmp/waggle-regression.jsonl
```

- [ ] Commit: `feat(push): surface pushed messages in agent sessions via persistent listener`
