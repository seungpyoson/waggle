---
name: waggle-ack
description: Record consumption of the exact native Waggle envelope.
---

After consuming the envelope, execute the acknowledgement command in its Waggle-authored header:

```sh
waggle ack <message-id> --attempt=<attempt-id>
```

Use the original message and attempt IDs. This records consumption, including when declining the requested work; it does not mean work completed. Only the original enrolled recipient may acknowledge. A correlated `waggle reply <message-id> '<message>'` records consumption in the same broker transaction. Native policy may deny these tool calls; report that failure without claiming a receipt.

