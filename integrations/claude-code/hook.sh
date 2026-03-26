#!/bin/bash
# waggle-connect.sh — SessionStart hook for Claude Code
# Outputs markdown context if there are pending messages or tasks.
# Silent exit (<2s) if waggle not available or no pending work.

set -euo pipefail

# Detect timeout command (gtimeout on macOS, timeout on Linux)
TIMEOUT_CMD="timeout"
if command -v gtimeout >/dev/null 2>&1; then
    TIMEOUT_CMD="gtimeout"
fi

# 1. Check if waggle is available
command -v waggle >/dev/null 2>&1 || exit 0

# 2. Check if broker is running (waggle status is the cheapest check)
STATUS=$($TIMEOUT_CMD 2 waggle status 2>/dev/null) || exit 0

# 3. Resolve agent name
AGENT_NAME="${WAGGLE_AGENT_NAME:-claude-$$}"

# 4. Check inbox (2s timeout)
INBOX=$($TIMEOUT_CMD 2 env WAGGLE_AGENT_NAME="$AGENT_NAME" waggle inbox 2>/dev/null) || INBOX=""
INBOX_COUNT=0
if [ -n "$INBOX" ]; then
    INBOX_COUNT=$(echo "$INBOX" | jq -r '.data | length' 2>/dev/null || echo "0")
fi

# 5. Check pending tasks (2s timeout)
TASKS=$($TIMEOUT_CMD 2 waggle task list --state pending 2>/dev/null) || TASKS=""
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

