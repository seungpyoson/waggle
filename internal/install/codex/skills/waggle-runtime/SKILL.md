---
name: waggle-runtime
description: Inspect Waggle prerequisites and the current Codex integration boundary.
---

Codex native messaging is unsupported in this build until exact-thread enrollment and per-thread credential propagation have been implemented and verified. Do not bootstrap identity from a shell, transcript, working directory or process ID. Do not use an alternate delivery channel.

`waggle sessions` reports canonical enrollment state. `waggle task --help` describes the separate task contract. A broker connection or a stored message is not evidence that a native Codex thread received input.
