# Task 57 Brief: Auto-Start Broker from SessionStart Hook

**Branch:** `feat/57-auto-start-broker`
**Goal:** The SessionStart hook starts the broker automatically if not running. Users never manually run `waggle start`.

**Files to modify:**
- `~/.claude/hooks/waggle-connect.sh` — add auto-start logic before status check

**Files to verify (must NOT change):**
- `internal/broker/` — no broker changes
- `cmd/start.go` — existing start command unchanged

**Dependencies:** None. Hook-only change.

---

## Root Cause

`waggle-connect.sh` line 18 uses `--no-auto-start` and exits silently if the broker isn't running:
```bash
STATUS=$($TIMEOUT_CMD 2 waggle --no-auto-start status 2>/dev/null) || exit 0
```

This means every new user must know to run `waggle start` before opening a Claude Code session. Non-technical users don't know this.

## What to build

Add auto-start logic to the hook: if `waggle status` fails (broker not running), run `waggle start` in the background, wait for it to be ready, then continue.

**Critical constraints:**
- Hook must remain fast (<3s total even with auto-start)
- Must not start duplicate brokers (race when multiple sessions open simultaneously)
- Must work from any directory (the hook runs in the user's cwd, not waggle project dir)
- `waggle` binary must be on PATH (if not, exit silently — same as today)
- Broker start is backgrounded (`waggle start &`) — hook polls for readiness

**Locking strategy for race prevention:**
Use a lockfile (`~/.waggle/broker.lock`) with `flock` (Linux) or `shlock`/`mkdir` (macOS). The simplest cross-platform approach: `mkdir ~/.waggle/broker-starting 2>/dev/null` — mkdir is atomic. First session creates it, starts broker, removes it. Others see the dir exists and wait.

## Invariants

| ID | Invariant | How to verify | Test name |
|----|-----------|---------------|-----------|
| A1 | Hook starts broker if not running | Kill broker, run hook, broker is running after | test_auto_start_cold |
| A2 | Hook does not start duplicate broker | Broker running, run hook, only one broker process | test_no_duplicate |
| A3 | Hook completes in <3s with auto-start | Time the hook execution with auto-start | test_timing |
| A4 | Multiple simultaneous hooks don't race | Launch 3 hooks in parallel, exactly one broker starts | test_parallel_start |
| A5 | Hook still works when broker is already running (regression) | Start broker, run hook, shows tasks/inbox as before | test_existing_broker |
| A6 | Hook exits silently if waggle not on PATH | Unset PATH, run hook, exits 0 with no output | test_no_waggle |
| A7 | Hook works from any directory (not just waggle project) | cd /tmp, run hook, broker starts | test_any_directory |

## Tests (shell-based — run via bash script)

Create `docs/briefs/tests/test-57-auto-start.sh`:

```bash
#!/bin/bash
set -euo pipefail

PASS=0
FAIL=0

# Helper
assert_broker_running() {
    waggle status >/dev/null 2>&1
}
assert_broker_stopped() {
    ! waggle status >/dev/null 2>&1
}
stop_broker() {
    waggle stop 2>/dev/null || true
    sleep 0.5
}

# test_auto_start_cold — A1
echo "TEST: auto_start_cold"
stop_broker
assert_broker_stopped
WAGGLE_AGENT_NAME=test-$$ bash ~/.claude/hooks/waggle-connect.sh
if assert_broker_running; then
    echo "  PASS"; ((PASS++))
else
    echo "  FAIL: broker not running after hook"; ((FAIL++))
fi
stop_broker

# test_no_duplicate — A2
echo "TEST: no_duplicate"
waggle start
sleep 0.5
PID1=$(cat ~/.waggle/broker.pid 2>/dev/null || pgrep -f "waggle.*start" | head -1)
WAGGLE_AGENT_NAME=test-$$ bash ~/.claude/hooks/waggle-connect.sh
PID2=$(cat ~/.waggle/broker.pid 2>/dev/null || pgrep -f "waggle.*start" | head -1)
if [ "$PID1" = "$PID2" ]; then
    echo "  PASS"; ((PASS++))
else
    echo "  FAIL: broker PID changed ($PID1 → $PID2)"; ((FAIL++))
fi
stop_broker

# test_timing — A3
echo "TEST: timing"
stop_broker
START=$(date +%s)
WAGGLE_AGENT_NAME=test-$$ bash ~/.claude/hooks/waggle-connect.sh
END=$(date +%s)
ELAPSED=$((END - START))
if [ "$ELAPSED" -le 3 ]; then
    echo "  PASS (${ELAPSED}s)"; ((PASS++))
else
    echo "  FAIL: took ${ELAPSED}s (max 3s)"; ((FAIL++))
fi
stop_broker

# test_parallel_start — A4
echo "TEST: parallel_start"
stop_broker
for i in 1 2 3; do
    WAGGLE_AGENT_NAME=test-$$-$i bash ~/.claude/hooks/waggle-connect.sh &
done
wait
BROKER_COUNT=$(pgrep -f "waggle.*serve" | wc -l | tr -d ' ')
if [ "$BROKER_COUNT" -le 1 ]; then
    echo "  PASS ($BROKER_COUNT broker)"; ((PASS++))
else
    echo "  FAIL: $BROKER_COUNT brokers running"; ((FAIL++))
fi
stop_broker

# test_existing_broker — A5 (regression)
echo "TEST: existing_broker_regression"
waggle start
sleep 0.5
waggle task create '{"desc":"regression"}' --type test 2>/dev/null
OUTPUT=$(WAGGLE_AGENT_NAME=test-$$ bash ~/.claude/hooks/waggle-connect.sh)
if echo "$OUTPUT" | grep -q "Pending Tasks"; then
    echo "  PASS"; ((PASS++))
else
    echo "  FAIL: hook did not show pending tasks"; ((FAIL++))
fi
stop_broker

echo ""
echo "Results: $PASS pass, $FAIL fail"
[ "$FAIL" -eq 0 ] || exit 1
```

## Implementation Order

```
Phase A: Test Script
  1. Create test script (above)
  2. Run it — A1 must fail (hook exits when broker not running), A2/A5 should pass
  3. Log failures

Phase B: Implementation
  4. Modify waggle-connect.sh:
     - After checking waggle is on PATH (line 15)
     - Before status check (line 18)
     - Add: try status, if fails → mkdir lock, start broker, poll for ready, rmdir lock
  5. Run test script — all tests must pass

Phase C: Smoke Test
  6. Live test (see below)
```

## Hook Modification (waggle-connect.sh)

Insert between lines 15 and 18. Replace the current status check:

```bash
# 2. Start broker if not running (atomic lock prevents races)
LOCK_DIR="${HOME}/.waggle/broker-starting"
if ! $TIMEOUT_CMD 2 waggle --no-auto-start status >/dev/null 2>&1; then
    # Try to acquire start lock (mkdir is atomic)
    if mkdir "$LOCK_DIR" 2>/dev/null; then
        # We won the race — start the broker
        waggle start >/dev/null 2>&1 &
        # Poll for readiness (max 2s)
        for i in 1 2 3 4; do
            sleep 0.5
            if $TIMEOUT_CMD 1 waggle --no-auto-start status >/dev/null 2>&1; then
                break
            fi
        done
        rmdir "$LOCK_DIR" 2>/dev/null || true
    else
        # Another hook is starting the broker — wait for it
        for i in 1 2 3 4; do
            sleep 0.5
            if $TIMEOUT_CMD 1 waggle --no-auto-start status >/dev/null 2>&1; then
                break
            fi
        done
    fi
fi

# 3. Check broker status (may still fail if start timed out — exit silently)
STATUS=$($TIMEOUT_CMD 2 waggle --no-auto-start status 2>/dev/null) || exit 0
```

## POST-TASK: Live Smoke Test

```bash
# Ensure broker is stopped
waggle stop 2>/dev/null || true
sleep 1

# Verify no broker
waggle status 2>/dev/null && echo "FAIL: broker still running" && exit 1

# Run hook — should auto-start broker
WAGGLE_AGENT_NAME=smoke-$$ bash ~/.claude/hooks/waggle-connect.sh

# Verify broker is now running
waggle status
# Must return {"ok": true, ...}

# Verify hook output (should show agent info)
# Run again — broker already running, should be fast
time WAGGLE_AGENT_NAME=smoke2-$$ bash ~/.claude/hooks/waggle-connect.sh
# Must complete in <1s

waggle stop
```

## Cross-Issue Regression

After merging, verify #56 (if already merged):
```bash
waggle stop 2>/dev/null || true
# Auto-start via hook
WAGGLE_AGENT_NAME=test bash ~/.claude/hooks/waggle-connect.sh
# Custom events (from #56)
waggle events subscribe chat.test &
sleep 1
waggle events publish chat.test '{"msg":"regression"}'
sleep 1
# Must show full Event with non-empty topic/event/ts
waggle stop
```

- [ ] Commit: `feat(hook): auto-start broker from SessionStart hook`
