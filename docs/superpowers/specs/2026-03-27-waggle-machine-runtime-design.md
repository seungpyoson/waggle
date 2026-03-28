# Waggle Machine Runtime Design

**Date:** 2026-03-27
**Status:** Draft
**Context:** Replace the fragile Claude Code `SessionStart` listener model with a machine-local runtime that supports reliable automatic delivery across Claude Code, Codex, Gemini CLI, and Augment Code.

## Problem

Waggle's current automatic push delivery path is architecturally unsound for Claude Code. The existing integration starts a long-lived background listener from a short-lived `SessionStart` hook. That couples persistent process lifecycle, terminal inheritance, and UI startup together. The result is a class of failures where the host TUI can be corrupted even if the broker itself is healthy.

This is not a Claude-specific shell bug to patch around. It is a boundary problem:

- The broker is a per-project coordination backend.
- A startup hook is a short-lived presentation entry point.
- A persistent listener is a long-lived machine-local runtime concern.

These responsibilities must be separated.

At the same time, automatic delivery remains important. Waggle should work across Claude Code, Codex, Gemini CLI, and Augment Code without forcing users to manually poll `waggle inbox` all day.

## Goals

1. Automatic message delivery across the four target tools:
   - Claude Code
   - Codex
   - Gemini CLI
   - Augment Code
2. Safe idle-time awareness via OS notifications.
3. Safe in-tool surfacing at the next interaction boundary.
4. No long-lived listener launched from startup hooks.
5. No dual-path architecture for manual vs auto registration.
6. No hardcoded platform-specific assumptions in the core runtime.
7. A simple v1 with low OS overhead and no heavyweight dependency stack.

## Non-Goals

1. No MCP integration in v1.
2. No tray/menu bar UI in v1.
3. No live mutation of a tool's UI while it sits idle.
4. No second broker hidden inside the machine-local runtime.
5. No platform-specific feature parity requirement beyond safe automatic delivery and safe surfacing.

## Design Principles

- **Single source of truth.** Broker-side message state remains authoritative for delivery and ack semantics.
- **One runtime path.** Manual registration, spawn-driven registration, and auto-registration from adapters all feed the same internal watch model.
- **Hooks stay thin.** Hooks may read local state and publish registration intent. Hooks must not supervise long-lived processes.
- **Broker stays transport-only.** The broker owns message storage, delivery, and task semantics. The machine runtime does not duplicate them.
- **Platform adapters stay presentation-only.** Adapters decide how to surface messages safely in a tool, not how transport works.
- **No hardcoded future blockers.** v1 may be explicit and minimal, but its data model and boundaries must support future auto-discovery and future UI without rewrite.

## Architecture Overview

Waggle will have two long-lived runtime roles:

1. **Project broker**
   - One broker per `project_id`
   - Owns tasks, messages, locks, sessions, and push delivery

2. **Machine runtime** (internal concept only; not a separate product name)
   - One machine-local background process per user session
   - Owns persistent listeners, local queue/cache, and OS notifications
   - Watches explicit `(project_id, agent_name)` pairs

Tool integrations become thin adapters:

- Claude Code adapter
- Codex adapter
- Gemini CLI adapter
- Augment Code adapter

These adapters do not own persistent listeners. They only:

- register session/watch intent
- read local unread state at safe boundaries
- surface messages using tool-safe mechanisms

## Core Boundary

The key architectural boundary is:

- **Broker:** source of truth for message lifecycle and routing
- **Machine runtime:** machine-local receiver and notifier
- **Adapters:** safe presentation layer

This is the most important decision in the design. If these boundaries blur, the system will accumulate duplicated semantics and become difficult to reason about.

## Project Identity

The system must key everything off `project_id`, not filesystem path.

Canonical project identity remains Waggle's existing resolution logic:

1. `WAGGLE_PROJECT_ID`
2. git root commit SHA
3. `"path:" + WAGGLE_ROOT`

The machine runtime must reuse the same project identity rules as the main CLI. It must not invent a second project-resolution scheme for specific platforms.

All local runtime state is keyed by:

- `project_id`
- `agent_name`

## Watch Model

The machine runtime tracks a single internal model: **watched endpoints**.

A watched endpoint consists of:

- `project_id`
- `agent_name`
- delivery preferences
- registration source metadata

Possible registration sources:

- explicit CLI registration
- `spawn`
- platform adapter session-start registration
- future auto-discovery

All of these sources feed the same watch model.

This is a hard requirement. There must not be separate manual and auto-discovered watch pipelines.

## Delivery Flow

For each watched `(project_id, agent_name)`:

1. The machine runtime resolves broker paths for `project_id`.
2. The runtime keeps a persistent Waggle listener alive for that agent's push channel.
3. When a message arrives:
   - persist a local delivery record
   - emit an OS notification
   - retain unread state for adapters
4. At a safe tool boundary, the tool adapter reads unread local records and surfaces them.
5. When the user/agent acts on the message, the adapter may send the appropriate Waggle `ack`.

The runtime is responsible for immediate machine-local awareness.
The adapter is responsible for safe in-tool presentation.
The broker remains the source of truth for delivery semantics.

## Local State Model

The machine runtime needs local machine state for safe presentation and notifications.

This local state is not a second source of truth for message delivery. It exists only to support:

