---
name: waggle-send
description: Send through this session's authenticated broker enrollment.
---

Run `waggle sessions`, select the exact receive-bound incarnation, then use:

```sh
waggle send <recipient-id> '<message>'
```

Use `--within <duration>` and `--hops <count>` to bound the conversation. Replies inherit these limits. For a disconnected enrolled recipient, `waggle enqueue` is an explicit storage request. A failed direct send must not automatically become enqueue or a second send. Credentials come from the session environment; do not print or replace them.

