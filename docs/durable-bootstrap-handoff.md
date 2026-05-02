# Durable Bootstrap Handoff

## Current Failure Modes

Waggle now has two delivery paths:

- broker inbox replay, used by runtime catch-up
- runtime signal files, used by adapter shell hooks

The remaining durability risks are:

1. A consumer can read unread runtime delivery records and crash after the local
   store transition but before the agent has actually consumed the context.
2. Multiple bootstrap consumers for the same agent can race unless one store
   transition claims ownership before surfacing records.
3. Broker catch-up previously had to connect as `<agent>-push`, which could
   collide with an active listener and left a disconnect/reconnect gap.

## Chosen Direction

Use a single lifecycle for future durable bootstrap handoff:

1. Claim unread runtime records with an expiring lease.
2. Surface only claimed records to the adapter.
3. Dismiss records only after an explicit adapter ACK.
4. Reclaim expired claims so crashed consumers do not strand records.

This requires store changes for claim ownership and expiry. It does not require
agents to hold broker session identity while replaying historical inbox state.
Broker replay is now separated from session registration through `CmdReplay`,
which reads a named inbox without connecting as that name.

## Current Implementation Boundary

This PR implements the safe broker-side foundation:

- `CmdReplay` reads a named inbox without session registration.
- runtime CatchUp ACKs each successfully handled replayed broker message, so
  broker inbox replay does not grow without bound for runtime-managed agents.
- runtime CatchUp uses `CmdReplay`, so it no longer competes with an active
  `<agent>-push` listener.
- adapter bootstrap writes both PPID and TTY mappings, so pushed signal files
  are discoverable when later command shells have a different PPID.

Claim/lease/ACK for local runtime bootstrap records is intentionally not fused
into this PR. It needs a runtime store migration and a new adapter ACK contract.
