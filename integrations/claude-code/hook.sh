#!/bin/bash
# Canonical Claude Code hook source lives in integrations/claude-code/.
# The identical copy under internal/install/claude-code/ exists only so go:embed
# can package the integration assets for `waggle install claude-code`.
# waggle-connect.sh — safe SessionStart hook for Claude Code
# Registers the Claude session with the machine runtime and surfaces unread
# runtime records. Silent exit (≤3s best effort) if waggle is unavailable.

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

# Bootstrap this Claude session into the waggle mesh.
# The adapter bootstrap command is the single authoritative path for tool registration.
OUTPUT=$($TIMEOUT_CMD 3 waggle adapter bootstrap claude-code --format markdown 2>/dev/null) || OUTPUT=""
if [ -n "$OUTPUT" ]; then
    echo ""
    echo "$OUTPUT"
    echo ""
fi
