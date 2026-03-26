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

# 2. Resolve project ID (git root commit or fallback to HOME)
# This ensures all waggle commands use the same project/socket
if git rev-parse --git-common-dir >/dev/null 2>&1; then
    # In a git repo — use root commit as project ID
    PROJECT_ID=$(git rev-list --max-parents=0 HEAD 2>/dev/null | sort | head -1)
    if [ -n "$PROJECT_ID" ]; then
        export WAGGLE_PROJECT_ID="$PROJECT_ID"
    else
        # Empty repo — fall back to HOME
        export WAGGLE_ROOT="${HOME}"
    fi
else
    # Not in a git repo — use HOME as project root
    export WAGGLE_ROOT="${HOME}"
fi

# 3. Start broker if not running (atomic lock prevents races)
LOCK_DIR="${HOME}/.waggle/broker-starting"

# Clean up stale lock (>10s old = previous hook crashed)
if [ -d "$LOCK_DIR" ]; then
    # macOS uses stat -f %m, Linux uses stat -c %Y
    LOCK_MTIME=$(stat -f %m "$LOCK_DIR" 2>/dev/null || stat -c %Y "$LOCK_DIR" 2>/dev/null || echo 0)
    NOW=$(date +%s)
    if [ $((NOW - LOCK_MTIME)) -gt 10 ]; then
        rmdir "$LOCK_DIR" 2>/dev/null || true
    fi
fi

if ! $TIMEOUT_CMD 2 waggle --no-auto-start status >/dev/null 2>&1; then
    # Try to acquire start lock (mkdir is atomic, restrictive permissions)
    if (umask 077 && mkdir "$LOCK_DIR") 2>/dev/null; then
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

# 4. Check broker status (may still fail if start timed out — exit silently)
STATUS=$($TIMEOUT_CMD 2 waggle --no-auto-start status 2>/dev/null) || exit 0

# 5. Resolve agent name
AGENT_NAME="${WAGGLE_AGENT_NAME:-claude-$$}"

# 6. Check inbox (2s timeout)
INBOX=$($TIMEOUT_CMD 2 env WAGGLE_AGENT_NAME="$AGENT_NAME" waggle inbox 2>/dev/null) || INBOX=""
INBOX_COUNT=0
if [ -n "$INBOX" ]; then
    INBOX_COUNT=$(echo "$INBOX" | jq -r '.data | length' 2>/dev/null || echo "0")
fi

# 7. Check pending tasks (2s timeout)
TASKS=$($TIMEOUT_CMD 2 waggle task list --state pending 2>/dev/null) || TASKS=""
TASK_COUNT=0
if [ -n "$TASKS" ]; then
    TASK_COUNT=$(echo "$TASKS" | jq -r '.data | length' 2>/dev/null || echo "0")
fi

# 8. Output context only if there's something to report
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

