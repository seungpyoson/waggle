---
name: waggle-ack
description: Acknowledge a waggle message (confirms receipt to sender)
---

Execute this command:

```bash
WAGGLE_AGENT_NAME="${WAGGLE_AGENT_NAME:-claude-$$}" waggle ack <message_id>
```

Replace `<message_id>` with the numeric ID from your inbox.
If the sender used `--await-ack`, this unblocks them.

