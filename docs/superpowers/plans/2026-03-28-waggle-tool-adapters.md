# Waggle Tool Adapters Execution Plan

**Date:** 2026-03-28
**Branch:** `feat/tool-adapters`
**Scope:** Shared adapter contract, Codex-first implementation, Gemini only if clean, Augment deferred

## Outcome

Deliver one coherent adapter slice that:

- keeps Waggle low-visibility
- preserves one runtime-backed coordination contract
- makes Codex a real runtime participant
- optionally includes Gemini if the same contract fits without distortion
- does not lock in avoidable CPU/process overhead at the tool boundary

## Boundaries

In scope:

- installer surface for new adapter support
- reusable adapter-install shape
- Codex adapter assets and behavior
- Gemini adapter only if it fits the same shape cleanly
- docs updates for shipped scope

Out of scope:

- Augment Code Mac app integration
- runtime observability work
- dismiss/read API
- reconnect-under-restart testing
- binary versioning/distribution work
- deeper runtime scaling/hardening beyond what is required to avoid baking in the wrong cost model

## Cheapness Invariant

This branch must preserve Waggle as a boringly cheap coordination layer under high agent concurrency.

That means:

- no unbounded subprocess fanout from adapter or hook paths
- no polling storms introduced by tool integration work
- tool hooks stay thin and bounded
- one shared runtime-backed contract remains the only path

If the branch uncovers a deeper runtime scaling issue, document it and keep `#77` as the immediate next slice after this branch rather than deferring it until after later roadmap waves.

## Workstreams

### WS1. Explicit Shared Adapter Contract

Goal:
- make the existing Claude-style runtime participation contract explicit in code structure

Tasks:
1. Review current Claude installer and assets as the contract reference
2. Identify which install logic should be shared vs tool-specific
3. Refactor only enough to avoid copy-paste divergence for additional adapters

Definition of done:
- adapter-install shape is clear
- new adapters can reuse the same runtime semantics without cloning Claude-specific assumptions

### WS2. Codex Adapter

Goal:
- ship the first serious non-Claude adapter

Tasks:
1. Decide the thinnest viable Codex install/integration surface
2. Add install/uninstall support for Codex
3. Install Codex-side assets/config needed to:
   - resolve identity predictably
   - use the existing runtime contract
   - surface unread records at a safe boundary
4. Verify Codex participation behavior end to end at the adapter level

Definition of done:
- Codex has a real, supported Waggle integration path
- it uses runtime start/watch/pull, not a parallel transport
- the user does not need to manually relay messages for the basic Codex path

### WS3. Gemini Conditional Pass

Goal:
- include Gemini only if it cleanly reuses the same contract

Tasks:
1. Validate Gemini’s native hook/skill surface against the shared contract
2. If clean, add installer/assets using the same structure as Codex/Claude
3. If not clean, document deferral and leave it out of this branch

Definition of done:
- Gemini is either shipped cleanly or explicitly left out
- no half-supported Gemini path remains

### WS4. Docs and Scope Honesty

Goal:
- ensure README and install UX match the shipped state exactly

Tasks:
1. Update install help/docs for any new supported platforms
2. Update README shipped-scope language
3. Document Augment as deferred if still out of scope

Definition of done:
- docs match reality
- no overclaiming

## Execution Order

1. WS1 shared adapter shape
2. WS2 Codex implementation
3. WS3 Gemini decision and implementation only if clean
4. WS4 docs and verification cleanup

## Verification

Minimum:

- adapter install tests for new support
- `go test ./internal/install ./cmd -count=1`
- targeted runtime/adapter tests as added
- `go test ./... -count=1`
- `go build ./...`
- review for new per-agent background work, polling amplification, or subprocess fanout

Additional if Gemini is included:

- Gemini install/uninstall coverage
- one practical verification that Gemini still uses the same runtime path

## Merge Gate

Do not merge until:

1. Codex adapter is real, not docs-only
2. No new transport or listener lifecycle path was introduced
3. Claude integration still passes unchanged
4. README only claims shipped adapters
5. Full tests and build pass

## Notes

- If Codex requires a temporary shim/launcher path, it must be clearly transitional and must not become the documented long-term UX.
- If Gemini is cleaner than Codex but Codex remains the product priority, do not let Gemini’s cleaner hook surface hijack the architecture. The contract stays generic; Codex remains the first-class target.
- Augment is intentionally deferred to avoid forcing a Mac-app-specific integration into the first adapter slice.
- Runtime scaling/hardening remains a separate slice, but it is the next required slice after this branch if current review surfaces material cheapness risks.
