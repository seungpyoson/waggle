---
name: waggle-claim
description: Claim a broker task using an explicit worker name.
---

Use `waggle task claim --help` for the task contract. Set an explicit `WAGGLE_AGENT_NAME` for task-worker operations. This label does not authenticate messaging. Keep the returned claim token private, renew the lease through `waggle task heartbeat`, and complete or fail the task with that same token. Read the actual lease fields; do not assume a fixed duration.

