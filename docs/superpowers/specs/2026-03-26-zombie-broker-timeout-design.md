# Zombie Broker Timeout — Design Spec

**Issue:** #65
**Date:** 2026-03-26
**Status:** Approved

## Problem

A zombie broker (process alive, listening on socket, never accepting connections) causes all waggle CLI commands to hang indefinitely. The SessionStart hook runs waggle commands at Claude Code session start — if the broker is zombie, the hook hangs and blocks the entire IDE.

**Root cause:** `client.Connect()` calls `net.Dial("unix", socketPath)` with no timeout. The kernel queues the connection (dial succeeds), but `scanner.Scan()` blocks forever waiting for a response that never comes. `broker.IsRunning()` only checks PID existence (signal 0), so it considers a zombie broker "healthy" and skips auto-start.

**Not the root cause:** The GitHub issue suggested `--help` was affected. Empirical testing proves Cobra v1.10.2 handles `--help` before `PersistentPreRunE` — no fix needed there.

## Design

### 1. Config Defaults

Two new durations in `config.Defaults`:

| Name | Value | Purpose |
|------|-------|---------|
| `ConnectTimeout` | 5s | `net.DialTimeout` + handshake deadline in `connectToBroker()` |
| `HealthCheckTimeout` | 1s | Socket probe timeout in `broker.IsResponding()` |

### 2. client.Connect() — dial timeout

Replace `net.Dial` with `net.DialTimeout(socketPath, timeout)`. Expose the underlying `net.Conn` so callers can set/clear deadlines for the handshake phase.

**API change:**
```go
// Before
func Connect(socketPath string) (*Client, error)

// After
func Connect(socketPath string, timeout time.Duration) (*Client, error)
```

The `Client` struct gains a `SetDeadline` method (already exists) and the caller manages deadlines.

### 3. connectToBroker() — deadline-scoped handshake

In `cmd/root.go`, `connectToBroker()` changes to:

1. `client.Connect(paths.Socket, ConnectTimeout)` — timed dial
2. `c.SetDeadline(ConnectTimeout)` — deadline for handshake Send/Receive
3. Send CONNECT command, receive response
4. `c.SetDeadline(time.Time{})` — **clear deadline** so streaming commands work
5. Return client

This is critical: streaming commands (`listen`, `events subscribe`) enter long-running read loops after `connectToBroker()`. The deadline must be cleared before they start streaming, otherwise they'd timeout after 5s.

