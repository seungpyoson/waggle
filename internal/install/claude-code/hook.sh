#!/bin/sh
# SessionStart enrollment is bounded inside the CLI and never supplies a receipt.
if ! command -v waggle >/dev/null 2>&1; then
    printf '%s\n' 'waggle enrollment unavailable: executable not found' >&2
    exit 0
fi
exec waggle enroll claude-code
