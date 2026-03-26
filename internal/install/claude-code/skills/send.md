---
name: waggle-send
description: Send a message to another waggle agent. Use when told to message, notify, or communicate with another agent.
---

Execute this command:

```bash
WAGGLE_AGENT_NAME="${WAGGLE_AGENT_NAME:-claude-$$}" waggle send "<recipient>" "<message>"
```

Replace `<recipient>` with the agent name and `<message>` with the text.

Options (append to command):
- `--priority critical` — urgent message
- `--priority bulk` — low priority
- `--ttl 300` — message expires after 300 seconds
- `--await-ack --timeout 30` — block until receiver acknowledges (max 30s)

Example:
```bash
WAGGLE_AGENT_NAME=orchestrator waggle send worker-1 "implement the auth module" --await-ack --timeout 60
```

