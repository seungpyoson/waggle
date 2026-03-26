---
name: waggle-done
description: Complete a claimed waggle task with a result
---

Execute this command:

```bash
waggle connect --name "${WAGGLE_AGENT_NAME:-claude-$$}"
waggle task complete <task_id> '<result_json>' --token <claim_token>
```

Replace:
- `<task_id>` — the task ID from when you claimed it
- `<result_json>` — JSON describing what was accomplished
- `<claim_token>` — the token from the claim response

Example:
```bash
waggle task complete 5 '{"status": "done", "commit": "abc123"}' --token e4f7a2b1c3d5
```

