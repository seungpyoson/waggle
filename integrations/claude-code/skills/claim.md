---
name: waggle-claim
description: Claim the next available task from the waggle queue
---

Execute these commands:

```bash
# Connect and claim
waggle connect --name "${WAGGLE_AGENT_NAME:-claude-$$}"
RESULT=$(waggle task claim --type "<type>")
TASK_ID=$(echo "$RESULT" | jq -r '.data.ID')
TOKEN=$(echo "$RESULT" | jq -r '.data.ClaimToken')
echo "Claimed task $TASK_ID with token $TOKEN"
```

Replace `<type>` with the task type to claim, or omit `--type` for any task.

**IMPORTANT:** After claiming, the task has a 5-minute lease. Start a heartbeat:
```bash
# Keep lease alive (run in background)
while true; do sleep 120; waggle task heartbeat $TASK_ID --token $TOKEN 2>/dev/null || break; done &
```

When done, complete the task:
```bash
waggle task complete $TASK_ID '{"result": "what you did"}' --token $TOKEN
```

