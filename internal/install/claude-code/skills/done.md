---
name: waggle-done
description: Complete a claimed task with its original claim token.
---

Use `waggle task complete <task-id> '<result-json>' --token <claim-token>` with the token from the original claim. Preserve the explicit task worker name used when claiming. Report errors instead of reconnecting under another identity. A task completion is separate from a messaging consumption receipt.

