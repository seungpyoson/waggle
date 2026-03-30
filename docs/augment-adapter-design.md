# Augment Adapter Design

## 1. Source of Truth for Session Identity

Agent identity is derived from the `waggle adapter bootstrap augment --format markdown` command output. This command returns the agent name assigned by the broker for the current session. The identity is **ephemeral per session** but **stable across commands within that session**.

The Augment skill file (installed at `~/.augment/skills/waggle.md`) contains the bootstrap instruction but does not hardcode any state. State lives in the broker.

## 2. Detecting Active Augment Workspace

The workspace is detected by checking for the presence of `~/.augment/` directory with a `skills/` subdirectory. Within this directory, the file `~/.augment/skills/waggle.md` serves as the fingerprint — its presence and content signal that Augment has been integrated with Waggle.

The detection is passive and file-based, not dynamic. It does not require Augment to be running.

## 3. Normal Path (No User Commands)

**Single user action:** `waggle install augment` (run once, in any waggle-enabled repo)

**Automatic path thereafter:**
1. User opens Augment session in waggle-enabled repo
2. Augment reads `~/.augment/skills/waggle.md` at session start
3. Augment sees the `<!-- WAGGLE-AUGMENT-BEGIN -->` block with instruction
4. Augment **automatically executes** `waggle adapter bootstrap augment --format markdown` at session start
5. Broker assigns agent identity and returns it to Augment
6. Augment receives agent name and uses it for all waggle commands in that session

**Zero additional user action required after install.**

## 4. Fallback Path & Temporary Tracking

If the skill file is missing or malformed:
- `waggle status` reports the adapter as `broken` or `not_installed` with repair guidance: `waggle install augment`
- User can manually run `waggle adapter bootstrap augment --format markdown` as a temporary workaround
- Manual runs are not tracked or encouraged — they indicate installation failure

## 5. No Dual Transport

This design creates **one authoritative path**:
- **Wire path:** Augment → broker via socket (using waggle client)
- **Configuration path:** Install writes to `~/.augment/skills/waggle.md` (Augment's config)
- **Bootstrap path:** Augment calls `waggle adapter bootstrap augment` (single source of truth)

There is no second transport. Augment does not directly register with the broker. Augment does not create parallel connection state. All coordination flows through the single `adapter bootstrap` command.

## 6. User Interaction Elimination

**Before this PR:**
- User runs `waggle install augment` (installs integration)
- User manually runs `waggle adapter bootstrap augment` in **every Augment session**
- Augment never receives coordination messages
- Manual step required, error-prone, UX friction

**After this PR:**
- User runs `waggle install augment` **once** (installs integration)
- Augment skill file automatically bootstraps at session start
- Augment receives agent name from bootstrap output
- Augment can receive coordination messages
- **Zero manual steps per session**

The skill file becomes a persistent contract: "Augment will run this command at session start." Users do not manually run `adapter bootstrap` anymore.
