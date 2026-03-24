# Task 13: Documentation and Cleanup

**Files:**
- Modify: `README.md` — full usage documentation
- Create: `CLAUDE.md` — project-specific instructions for AI agents
- Depends on: Task 12 (e2e passing)

- [ ] **Step 1: Update README with full usage docs**

Sections to add:
- **Install**: `go install github.com/seungpyoson/waggle@latest` + binary download
- **Quick Start**: Minimal example (create task, claim, complete)
- **CLI Reference**: All commands with flags and examples
- **Configuration**: WAGGLE_ROOT env var, per-project .waggle/config.json
- **Architecture**: Brief overview with diagram (broker, events, tasks, locks)
- **Integration**: How to use with Claude Code, Gemini CLI, Codex, scripts
- **Contributing**: How to build, test, submit PRs

- [ ] **Step 2: Create CLAUDE.md**

Project-specific instructions for Claude Code (and other AI agents) working in this repo:

```markdown
# Waggle — Development Guide

## Build & Test
- Build: `go build -o waggle .`
- Test all: `go test ./... -v`
- Test specific: `go test ./internal/tasks/ -v -run TestName`
- E2E: `go test -v -run TestE2E -count=1`

## Design Principles
Read `docs/superpowers/specs/2026-03-24-waggle-design.md` before making changes.
Key rules: zero hardcodes, single source of truth, no dual paths, fail loud.

## Code Structure
- `internal/config/` — path resolution, all defaults (THE source of truth)
- `internal/protocol/` — wire format types (public contract, change carefully)
- `internal/events/` — in-memory pub/sub hub
- `internal/tasks/` — SQLite task store, dependencies, lease management
- `internal/locks/` — advisory lock manager
- `internal/broker/` — socket listener, session management, command routing
- `internal/client/` — shared client for CLI commands
- `cmd/` — cobra CLI commands

## Commit Convention
- Conventional commits: `feat:`, `fix:`, `test:`, `docs:`, `chore:`
- Use `python3 ~/.claude/lib/safe_git.py commit` (not raw git commit)
```

- [ ] **Step 3: Run all tests one final time**

Run: `cd ~/Projects/Claude/waggle && go test ./... -v`
Expected: ALL PASS

- [ ] **Step 4: Commit**

```bash
git add README.md CLAUDE.md
python3 ~/.claude/lib/safe_git.py commit -m "docs: full README and CLAUDE.md"
```
