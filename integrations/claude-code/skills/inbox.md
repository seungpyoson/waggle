---
name: waggle-inbox
description: Check your waggle inbox for messages from other agents
---

Execute this command:

```bash
WAGGLE_AGENT_NAME="${WAGGLE_AGENT_NAME:-claude-$$}" waggle inbox
```

Returns JSON with messages. Each message has `id`, `from`, `body`, `priority`, `state`.
After reading, acknowledge important messages with `/waggle ack <id>`.