- unread tracking for adapters
- notification suppression/de-duplication
- safe surfacing at the next tool boundary
- basic recovery if the tool opens after a message was already received

Suggested local record fields:

- `project_id`
- `agent_name`
- `message_id`
- `from_name`
- `body`
- `sent_at`
- `received_at`
- `notified_at`
- `surfaced_at`
- `dismissed_at`
- `acked_at_local`

The broker still owns queued/pushed/seen/acked semantics.
The machine runtime owns only machine-local presentation metadata.

## Registration Model

### v1

v1 should support three registration sources:

1. **Explicit registration**
   - reliable base path
   - user can directly watch an agent in the current repo/project

2. **Spawn registration**
   - Waggle can auto-register agents it launches itself

3. **Adapter auto-registration**
   - when Claude/Codex/Gemini/Augment start in a Waggle-aware project, the adapter can register the current `(project_id, agent_name)` with the machine runtime

These are not separate runtime modes. They are simply different sources of watch intent.

### v2

Future auto-discovery may populate the same watch model based on:

- recent active projects
- observed adapter registrations
- existing spawned agents
- recent broker usage

This is a future producer for the same watch model, not a new subsystem.

## Tool Adapter Contract

Each adapter should support the same conceptual contract:

1. Resolve the current `project_id`
2. Resolve the current `agent_name`
3. Register watch intent with the machine runtime
4. At a safe boundary, read unread local messages
5. Surface those messages safely inside the tool

Adapters must not:

- keep persistent broker listeners alive
- supervise background processes
- duplicate project identity logic
- store their own separate unread model

### Claude Code

- `SessionStart` may register watch intent and read local unread state
- `SessionStart` must remain short-lived
- no persistent listener spawned from hook
- idle-time awareness comes from OS notifications
- in-tool surfacing happens only at safe Claude boundaries

### Codex

- thin adapter reads local unread state at safe boundaries
- no persistent listener inside Codex session startup path

### Gemini CLI

- same model as Codex
- no MCP requirement

### Augment Code

- same model as Codex
- no background process managed by the Augment session itself

## Notifications

v1 notification requirements:

- OS notifications only
- no tray/menu bar UI required
- event-driven, not polling-heavy
- minimal platform-specific bridge behind a small interface

The runtime must define a notifier interface so future UI can be added without changing core runtime logic.

## Overhead Constraints

The machine runtime must remain lightweight:

- one background process
- event-driven listener loops
- bounded active watches
- append-friendly local persistence
- no heavyweight frameworks
- idle CPU near zero when no messages arrive

This keeps the runtime suitable for always-on use.

## CLI Shape

The user-facing CLI should remain under `waggle`.
The internal term "companion" should not become a separate end-user product surface.

Exact command naming is still open, but the CLI must support these capabilities:

- start/stop/status the machine-local runtime
- register/unregister a watch
- list watches
- inspect local unread state for debugging

The CLI should be designed so:

- explicit commands are the reliable base path
- auto-registration layers on top
- users are not forced to learn multiple concepts for the same underlying model

## v1 Scope

v1 should include:

1. Machine-local runtime process
2. Shared watch model
3. Local unread/presentation state
4. OS notifications
5. Safe Claude adapter
6. Thin adapter contract for Codex, Gemini CLI, and Augment Code
7. Explicit registration commands
8. Auto-registration from adapters where safe
9. Auto-registration from `spawn`

v1 should not include:

1. Tray/menu bar UI
2. MCP
3. Full auto-discovery of all active projects and agents
4. Invasive live UI mutation while tools are idle

## Risks

### 1. Identity ambiguity

If agent names are sloppy or inconsistent, automatic delivery becomes confusing even if the transport is correct.

Mitigation:

- require explicit `agent_name` discipline
- keep watch model transparent and inspectable

### 2. Companion scope creep

The machine runtime may accidentally accumulate broker-like semantics.

Mitigation:

- keep the runtime limited to reception, local persistence, and notifications
- treat broker state as authoritative

### 3. Duplicate unread semantics

If adapters invent their own unread tracking, the system will fork into incompatible paths.

Mitigation:

- centralize local unread state in the machine runtime
- adapters only consume it

### 4. Cross-platform notification differences

Notification delivery is inherently platform-specific.

Mitigation:

- keep platform-specific code behind one notifier abstraction

## Why This Is Better Than the Current Hook Model

The current Claude integration fails because it uses a startup hook as a daemon supervisor.

The new design fixes the class of problem:

- long-lived process ownership moves to a machine-local runtime
- hooks become short-lived and read-only
- OS notifications provide immediate idle-time awareness safely
- in-tool surfacing happens only at safe boundaries

This preserves automatic delivery without requiring live TUI mutation or fragile background children in the host tool's startup path.

## Open Questions

1. Exact user-facing CLI names for the machine runtime commands
2. Exact local persistence format for v1
3. Whether v1 notifications should include action buttons or remain simple
4. Which safe boundaries are available per tool for in-tool surfacing

These questions do not change the core architecture.

## Recommendation

Proceed with a headless machine-local Waggle runtime in v1.

Keep the broker as the source of truth.
Keep adapters thin.
Keep one watch model for all registration sources.
Build explicit registration first, then layer auto-registration on top.
