---
name: waggle-presence
description: List connected waggle agents and their status
---

Execute this command:

```bash
WAGGLE_AGENT_NAME="${WAGGLE_AGENT_NAME:-claude-$$}" waggle presence
```

Returns JSON array of connected agents with name and state (online).

