# Waggle

## Identity
- Go broker/task runtime for agent coordination.
- Preserve clear ownership boundaries between broker, agents, protocol, and config.

## Build / Test
- Build: `go build -o waggle .`
- Test all: `go test ./... -v`
- Run targeted tests when narrowing a change, but keep verification honest.

## Design Rules
- Zero hardcodes. Paths, ports, timeouts, and limits belong in config.
- Single source of truth. Do not duplicate state, config, or routing logic.
- No dual paths. Keep one authoritative implementation path for each behavior.
- Fail loud with contextual errors.
- Broker is dumb; agents own semantics.
- Never block the host. Hooks and CLI help must work without broker, git, network, or project context.
- Every CLI `--help` must succeed from any directory in degraded state.

## Code Structure
- `internal/config/` owns defaults and path resolution.
- `internal/protocol/` is the wire contract; change carefully.
- `internal/tasks/` owns task persistence and leasing.
- Keep new code aligned with the existing package responsibilities.

## Critical
1. Do not hardcode defaults outside `internal/config/`.
2. Treat `internal/protocol/` changes as public contract changes.
3. Startup-path commands must stay fast, resilient, and non-fatal in degraded environments.

## graphify

This project has a graphify knowledge graph at graphify-out/.

Rules:
- Before answering architecture or codebase questions, read graphify-out/GRAPH_REPORT.md for god nodes and community structure
- If graphify-out/wiki/index.md exists, navigate it instead of reading raw files
- After modifying code files in this session, run `graphify update .` to keep the graph current (AST-only, no API cost)
