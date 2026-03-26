#!/bin/bash
set -euo pipefail

PASS=0
FAIL=0

# Ensure broker is running
waggle stop 2>/dev/null || true
sleep 0.5
waggle start >/dev/null 2>&1
sleep 0.5

# test_sessions_command — D1
echo "TEST: sessions_command"
waggle events subscribe task.events >/dev/null 2>&1 &
SUB_PID=$!
sleep 0.5
OUTPUT=$(waggle sessions 2>/dev/null)
kill $SUB_PID 2>/dev/null || true
# Check that at least one non-ephemeral session is listed
if echo "$OUTPUT" | jq -e '.data[] | select(.name | startswith("_") | not)' >/dev/null 2>&1; then
    echo "  PASS"; ((PASS++))
else
    echo "  FAIL: no non-ephemeral sessions in output"; ((FAIL++))
fi

# test_hook_shows_agents — D6
echo "TEST: hook_shows_agents"
waggle events subscribe task.events >/dev/null 2>&1 &
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

