---
name: waggle
description: Interact with waggle agent coordination. Subcommands: send, inbox, status, claim, done, presence, ack
---

Available commands:
- `/waggle send <recipient> <message>` — send a message
- `/waggle inbox` — check your messages
- `/waggle ack <id>` — acknowledge a message
- `/waggle status` — broker status and queue health
- `waggle whoami` — show this session's Waggle runtime identity
- `/waggle claim` — claim next available task
- `/waggle done <task_id> <result>` — complete a claimed task
- `/waggle presence` — who's connected

Your agent name is `${WAGGLE_AGENT_NAME}` when set by waggle spawn or environment. If it is missing, run `waggle whoami` to read the runtime mapping used for pushed message delivery.
