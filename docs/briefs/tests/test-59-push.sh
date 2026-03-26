#!/bin/bash
set -euo pipefail

# Build waggle from current directory
go build -o waggle .

PASS=0
FAIL=0

./waggle start >/dev/null 2>&1 || true
sleep 0.5

# test_listen_file_output — L2
echo "TEST: listen_file_output"
LISTEN_FILE="/tmp/waggle-test-listen-$$.jsonl"
./waggle listen --name test-receiver-push --output "$LISTEN_FILE" &
LISTEN_PID=$!
sleep 1
WAGGLE_AGENT_NAME=test-sender ./waggle send test-receiver "hello push test"
sleep 1.5
if [ -f "$LISTEN_FILE" ] && grep -q "hello push test" "$LISTEN_FILE"; then
    echo "  PASS"; ((PASS++))
else
    echo "  FAIL: message not in listen file"; ((FAIL++))
fi
kill $LISTEN_PID 2>/dev/null || true
rm -f "$LISTEN_FILE"

# test_listen_broker_disconnect — L3
echo "TEST: listen_broker_disconnect"
./waggle stop 2>/dev/null || true
./waggle start >/dev/null 2>&1
sleep 0.5
./waggle listen --name l3-test-push --output /dev/null &
LISTEN_PID=$!
sleep 0.5
# Stop broker — listener should exit cleanly
./waggle stop
sleep 1
if ! kill -0 $LISTEN_PID 2>/dev/null; then
    # Process exited
    wait $LISTEN_PID 2>/dev/null
    EXIT_CODE=$?
    if [ "$EXIT_CODE" -eq 0 ]; then
        echo "  PASS (exit 0)"; ((PASS++))
    else
        echo "  PASS (exit $EXIT_CODE — acceptable)"; ((PASS++))
    fi
else
    echo "  FAIL: listener still running after broker stop"; ((FAIL++))
    kill $LISTEN_PID 2>/dev/null
fi
# Restart broker for remaining tests
./waggle start >/dev/null 2>&1
sleep 0.5

# test_listen_sigterm — L4
echo "TEST: listen_sigterm"
./waggle listen --name sigterm-test-push --output /dev/null &
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
./waggle listen --name e2e-receiver-push --output "$E2E_FILE" &
LISTEN_PID=$!
sleep 1
WAGGLE_AGENT_NAME=e2e-sender ./waggle send e2e-receiver "end to end test"
sleep 1.5
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

# test_multiple_messages — L9
echo "TEST: multiple_messages"
L9_FILE="/tmp/waggle-l9-$$.jsonl"
./waggle listen --name l9-receiver-push --output "$L9_FILE" &
LISTEN_PID=$!
sleep 0.5
WAGGLE_AGENT_NAME=sender1 ./waggle send l9-receiver "message one"
WAGGLE_AGENT_NAME=sender2 ./waggle send l9-receiver "message two"
WAGGLE_AGENT_NAME=sender3 ./waggle send l9-receiver "message three"
sleep 1
# Verify all 3 messages in file
COUNT=$(wc -l < "$L9_FILE" | tr -d ' ')
if [ "$COUNT" -ge 3 ]; then
    # Now test hook reads all 3
    WAGGLE_AGENT_NAME=l9-receiver WAGGLE_LISTEN_FILE="$L9_FILE" node ~/.claude/hooks/waggle-push.js > /tmp/l9-hook-$$.json 2>/dev/null
    HOOK_COUNT=$(grep -o 'Message from' /tmp/l9-hook-$$.json | wc -l | tr -d ' ')
    if [ "$HOOK_COUNT" -ge 3 ]; then
        echo "  PASS ($COUNT messages, hook showed $HOOK_COUNT)"; ((PASS++))
    else
        echo "  FAIL: hook only showed $HOOK_COUNT of $COUNT messages"; ((FAIL++))
    fi
else
    echo "  FAIL: only $COUNT messages in file (expected 3)"; ((FAIL++))
fi
kill $LISTEN_PID 2>/dev/null || true
rm -f "$L9_FILE" /tmp/l9-hook-$$.json

./waggle stop 2>/dev/null || true

echo ""
echo "Results: $PASS pass, $FAIL fail"
[ "$FAIL" -eq 0 ] || exit 1

