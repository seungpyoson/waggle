# Runtime Cheapness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the shared machine runtime more bounded under many watches by removing per-watch retry polling and separating runtime reconcile cadence from the generic poll loop, while keeping the current adapter/runtime contract intact.

**Architecture:** Keep one machine-local runtime and the existing watch/listener model, but reduce multiplicative idle work. Replace the current per-watch pending-notification retry loop with one manager-wide retry sweep, and introduce explicit runtime intervals so watch reconciliation does not inherit the generic `PollInterval` cadence. This is a bounded cheapness pass, not a full runtime rewrite.

**Tech Stack:** Go, Cobra CLI, SQLite runtime store, existing runtime manager tests

---

## Scope

In scope:

- remove one retry polling goroutine per watch from `internal/runtime/manager.go`
- add explicit runtime cadence defaults for watch reconciliation and retry sweep behavior
- preserve current runtime/watch semantics and notification correctness
- add practical tests that exercise multiple watches and bounded retry behavior
- document the cheapness model clearly in the branch docs / README as needed

Out of scope:

- install repair / doctor flows (`#76`)
- broad runtime observability work
- event-driven watch invalidation or a full runtime manager rewrite
- new transport paths, new unread stores, or tool-specific delivery logic

## File Map

- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/runtime/manager.go`
- Modify: `internal/runtime/manager_test.go`
- Modify: `internal/runtime/store.go`
- Modify: `internal/runtime/store_test.go`
- Modify: `README.md` if the overhead model needs one concise public note
- Modify: `docs/superpowers/plans/2026-03-28-tool-adapters-handoff.md` only if a cross-branch pointer is helpful after implementation

## Cheapness Rules For This Slice

- one persistent machine-local runtime process max
- no new per-watch polling loops
- no new subprocess fanout
- retry behavior must stay bounded under failure
- repeated adapter/runtime invocation must remain safe collapse, not amplification

## Task 1: Introduce Explicit Runtime Cadence Defaults

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing config tests**

Add tests for new runtime cadence defaults so the shape is locked before implementation:

```go
func TestDefaults_RuntimeReconcileInterval(t *testing.T) {
	if Defaults.RuntimeReconcileInterval != 2*time.Second {
		t.Fatalf("RuntimeReconcileInterval = %v, want 2s", Defaults.RuntimeReconcileInterval)
	}
}

