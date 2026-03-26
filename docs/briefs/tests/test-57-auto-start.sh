#!/bin/bash
set -euo pipefail

# Set WAGGLE_ROOT so tests work from any directory
export WAGGLE_ROOT="${HOME}"

PASS=0
FAIL=0

# Helper
assert_broker_running() {
    waggle --no-auto-start status >/dev/null 2>&1
}
assert_broker_stopped() {
    ! waggle --no-auto-start status >/dev/null 2>&1
}
stop_broker() {
    pkill -f "waggle.*--foreground" 2>/dev/null || true
    sleep 1
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
PID1=$(pgrep -f "waggle.*--foreground" | head -1)
WAGGLE_AGENT_NAME=test-$$ bash ~/.claude/hooks/waggle-connect.sh
PID2=$(pgrep -f "waggle.*--foreground" | head -1)
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
BROKER_COUNT=$(pgrep -f "waggle.*--foreground" | wc -l | tr -d ' ')
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

# test_no_waggle — A6
echo "TEST: no_waggle"
stop_broker
OUTPUT=$(PATH="/usr/bin:/bin" WAGGLE_AGENT_NAME=test-$$ bash ~/.claude/hooks/waggle-connect.sh 2>&1) || true
if [ -z "$OUTPUT" ]; then
    echo "  PASS"; ((PASS++))
else
    echo "  FAIL: hook produced output without waggle on PATH"; ((FAIL++))
fi

# test_any_directory — A7
echo "TEST: any_directory"
stop_broker
(cd /tmp && WAGGLE_AGENT_NAME=test-$$ bash ~/.claude/hooks/waggle-connect.sh)
# When hook runs from /tmp (non-git), it uses WAGGLE_ROOT=$HOME
# So we need to check status with the same WAGGLE_ROOT
if (cd /tmp && WAGGLE_ROOT="$HOME" waggle --no-auto-start status >/dev/null 2>&1); then
    echo "  PASS"; ((PASS++))
else
    echo "  FAIL: broker not running after hook from /tmp"; ((FAIL++))
fi
stop_broker

echo ""
echo "Results: $PASS pass, $FAIL fail"
[ "$FAIL" -eq 0 ] || exit 1

