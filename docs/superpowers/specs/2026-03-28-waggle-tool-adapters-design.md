# Waggle Tool Adapters Design

**Date:** 2026-03-28
**Status:** Draft
**Context:** Turn the machine-runtime foundation into a real multi-tool participation model without making Waggle the visible front door for every tool.

## Problem

The machine-runtime branch solved a real boundary problem: persistent delivery now belongs to a machine-local runtime instead of a fragile Claude `SessionStart` listener. That fixed the transport and lifecycle foundation, but it did not yet solve the product problem:

- Claude is the only shipped adapter.
- Codex and Gemini can participate only through explicit CLI usage or spawn.
- Augment Code is still outside the current adapter surface.
- Users still act as the relay between tools in real workflows.

The next step is not "add three more integrations" in the abstract. The next step is to make selected tools participate through one invisible coordination contract underneath, while keeping Waggle as infrastructure rather than a required launcher.

## Goals

1. Preserve one shared runtime-backed adapter contract across tools.
2. Ship one serious non-Claude adapter pass instead of three shallow ones.
3. Make Codex participate through the same runtime watch/pull model as Claude.
4. Extend the same contract to Gemini only where the native surface is clean.
5. Keep Waggle low-visibility in the long run.
6. Avoid any new transport path, sidecar daemon, or tool-specific delivery model.

## Non-Goals

1. No full Augment Code adapter in this slice.
2. No new broker/runtime semantics.
3. No wrapper-only product surface as the long-term answer.
4. No live UI mutation while a tool sits idle.
5. No attempt to solve rich observability, dismiss UX, or broker restart confidence here.

## Scope Decision

This slice is intentionally narrow:

- **Codex first**
  - highest workflow priority
  - serious first pass
- **Gemini if clean**
  - only if it fits the same adapter contract without contaminating it
- **Augment deferred**
  - current usage is primarily through the Mac app
  - no clean low-visibility boundary has been identified yet

This is a deliberate two-way-door decision. We are optimizing for one or two tools integrated well, not broad shallow coverage.

## Design Principles

- **One invisible coordination contract.** All tools should join the same runtime-backed system even if their user-facing integration surfaces differ.
- **Waggle is infrastructure, not the front door.** Users should not have to consciously "enter through Waggle" forever.
- **Presentation differs, transport does not.** Tool-specific logic may decide when and how to surface unread records, but it must not fork transport, identity, or storage.
- **Adapters stay thin.** Adapters may install configuration, hooks, or helper assets. They must not own persistent listeners or background supervision.
- **Waggle must stay boringly cheap under load.** Tool integration work must not introduce per-agent daemon fanout, polling storms, or other multiplicative background cost that becomes the bottleneck at 50-100+ concurrent agents.
- **Codex-first does not mean Codex-only architecture.** The first implementation should generalize cleanly to Gemini and later Augment.

## Invisible Coordination Contract

Every tool adapter must use the same underlying contract:

1. Resolve `project_id`
2. Resolve stable `agent_name`
3. Ensure the machine runtime is available
4. Register watch intent through the existing runtime watch path
5. Read unread local runtime records at a safe tool boundary
6. Surface those records safely for that tool

Adapters must not:

- start persistent broker listeners
- own separate unread stores
- invent tool-specific project identity rules
- create alternative broker or runtime control paths
- create unbounded subprocess fanout or polling-heavy side paths at the tool boundary

The current Claude integration already expresses this contract:

- `waggle runtime start`
- `waggle runtime watch <agent> --source <adapter>`
- `waggle runtime pull <agent>`

That remains the canonical adapter shape.

## Tool Assessment

### Claude Code

Claude remains the reference adapter:

- native startup hook exists
- existing install story is already shipped
- current adapter is thin and runtime-backed

Claude is not the target of this slice, but its adapter contract is the model to preserve.

### Codex

Codex is the first serious target because it is the highest-priority partner in the intended workflow.

Constraints observed in the current environment:

- `codex` has no obvious hook command comparable to Claude SessionStart or Gemini hooks
- Codex does have strong config/context surfaces under `~/.codex/`
- Codex already supports AGENTS/skills style workflows and stable environment

Implication:

- Codex should be integrated through a thin Waggle-aware Codex context surface plus explicit runtime-backed participation behavior
- if a temporary launcher path is needed, it must be treated as transitional, not as the long-term product surface

