# Task 38 Review Fixes — PR #52 Amendments

**Branch:** `feat/38-spawn` (same PR #52)
**Goal:** Fix two classes of problems found during tech lead review. Same branch, same PR.

**Authority:** This spec is authoritative. Do NOT modify `task-38-spawn.md`. Code changes only.

---

## Class 1: Unsanitized user input in command execution [SERIOUS]

### Root cause

`internal/spawn/terminal.go` builds shell and AppleScript commands by concatenating strings. There is no safe command builder. User-controlled values (agent name, project ID, agent command) flow through three separate interpolation points without escaping:

1. **AppleScript injection** (line 72/87) — `fullCmd` interpolated into `"..."` AppleScript strings
2. **Shell value breakout** (line 59) — env values with spaces/quotes break `KEY=VALUE` assignments
3. **pgrep partial match** (line 143) — search pattern `WAGGLE_AGENT_NAME=<name>` matches substrings

Three symptoms, one missing abstraction.

### Design: `SafeCommand` builder

Instead of patching each callsite with ad-hoc escaping, introduce a structured command builder in `internal/spawn/command.go` that takes typed inputs and produces correctly escaped output for each target context.

```go
// command.go — safe command construction for shell and AppleScript contexts

package spawn

// EnvMap is a map of environment variable key-value pairs.
type EnvMap map[string]string

// BuildShellCommand constructs a safe shell command string from env vars and a command.
// All values are single-quote escaped. Keys are validated (alphanumeric + underscore only).
// Returns: "KEY1='val1' KEY2='val2' cmd arg1 arg2"
func BuildShellCommand(env EnvMap, cmd string, args []string) (string, error)

// BuildAppleScript wraps a shell command in AppleScript for Terminal.app or iTerm2.
// The shell command is escaped for embedding inside AppleScript double-quoted strings.
func BuildAppleScript(terminal Terminal, shellCmd string) string

// BuildPgrepPattern constructs an exact-match pattern for finding a process by agent name.
// Prevents partial matches (e.g., "w" matching "worker-1").
func BuildPgrepPattern(name string) string
```

Internal helpers (unexported, tested via the public API):

```go
// shellQuote wraps a value in single quotes with proper escaping.
// Single-quote escape: replace ' with '\'' (end quote, escaped quote, start quote)
func shellQuote(s string) string

// escapeAppleScript escapes a string for use inside AppleScript double-quoted strings.
// Escapes: \ → \\, " → \"
func escapeAppleScript(s string) string

// validateEnvKey checks that an env key contains only [A-Za-z0-9_].
// Rejects keys with =, spaces, or shell metacharacters.
func validateEnvKey(key string) error
```

### Invariants

| ID | Invariant | How to verify |
|----|-----------|---------------|
| C1 | Shell values with spaces are correctly quoted | `BuildShellCommand({"K": "a b"}, ...)` → `K='a b' ...` |
| C2 | Shell values with single quotes are escaped | `BuildShellCommand({"K": "it's"}, ...)` → `K='it'\''s' ...` |
| C3 | Shell values with double quotes are preserved | `BuildShellCommand({"K": `say "hi"`}, ...)` → `K='say "hi"' ...` |
| C4 | AppleScript strings with `"` are escaped | `BuildAppleScript(TerminalApp, `echo "hi"`)` → `... \"hi\" ...` |
| C5 | AppleScript strings with `\` are escaped | `BuildAppleScript(TerminalApp, `path\to`)` → `... path\\to ...` |
| C6 | pgrep pattern for "worker-1" does NOT match "worker-10" | `BuildPgrepPattern("worker-1")` produces pattern that excludes "worker-10" |
| C7 | pgrep pattern for "w" does NOT match "worker" | Same as C6 but for prefix case |
| C8 | Env key with `=` or space is rejected | `validateEnvKey("BAD KEY")` → error |
| C9 | Empty command is rejected | `BuildShellCommand(nil, "", nil)` → error |
| C10 | End-to-end: OpenTab uses SafeCommand (no raw interpolation left) | `grep` for `fmt.Sprintf.*fullCmd` in terminal.go returns 0 matches |

### Tests (TDD — write first, see fail, then implement)

**File:** `internal/spawn/command_test.go`

```
TestBuildShellCommand_Simple        — no special chars, outputs KEY='val' cmd
TestBuildShellCommand_Spaces        — value "a b" → KEY='a b' cmd
TestBuildShellCommand_SingleQuotes  — value "it's" → KEY='it'\''s' cmd
TestBuildShellCommand_DoubleQuotes  — value 'say "hi"' → KEY='say "hi"' cmd
TestBuildShellCommand_EmptyValue    — value "" → KEY='' cmd
TestBuildShellCommand_NoEnv         — nil env, just cmd + args
TestBuildShellCommand_EmptyCmd      — returns error
TestBuildShellCommand_InvalidKey    — key with space/= → error
TestBuildShellCommand_MultipleArgs  — cmd with args properly joined

TestBuildAppleScript_Terminal       — wraps in Terminal.app AppleScript
TestBuildAppleScript_ITerm2         — wraps in iTerm2 AppleScript
TestBuildAppleScript_Quotes         — embedded " escaped to \"
TestBuildAppleScript_Backslash      — embedded \ escaped to \\
TestBuildAppleScript_Both           — " and \ in same string

TestBuildPgrepPattern_Exact         — "worker-1" does not match "worker-10"
TestBuildPgrepPattern_Prefix        — "w" does not match "worker"
TestBuildPgrepPattern_Normal        — "worker-1" matches "WAGGLE_AGENT_NAME=worker-1 claude"
```

### Migration: Update terminal.go to use SafeCommand

After `command.go` and tests pass, refactor `terminal.go`:

1. Replace the inline env string builder (lines 57-61) with `BuildShellCommand`
2. Replace the inline AppleScript `fmt.Sprintf` (lines 72, 87) with `BuildAppleScript`
3. Replace the inline `searchPattern` (line 143) with `BuildPgrepPattern`
4. Delete the old inline code
5. Verify: `grep -n 'fmt.Sprintf.*fullCmd\|envParts\|searchPattern.*WAGGLE' terminal.go` returns 0 matches

---

## Class 2: Protocol field proliferation [MINOR]

### Root cause

New fields get added to `protocol.Request` per-command instead of reusing existing generic fields. `Data json.RawMessage` duplicates the purpose of `Payload json.RawMessage`.

### Fix

1. Remove `Data json.RawMessage` from `protocol.Request` (message.go)
2. Change `cmd/spawn.go` to use `Payload` field instead of `Data`
3. Change `handleSpawnRegister` in `router.go` to unmarshal from `req.Payload` instead of `req.Data`
4. Update `TestBroker_SpawnRegister` and `TestBroker_SpawnRegisterDuplicate` to send `Payload`

### Invariant

| ID | Invariant | How to verify |
|----|-----------|---------------|
| P1 | No `Data` field on Request | `grep 'Data.*json.RawMessage' internal/protocol/message.go` → 0 matches |
| P2 | spawn.register works via Payload | `TestBroker_SpawnRegister` passes |
| P3 | Duplicate spawn.register rejected via Payload | `TestBroker_SpawnRegisterDuplicate` passes |

### Tests

No new tests — existing integration tests cover this after the rename. Run full suite to verify no regressions.

---

## Implementation Order

```
Phase A: Invariants (TDD)
  1. Create internal/spawn/command_test.go with ALL tests from Class 1
  2. Run tests — ALL must fail (red phase)
  3. Screenshot/log failures as evidence

Phase B: Implementation
  4. Create internal/spawn/command.go with BuildShellCommand, BuildAppleScript, BuildPgrepPattern
  5. Run command_test.go — ALL must pass
  6. Refactor terminal.go to use command.go (delete inline interpolation)
  7. Run full spawn tests — ALL must pass
  8. Fix Class 2 (Payload rename)
  9. Run full test suite: go test ./... -race -count=1 -timeout=120s
  10. Run: go vet ./...

Phase C: Verification
  11. grep -n 'fmt.Sprintf.*fullCmd\|envParts\|searchPattern.*WAGGLE' internal/spawn/terminal.go → 0 matches
  12. grep -rn 'req\.Data' internal/ cmd/ → 0 matches
  13. grep -n 'Data.*json.RawMessage' internal/protocol/message.go → 0 matches
```

## Do NOT

- Modify `docs/briefs/task-38-spawn.md` — brief is frozen
- Add new features beyond what's listed here
- Change the wire protocol (removing `Data` is a protocol cleanup, not a change)
- Break existing tests
- Use `fmt.Sprintf` with user-controlled strings for command construction — that's the bug we're fixing
