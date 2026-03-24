# Waggle

Agent session coordination broker. Lets independent AI coding agent sessions (Claude Code, Gemini CLI, Codex, scripts) coordinate work on the same project through shell commands.

**Status:** In development

## What it does

- **Task distribution** — Post tasks, workers claim and complete them
- **Coordination** — Advisory file locks prevent agents from stepping on each other
- **Event streaming** — Subscribe to real-time events (task completions, status changes)

## How it works

One broker per project, auto-started on first command. Any process that can run bash can participate.

```bash
# Queue work
waggle task create '{"desc": "fix lint errors in src/auth.py"}'

# Worker claims next task
waggle task claim --type code-edit

# Check what's happening
waggle task list
waggle status
```

## Install

```bash
go install github.com/seungpyoson/waggle@latest
```

## Design

See [design spec](docs/superpowers/specs/2026-03-24-waggle-design.md) for full architecture.

## License

MIT