func TestDefaults_RuntimeNotificationRetrySweepInterval(t *testing.T) {
	if Defaults.RuntimeNotificationRetrySweepInterval != time.Second {
		t.Fatalf("RuntimeNotificationRetrySweepInterval = %v, want 1s", Defaults.RuntimeNotificationRetrySweepInterval)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOCACHE=/tmp/go-build-cache GOMAXPROCS=1 go test ./internal/config -run 'Runtime(ReconcileInterval|NotificationRetrySweepInterval)' -count=1 -p=1`

Expected: FAIL because the new defaults do not exist yet.

- [ ] **Step 3: Add minimal config fields and defaults**

Extend the config defaults struct with two explicit runtime cadence fields and keep the values in `internal/config/` only:

```go
RuntimeReconcileInterval             time.Duration
RuntimeNotificationRetrySweepInterval time.Duration
```

Set them conservatively:

```go
RuntimeReconcileInterval:             2 * time.Second,
RuntimeNotificationRetrySweepInterval: 1 * time.Second,
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOCACHE=/tmp/go-build-cache GOMAXPROCS=1 go test ./internal/config -run 'Runtime(ReconcileInterval|NotificationRetrySweepInterval)' -count=1 -p=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "perf: add explicit runtime cheapness intervals"
```

## Task 2: Replace Per-Watch Retry Polling With One Manager-Wide Sweep

**Files:**
- Modify: `internal/runtime/manager.go`
- Modify: `internal/runtime/store.go`
- Modify: `internal/runtime/store_test.go`
- Modify: `internal/runtime/manager_test.go`

- [ ] **Step 1: Write the failing multi-watch retry test**

Add a manager test that proves retries still work for multiple watches without depending on one goroutine per watch. Structure it around two failed notifications that later succeed:

```go
func TestManager_GlobalRetrySweepRetriesPendingNotificationsAcrossMultipleWatches(t *testing.T) {
	// Arrange: two watches, two pending records, notifier fails once per record then succeeds.
	// Assert: both records eventually become notified and notifier call count reaches 4.
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOCACHE=/tmp/go-build-cache GOMAXPROCS=1 go test ./internal/runtime -run 'GlobalRetrySweepRetriesPendingNotificationsAcrossMultipleWatches' -count=1 -p=1`

Expected: FAIL because no manager-wide retry sweep exists yet.

- [ ] **Step 3: Add store support for manager-wide pending notification sweep**

Add one store method that returns pending notifications across all watches in stable order, instead of forcing per-watch queries:

```go
func (s *Store) PendingNotificationsAll() ([]DeliveryRecord, error) {
	rows, err := s.db.Query(`
		SELECT project_id, agent_name, message_id, from_name, body,
		       sent_at, received_at, notified_at, surfaced_at, dismissed_at
		FROM delivery_records
		WHERE notified_at IS NULL OR notified_at = ''
		ORDER BY project_id ASC, agent_name ASC, message_id ASC
	`)
	// scan with existing helper logic
}
```

Add a store test that confirms cross-watch stable ordering.

- [ ] **Step 4: Replace per-watch retry goroutine with one manager-wide loop**

In `internal/runtime/manager.go`:

- remove the `runPendingRetryLoop(ctx, w)` goroutine from `runWatch`
- add one manager-level retry sweep loop started from `Start`
- have that loop call a new `retryPendingNotifications()` that operates across `PendingNotificationsAll()`
- keep the existing retry/backoff map keyed by `(project_id, agent_name, message_id)`

Minimal target shape:

```go
func (m *Manager) runRetrySweepLoop(ctx context.Context) {
	ticker := time.NewTicker(config.Defaults.RuntimeNotificationRetrySweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.retryPendingNotifications(); err != nil {
				m.captureDeliveryError(fmt.Errorf("retry pending notifications: %w", err))
			}
		}
	}
}
```

- [ ] **Step 5: Run the targeted runtime tests**

Run:

`GOCACHE=/tmp/go-build-cache GOMAXPROCS=1 go test ./internal/runtime -run 'GlobalRetrySweepRetriesPendingNotificationsAcrossMultipleWatches|RetryPendingNotifications' -count=1 -p=1`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/runtime/manager.go internal/runtime/manager_test.go internal/runtime/store.go internal/runtime/store_test.go
git commit -m "perf: centralize runtime notification retry sweeps"
```

## Task 3: Decouple Watch Reconciliation From Generic PollInterval

**Files:**
- Modify: `internal/runtime/manager.go`
- Modify: `internal/runtime/manager_test.go`

- [ ] **Step 1: Write the failing cadence-focused test**

Add a small test that locks the runtime manager to the explicit reconcile interval rather than `config.Defaults.PollInterval`. This can be indirect by verifying a manager built with a fake store does not react on the old cadence after `PollInterval` changes alone:

```go
func TestManager_UsesRuntimeReconcileIntervalForWatchReconciliation(t *testing.T) {
	// Arrange manager with short RuntimeReconcileInterval and larger PollInterval.
	// Assert reconcile-driven listener creation follows the runtime-specific interval.
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOCACHE=/tmp/go-build-cache GOMAXPROCS=1 go test ./internal/runtime -run 'UsesRuntimeReconcileIntervalForWatchReconciliation' -count=1 -p=1`

Expected: FAIL because reconcile still uses `PollInterval`.

- [ ] **Step 3: Update the reconcile loop**

Change:

```go
ticker := time.NewTicker(config.Defaults.PollInterval)
```

to:

```go
ticker := time.NewTicker(config.Defaults.RuntimeReconcileInterval)
```

Also update any reconnect backoff reset that should now use the runtime-specific cadence rather than the generic one, but keep the backoff bounded by the existing max.

- [ ] **Step 4: Run targeted runtime manager tests**

Run:

`GOCACHE=/tmp/go-build-cache GOMAXPROCS=1 go test ./internal/runtime -run 'UsesRuntimeReconcileIntervalForWatchReconciliation|DynamicallyAddedWatch|WatchCount' -count=1 -p=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/runtime/manager.go internal/runtime/manager_test.go
git commit -m "perf: decouple runtime reconcile cadence from generic polling"
```

## Task 4: Document and Verify the Cheapness Model

**Files:**
- Modify: `README.md` if one short public note improves merge readiness
- Modify: `docs/superpowers/plans/2026-03-28-tool-adapters-handoff.md` only if needed

- [ ] **Step 1: Add one concise cheapness note**

Document the merge bar clearly:

- one machine-local runtime process max
- no per-watch retry polling
- no adapter-side fanout
- practical verification is single-process and bounded

Prefer a short section or paragraph rather than a large essay.

- [ ] **Step 2: Run full verification**

Run:

`GOCACHE=/tmp/go-build-cache GOMAXPROCS=1 go test ./internal/runtime ./internal/config -count=1 -p=1`

Then:

`GOCACHE=/tmp/go-build-cache GOMAXPROCS=1 go test ./... -count=1 -p=1`

Then:

`GOCACHE=/tmp/go-build-cache GOMAXPROCS=1 go build ./...`

Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add README.md docs/superpowers/plans/2026-03-28-tool-adapters-handoff.md
git commit -m "docs: record runtime cheapness merge bar"
```

## Merge Gate

Do not consider `#77` ready until:

1. Runtime retry behavior no longer uses one polling loop per watch
2. Reconcile cadence is explicit and no longer tied to the generic poll loop
3. Tests prove multiple watches still receive retries correctly
4. Full repo tests pass in single-process mode
5. No new subprocess fanout or alternate delivery path was introduced
6. The cheapness model is written down plainly enough to review against future adapter/runtime work

## Notes

- This slice intentionally does **not** solve all runtime scaling concerns.
- If this work reveals that full watch-table reconciliation every interval is still too expensive, capture that as the next bounded follow-up instead of broadening this branch midstream.
- Keep the runtime manager behavior coherent: fix one class of multiplicative background cost now, then re-measure before taking on a deeper architectural change.
