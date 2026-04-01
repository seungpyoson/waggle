#!/bin/bash
# Proves finding e12e0196 is FIXED: hookFiles is the single source of truth
# for both install and uninstall — no separate lists to drift.
set -euo pipefail
cd "$(dirname "$0")/../.."
go test ./internal/install/ -count=1 -run "TestInstall|TestUninstall" -v