### Gemini CLI

Gemini appears to have a cleaner native boundary than Codex:

- `gemini hooks`
- `gemini skills`
- `~/.gemini/` config surface

Implication:

- Gemini may fit the shared adapter contract cleanly
- Gemini should only be included in this slice if we can reuse the same contract and installer shape without creating a Gemini-specific subsystem

### Augment Code

Augment is explicitly deferred in this slice.

Current environment signal:

- usage is primarily through the Mac app
- no clean repo-local or CLI-native hook/install boundary has been identified

Implication:

- do not force Augment into a rushed adapter path now
- preserve the shared runtime contract so Augment can adopt it later

## Proposed Adapter Architecture

### 1. Introduce a Generic Adapter Model in Install Logic

The current install path is Claude-specific. This slice should create a reusable adapter-install shape without over-abstracting runtime behavior.

Each adapter install flow should be able to:

- install tool-specific assets
- register any tool-specific startup boundary
- uninstall cleanly
- remain idempotent

The shared runtime contract stays below that layer.

### 2. Separate Core Adapter Semantics from Tool Assets

There are two different concerns:

- **shared semantics**
  - project/agent identity
  - runtime start/watch/pull contract
  - low-visibility participation rules
- **tool assets**
  - hook scripts
  - skills/context files
  - config snippets

This slice should make the shared semantics explicit so Codex and Gemini do not become copies of Claude with tool names substituted.

### 3. Keep `install` as the Explicit Activation Path

The low-visibility long-term goal does not mean "no explicit setup."

For v1 of this slice:

- `waggle install <platform>` remains the explicit activation surface
- the installed adapter should then make Waggle less visible during day-to-day tool use

This keeps setup intentional while keeping runtime participation invisible afterward.

### 4. Codex First-Class, Gemini Conditional

Codex gets the first full pass:

- installer and assets if needed
- stable identity and runtime participation behavior
- documentation of the expected Codex-side interaction boundary

Gemini is only added in the same branch if:

- it fits the same install/runtime contract cleanly
- it does not force a new abstraction that weakens the Codex-first design

Otherwise Gemini should be deferred to the next slice even if its native surface is cleaner.

## User Experience Target

### Desired Long-Term Experience

Users should be able to open tools naturally:

- Claude Code
- Codex
- Gemini
- eventually Augment

and have Waggle present only as the coordination substrate underneath.

### Acceptable v1 Experience for This Slice

- one-time explicit install/setup is fine
- small tool-specific context or startup configuration is fine
- lightweight helper assets are fine
- a temporary shim is acceptable only if it is clearly transitional and does not fork architecture

### Unacceptable v1 Experience

- users must always launch Codex or Gemini through a visible `waggle <tool>` wrapper as the main UX
- each tool uses a different transport or delivery model
- Codex/Gemini participation requires reopening the runtime architecture

## README / Product Honesty

Docs must remain explicit about what ships:

- Claude adapter is already shipped
- this slice may ship Codex
- Gemini should only be claimed if fully included in the same runtime-backed contract
- Augment remains future work

No doc language should imply "all tools are now native participants" unless that is actually true.

## Verification Requirements

The slice is not complete unless it proves the shared contract in practice.

Minimum verification:

1. Install / uninstall tests for any new adapter installer
2. Runtime-backed participation test for Codex path
3. Documentation verified against actual shipped scope
4. No regression to Claude install/runtime behavior
5. Review that the adapter path remains thin and bounded rather than adding new per-agent background overhead

If Gemini is included in-slice, it needs equivalent verification rather than "docs-only" support.

## Cheapness Follow-Through

This branch should avoid locking in the wrong cost model, but it should not absorb the full runtime scaling redesign.

If review shows deeper cheapness risks in the shared runtime itself, that work belongs in the immediate next hardening slice after this branch, not after the rest of the roadmap waves.

## Decision

Proceed with:

- shared adapter semantics made explicit
- Codex as the first serious non-Claude adapter
- Gemini only if it fits the same contract cleanly during implementation
- Augment explicitly deferred

This keeps the door open for lower-visibility native integrations later, while moving Waggle from "runtime foundation exists" to "another real tool can now participate through the same invisible contract."
