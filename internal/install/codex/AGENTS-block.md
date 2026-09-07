## Waggle coordination

Messaging requires broker-verified enrollment of the exact native conversation. Codex native enrollment and per-thread credential propagation are not yet implemented in this build; sending and receiving through Codex are unsupported. Do not run a shell command to invent enrollment, choose identity by PID or directory, or start a background listener.

The selected integration requires a user-owned App Server with the interactive TUI explicitly attached to its registered Unix endpoint. Plain sessions are not assumed to share that endpoint. Runtime conformance remains required before this integration can report readiness.

Existing task operations remain available through `waggle task --help` with an explicit task-worker name. Task labels cannot authenticate messages.
