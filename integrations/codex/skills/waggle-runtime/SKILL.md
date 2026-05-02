---
name: waggle-runtime
description: Bootstrap this Codex session into Waggle's machine-local runtime and surface unread coordination records.
---

# Waggle Runtime

Use this skill near the start of a Codex session when `waggle` is available and the current repository is using Waggle for coordination.

Run:

```bash
waggle adapter bootstrap codex --format markdown
```

What to do with the result:

1. Read the returned unread records and incorporate them into the current context.
2. Note the `Agent:` value from the output and reuse it for later Waggle commands in this session by prefixing them with `WAGGLE_AGENT_NAME="<agent>"` when needed. If you lose it, run `waggle whoami`.
3. If the command reports that Waggle is unavailable or the current directory is not a Waggle-capable project, continue normally without treating that as a failure.

Do not start background listeners or invent a separate transport path. The bootstrap command is the authoritative runtime-backed participation entry point.
