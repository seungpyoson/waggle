#!/bin/bash
# Disproves findings 3722f4bf and 23dd9fce: rmdir(2) is atomic —
# os.Remove on a non-empty directory returns ENOTEMPTY, no signal loss possible.
set -euo pipefail
cd "$(dirname "$0")/../.."
go test ./internal/runtime/ -count=1 -run "TestPruneStaleSignals_Rmdir|TestPruneStaleSignals_EmptyDir" -v