**Fallback cleanup on timeout:** If `connectToBroker()` fails with a timeout error (dial or handshake), it cleans up the socket and PID files before returning the error. This handles the case where `IsResponding()` gave a false positive (e.g., zombie's backlog was full on probe but freed a slot before the real connect). The next command invocation will detect no broker and auto-start a fresh one.

**Retry within same invocation:** If `connectToBroker()` fails with a timeout and cleanup succeeds, `PersistentPreRunE` retries the auto-start flow once (cleanup → start fresh broker → reconnect). This ensures the first command after a zombie succeeds rather than requiring the user to run it twice.

### 4. broker.IsResponding() — zombie detection

New function in `internal/broker/lifecycle.go`:

```go
func IsResponding(socketPath string, timeout time.Duration) bool
```

Performs a dial+send+read probe:

1. `net.DialTimeout("unix", socketPath, timeout)` — connect to socket
2. Set read/write deadline to `timeout`
3. Write a minimal JSON request: `{"cmd":"status"}\n`
4. Read response with deadline
5. Close connection immediately

If any step fails or times out → return false (zombie or dead). If a response is received → return true (healthy).

**Why send+read, not dial-only:** `net.Dial` succeeds against a zombie if the kernel's listen backlog has space — the kernel accepts the connection independently of the application calling `Accept()`. This was verified empirically: dial-only gives false positives on zombie brokers. The probe must verify the broker actually processes requests, not just that the kernel queued a connection.

**Side effects:** The probe sends `{"cmd":"status"}` which is in the broker's `noSessionRequired` set (router.go:23) — it does not require a CONNECT handshake. The broker creates an unnamed session (`s.name == ""`), and when the probe closes, `readLoop()` returns and `cleanup()` runs via `cleanupOnce.Do()`. Since `name` is empty, cleanup skips lock/task/session teardown (session.go:101). No persistent state is accumulated, even under high-frequency probing.

**Protocol dependency:** The probe relies on `CmdStatus` existing in the broker's command set and being in `noSessionRequired`. This is verified: `protocol.CmdStatus = "status"` (codes.go:23) and `noSessionRequired[CmdStatus] = true` (router.go:23).

### 5. PersistentPreRunE flow

```
IsRunning(PID)?
  → false → CleanupStale() → auto-start
  → true  → IsResponding(socket, 1s)?
              → true  → skip auto-start, proceed to command
              → false → log warning to stderr with zombie PID
                       → remove socket + PID files
                       → auto-start fresh broker
```

The warning message:
```
waggle: unresponsive broker (PID %d) detected, starting fresh instance
```

### 6. No changes to

- **`--help` handling** — Cobra already handles it before PersistentPreRunE
- **Hook (`waggle-connect.sh`)** — fix is in the binary, hook's `timeout` wrappers are defense-in-depth
- **`brokerIndependent` allowlist** — unchanged
- **Protocol** — no wire format changes

## Invariants

These must hold after the fix:

1. **No command hangs >ConnectTimeout on zombie broker** — any command that connects to a zombie fails or auto-recovers within 5s
2. **Auto-recovery from zombie** — first command after zombie: detects zombie (~1s probe), cleans up, starts fresh broker (~1s), retries connection within same invocation. Total ~2-3s, then normal. **Edge case:** if zombie holds a SQLite write lock, the new broker's first write may fail with SQLITE_BUSY after `busy_timeout` (5s). This is self-healing — the zombie's lock is released when it eventually exits.
3. **Streaming commands work beyond ConnectTimeout** — `waggle listen` and `waggle events subscribe` can stream messages for hours after handshake
4. **Healthy broker: negligible overhead** — when broker is healthy, `IsResponding()` completes in low milliseconds (local Unix socket dial+send+read round-trip), no user-visible latency
5. **No process killed** — zombie detection never sends signals to other processes. Cleanup is file-only (socket, PID).
6. **--help always works** — from any directory, with or without broker, with or without git. Already true, must not regress.
7. **Hook completes <3s** — SessionStart hook must not block Claude Code. Already mostly true via timeout wrappers; connect timeout ensures no infinite hang on the `waggle listen &` call.

## Test Plan

### Smoke tests (reproduce the exact symptoms)

These reproduce the user's actual experience:

1. **Zombie broker hangs CLI** (before fix, MUST fail): Create zombie broker (listen, never accept, correct socket path), run `waggle status --no-auto-start` → verify it hangs (timeout >5s)
2. **Zombie broker hangs hook** (before fix, MUST fail): Same zombie, run the SessionStart hook → verify it takes >10s or hangs

### Invariant tests (must pass after fix)

3. **Zombie → fail fast**: Zombie broker + `waggle status --no-auto-start` → exits within ConnectTimeout with error, not hang
4. **Zombie → auto-recovery**: Zombie broker + `waggle sessions` (auto-start enabled) → detects zombie, starts fresh broker, returns valid JSON
5. **Zombie → warning logged**: Same as #4, verify stderr contains "unresponsive broker (PID %d)"
6. **Healthy broker unaffected**: Normal broker + `waggle sessions` → returns data, no warnings, completes in <1s
7. **Streaming survives past timeout**: Connect to healthy broker, stream messages via `listen` for longer than ConnectTimeout (e.g., 7s), verify messages received
8. **--help from /tmp**: `cd /tmp && waggle listen --help` → exit 0, prints help (regression guard)
9. **All subcommands --help from /tmp**: Every subcommand's --help works from non-git dir (regression guard)
10. **IsResponding returns true for healthy broker**: Unit test — create real broker, probe it → true
11. **IsResponding returns false for zombie**: Unit test — create zombie socket, probe it → false
12. **IsResponding returns false for missing socket**: Unit test — probe nonexistent path → false

### Unit tests

13. **ConnectWithTimeout dial timeout**: Zombie socket + short timeout → error within timeout
14. **ConnectWithTimeout success**: Healthy broker + timeout → connects normally
15. **Deadline cleared after handshake**: After connectToBroker(), verify conn has no deadline (can read after ConnectTimeout)

## Risks

- **SQLite WAL lock**: Zombie may hold SQLite db open. WAL mode allows concurrent readers. Writes use `busy_timeout=5s` — if zombie holds a write lock (unlikely), new broker gets SQLITE_BUSY after 5s, not infinite hang. Acceptable degradation.
- **Orphan process**: Zombie is not killed, wastes ~few MB. Negligible. User warned via stderr.
- **False positive probe**: Broker under heavy load might be slow to respond. `HealthCheckTimeout=1s` is generous for a local Unix socket round-trip (normal: low milliseconds). If a broker genuinely takes >1s to respond to a status request, it has bigger problems.
- **Startup race (pre-existing, out of scope)**: Multiple concurrent waggle processes can both detect zombie/dead broker and race to start a new one. This is a pre-existing issue in `PersistentPreRunE` (no locking around auto-start). The hook mitigates this with `mkdir`-based locking. This fix does not make the race worse — worth a separate issue.
- **Hook timeout budget**: The recovery path (~2-3s) is tight against the hook's `timeout 2` wrappers. The hook's commands use `--no-auto-start` (no recovery path), so only the backgrounded `waggle listen` at hook line 105 would hit the recovery path. Since it's backgrounded, it doesn't block the hook. The hook itself is safe.
- **CleanupStale idempotency**: `CleanupStale` already handles missing files gracefully (checks `os.Stat` before `os.Remove`). The retry flow in PersistentPreRunE can safely call it after connectToBroker already cleaned up.
- **SetDeadline**: `Client` already has a `SetDeadline` method that delegates to `conn.SetDeadline`. No new method needed — the spec uses the existing API.
