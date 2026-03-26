#!/bin/bash
# waggle-heartbeat.sh — background heartbeat for claimed tasks
# Usage: waggle-heartbeat.sh <task_id> <claim_token> [interval_seconds]
# Runs until heartbeat fails (task completed/failed/lease lost) or killed.

set -euo pipefail

TASK_ID="${1:?task_id required}"
CLAIM_TOKEN="${2:?claim_token required}"
INTERVAL="${3:-120}"  # default 2 minutes (lease is 5 minutes)

while true; do
    sleep "$INTERVAL"
    if ! waggle task heartbeat "$TASK_ID" --token "$CLAIM_TOKEN" 2>/dev/null; then
        # Heartbeat failed — task completed, failed, or lease lost
        exit 0
    fi
done

