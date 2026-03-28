#!/bin/bash
# Canonical Claude Code hook source lives in integrations/claude-code/.
# The identical copy under internal/install/claude-code/ exists only so go:embed
# can package the integration assets for `waggle install claude-code`.
# waggle-connect.sh — safe SessionStart hook for Claude Code
# Registers the Claude session with the machine runtime and surfaces unread
# runtime records. Silent exit (<2s best effort) if waggle is unavailable.

set -euo pipefail

TIMEOUT_CMD="timeout"
if command -v gtimeout >/dev/null 2>&1; then
    TIMEOUT_CMD="gtimeout"
fi

command -v waggle >/dev/null 2>&1 || exit 0

# Resolve project identity the same way waggle does elsewhere.
if git rev-parse --git-common-dir >/dev/null 2>&1; then
    PROJECT_ID=$(git rev-list --max-parents=0 HEAD 2>/dev/null | sort | head -1)
    if [ -n "$PROJECT_ID" ]; then
        export WAGGLE_PROJECT_ID="$PROJECT_ID"
    elif [ -n "${HOME:-}" ]; then
        export WAGGLE_ROOT="${HOME}"
    fi
elif [ -n "${HOME:-}" ]; then
    export WAGGLE_ROOT="${HOME}"
fi

AGENT_NAME="${WAGGLE_AGENT_NAME:-claude-$$}"

# Ensure the machine runtime is available, then register this Claude session.
$TIMEOUT_CMD 2 waggle runtime start >/dev/null 2>&1 || true
$TIMEOUT_CMD 2 waggle runtime watch "$AGENT_NAME" --source claude-session-start >/dev/null 2>&1 || true

UNREAD=$($TIMEOUT_CMD 2 waggle runtime pull "$AGENT_NAME" 2>/dev/null) || UNREAD=""
UNREAD_COUNT=0
if [ -n "$UNREAD" ]; then
    UNREAD_COUNT=$(echo "$UNREAD" | jq -r '.records | length' 2>/dev/null || echo "0")
fi

if [ "$UNREAD_COUNT" != "0" ]; then
    echo ""
    echo "## Waggle Agent: ${AGENT_NAME}"
    echo ""
    echo "### Unread Messages (${UNREAD_COUNT})"
    echo "$UNREAD" | jq -r '.records[] | "- **\(.from_name):** \(.body)"' 2>/dev/null || true
    echo ""
    echo "Use \`/waggle\` commands to interact. Agent name: \`${AGENT_NAME}\`"
fi
