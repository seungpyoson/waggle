package runtime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
)

func TestManager_StartLoadsWatchesAndNotifiesOnDelivery(t *testing.T) {
	store := newTestStore(t)
	if err := store.UpsertWatch(Watch{ProjectID: "proj-a", AgentName: "agent-1", Source: "hook"}); err != nil {
		t.Fatal(err)
	}

	factory := newFakeListenerFactory()
	notifier := &fakeNotifier{}
	manager := NewManager(store, factory, notifier)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()

	listener := factory.waitForListener(t, Watch{ProjectID: "proj-a", AgentName: "agent-1", Source: "hook"})
	if err := listener.emit(Delivery{
		MessageID:  42,
		FromName:   "orchestrator",
		Body:       "finish the runtime wiring",
		SentAt:     time.Unix(10, 0).UTC(),
		ReceivedAt: time.Unix(11, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "notification", func() bool {
		return notifier.callCount() == 1
	})

	unread, err := store.Unread("proj-a", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 1 {
		t.Fatalf("unread count = %d, want 1", len(unread))
	}
	if unread[0].MessageID != 42 {
		t.Fatalf("message id = %d, want 42", unread[0].MessageID)
	}
	if unread[0].FromName != "orchestrator" {
		t.Fatalf("from name = %q, want orchestrator", unread[0].FromName)
	}

	title, body := notifier.last()
	if title == "" {
		t.Fatal("notification title was empty")
	}
	if body != "finish the runtime wiring" {
		t.Fatalf("notification body = %q, want %q", body, "finish the runtime wiring")
	}
}

func TestManager_HandleDeliveryDeduplicatesByProjectAgentAndMessageID(t *testing.T) {
	store := newTestStore(t)
	notifier := &fakeNotifier{}
	manager := NewManager(store, newFakeListenerFactory(), notifier)

	watch := Watch{ProjectID: "proj-a", AgentName: "agent-1", Source: "hook"}
	first := Delivery{
		MessageID:  7,
		FromName:   "sender",
		Body:       "hello once",
		SentAt:     time.Unix(20, 0).UTC(),
		ReceivedAt: time.Unix(21, 0).UTC(),
	}

	if err := manager.handleDelivery(watch, first); err != nil {
		t.Fatal(err)
	}
	if err := manager.handleDelivery(watch, first); err != nil {
		t.Fatal(err)
	}

	unread, err := store.Unread("proj-a", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 1 {
		t.Fatalf("unread count = %d, want 1", len(unread))
	}
	if notifier.callCount() != 1 {
		t.Fatalf("notify count = %d, want 1", notifier.callCount())
	}
}

func TestManager_HandleDeliveryRetriesNotifyAfterFailure(t *testing.T) {
	store := newTestStore(t)
	notifier := &fakeNotifier{
		errs: []error{errors.New("notify failed"), nil},
	}
	manager := NewManager(store, newFakeListenerFactory(), notifier)

	watch := Watch{ProjectID: "proj-a", AgentName: "agent-1", Source: "hook"}
	delivery := Delivery{
		MessageID:  8,
		FromName:   "sender",
		Body:       "retry me",
		SentAt:     time.Unix(30, 0).UTC(),
		ReceivedAt: time.Unix(31, 0).UTC(),
	}

	if err := manager.handleDelivery(watch, delivery); err != nil {
		t.Fatalf("handleDelivery should preserve listener on notify failure: %v", err)
	}

	rec, err := store.GetRecord("proj-a", "agent-1", 8)
	if err != nil {
		t.Fatal(err)
	}
	if rec.RetryAttempts != 1 || rec.RetryNextAt.IsZero() || !rec.RetryExhaustedAt.IsZero() {
		t.Fatalf("record retry state = %+v, want attempt=1 next_retry set exhausted unset", rec)
	}
	if err := store.RecordNotificationFailure("proj-a", "agent-1", 8, rec.RetryAttempts, time.Now().UTC().Add(-time.Millisecond), time.Time{}); err != nil {
		t.Fatal(err)
	}

	if err := manager.retryPendingNotifications(); err != nil {
		t.Fatal(err)
	}

	if notifier.callCount() != 2 {
		t.Fatalf("notify count = %d, want 2", notifier.callCount())
	}

	unread, err := store.Unread("proj-a", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 1 {
		t.Fatalf("unread count = %d, want 1", len(unread))
	}
	if unread[0].NotifiedAt.IsZero() {
		t.Fatal("expected record to be marked notified after retry success")
	}
	if unread[0].RetryAttempts != 0 || !unread[0].RetryNextAt.IsZero() || !unread[0].RetryExhaustedAt.IsZero() {
		t.Fatalf("record retry state after success = %+v, want cleared retry state", unread[0])
	}
}

func TestManager_GlobalRetrySweepRetriesPendingNotificationsAcrossMultipleWatches(t *testing.T) {
	store := newTestStore(t)
	manager := NewManager(store, newFakeListenerFactory(), &fakeNotifier{
		errs: []error{
			errors.New("first watch retry failed"),
			errors.New("second watch retry failed"),
			nil,
			nil,
		},
	})

	records := []DeliveryRecord{
		{
			ProjectID:  "proj-a",
			AgentName:  "agent-1",
			MessageID:  1,
			FromName:   "planner",
			Body:       "first",
			SentAt:     time.Unix(80, 0).UTC(),
			ReceivedAt: time.Unix(81, 0).UTC(),
		},
		{
			ProjectID:  "proj-b",
			AgentName:  "agent-2",
			MessageID:  2,
			FromName:   "planner",
			Body:       "second",
			SentAt:     time.Unix(82, 0).UTC(),
			ReceivedAt: time.Unix(83, 0).UTC(),
		},
	}
	for _, rec := range records {
		if _, err := store.AddRecordIfAbsent(rec); err != nil {
			t.Fatal(err)
		}
	}

	if err := manager.retryPendingNotifications(); err != nil {
		t.Fatalf("first retry sweep returned error: %v", err)
	}

	for _, tc := range []struct {
		projectID string
		agentName string
		messageID int64
	}{
		{projectID: "proj-a", agentName: "agent-1", messageID: 1},
		{projectID: "proj-b", agentName: "agent-2", messageID: 2},
	} {
		rec, err := store.GetRecord(tc.projectID, tc.agentName, tc.messageID)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.RecordNotificationFailure(tc.projectID, tc.agentName, tc.messageID, rec.RetryAttempts, time.Now().UTC().Add(-time.Millisecond), time.Time{}); err != nil {
			t.Fatal(err)
		}
	}

	if err := manager.retryPendingNotifications(); err != nil {
		t.Fatalf("second retry sweep returned error: %v", err)
	}

	for _, tc := range []struct {
		projectID string
		agentName string
		messageID int64
	}{
		{projectID: "proj-a", agentName: "agent-1", messageID: 1},
		{projectID: "proj-b", agentName: "agent-2", messageID: 2},
	} {
		rec, err := store.GetRecord(tc.projectID, tc.agentName, tc.messageID)
		if err != nil {
			t.Fatal(err)
		}
		if rec.NotifiedAt.IsZero() {
			t.Fatalf("record %s/%s/%d not marked notified", tc.projectID, tc.agentName, tc.messageID)
		}
	}
}

func TestManager_RetryPendingNotificationsClearsOnlyDeliveryErrorOnSuccess(t *testing.T) {
	store := newTestStore(t)
	manager := NewManager(store, newFakeListenerFactory(), &fakeNotifier{})

	if _, err := store.AddRecordIfAbsent(DeliveryRecord{
		ProjectID:     "proj-a",
		AgentName:     "agent-1",
		MessageID:     7,
		FromName:      "planner",
		Body:          "retry me",
		SentAt:        time.Unix(10, 0).UTC(),
		ReceivedAt:    time.Unix(11, 0).UTC(),
		RetryAttempts: 1,
		RetryNextAt:   time.Now().UTC().Add(-time.Millisecond),
	}); err != nil {
		t.Fatal(err)
	}
	manager.captureDeliveryError(watchDeliveryErrorKey("proj-a", "agent-1"), errors.New("stale delivery error"))
	manager.captureDeliveryError("reconcile", errors.New("live reconcile error"))
	if err := manager.retryPendingNotifications(); err != nil {
		t.Fatal(err)
	}
	err := manager.LastDeliveryError()
	if err == nil {
		t.Fatal("LastDeliveryError() = nil, want remaining reconcile error")
	}
	if strings.Contains(err.Error(), "watch-delivery/proj-a/agent-1") {
		t.Fatalf("LastDeliveryError() = %v, want delivery error cleared", err)
	}
	if !strings.Contains(err.Error(), "reconcile") {
		t.Fatalf("LastDeliveryError() = %v, want remaining reconcile error", err)
	}
}

func TestManager_StopCancelsListeners(t *testing.T) {
	store := newTestStore(t)
	if err := store.UpsertWatch(Watch{ProjectID: "proj-a", AgentName: "agent-1", Source: "hook"}); err != nil {
		t.Fatal(err)
	}

	factory := newFakeListenerFactory()
	manager := NewManager(store, factory, &fakeNotifier{})

	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	listener := factory.waitForListener(t, Watch{ProjectID: "proj-a", AgentName: "agent-1", Source: "hook"})

	if err := manager.Stop(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-listener.done:
	case <-time.After(2 * time.Second):
		t.Fatal("listener was not canceled by Stop")
	}
}

func TestManager_StartReconcilesWatchesAddedAfterStartup(t *testing.T) {
	store := newTestStore(t)
	factory := newFakeListenerFactory()
	manager := NewManager(store, factory, &fakeNotifier{})
	setRuntimeReconcileIntervalForTest(t, 10*time.Millisecond)

	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()

	if err := store.UpsertWatch(Watch{ProjectID: "proj-b", AgentName: "agent-2", Source: "hook"}); err != nil {
		t.Fatal(err)
	}

	listener := factory.waitForListener(t, Watch{ProjectID: "proj-b", AgentName: "agent-2", Source: "hook"})
	if listener == nil {
		t.Fatal("expected listener for dynamically added watch")
	}

	waitFor(t, "watch count after add", func() bool {
		return manager.WatchCount() == 1
	})
}

func TestManager_ReconcileClearsOnlyReconcileErrorOnSuccess(t *testing.T) {
	store := newTestStore(t)
	manager := NewManager(store, newFakeListenerFactory(), &fakeNotifier{})

	manager.captureDeliveryError("reconcile", errors.New("stale reconcile error"))
	manager.captureDeliveryError(watchTransportErrorKey(Watch{ProjectID: "proj-a", AgentName: "agent-1"}), errors.New("live watch error"))
	if err := manager.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := manager.LastDeliveryError()
	if err == nil {
		t.Fatal("LastDeliveryError() = nil, want remaining watch error")
	}
	if strings.Contains(err.Error(), "reconcile") {
		t.Fatalf("LastDeliveryError() = %v, want reconcile error cleared", err)
	}
	if !strings.Contains(err.Error(), "watch-transport/proj-a/agent-1") {
		t.Fatalf("LastDeliveryError() = %v, want remaining watch error", err)
	}
}

func TestManager_UsesRuntimeReconcileIntervalForWatchReconciliation(t *testing.T) {
	store := newTestStore(t)
	factory := newFakeListenerFactory()
	manager := NewManager(store, factory, &fakeNotifier{})

	origPoll := config.Defaults.PollInterval
	config.Defaults.PollInterval = 5 * time.Second
	setRuntimeReconcileIntervalForTest(t, 10*time.Millisecond)
	defer func() {
		config.Defaults.PollInterval = origPoll
	}()

	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()

	watch := Watch{ProjectID: "proj-c", AgentName: "agent-3", Source: "hook"}
	if err := store.UpsertWatch(watch); err != nil {
		t.Fatal(err)
	}

	listener := factory.waitForListener(t, watch)
	if listener == nil {
		t.Fatal("expected listener for runtime reconcile interval test")
	}

	waitFor(t, "watch count after runtime-specific reconcile", func() bool {
		return manager.WatchCount() == 1
	})
}

func TestManager_StartReconcilesWatchesRemovedAfterStartup(t *testing.T) {
	store := newTestStore(t)
	watch := Watch{ProjectID: "proj-a", AgentName: "agent-1", Source: "hook"}
	if err := store.UpsertWatch(watch); err != nil {
		t.Fatal(err)
	}

	factory := newFakeListenerFactory()
	manager := NewManager(store, factory, &fakeNotifier{})
	setRuntimeReconcileIntervalForTest(t, 10*time.Millisecond)

	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()

	listener := factory.waitForListener(t, watch)

	if err := store.RemoveWatch(watch.ProjectID, watch.AgentName); err != nil {
		t.Fatal(err)
	}

	select {
	case <-listener.done:
	case <-time.After(2 * time.Second):
		t.Fatal("listener was not canceled after watch removal")
	}

	waitFor(t, "watch count after remove", func() bool {
		return manager.WatchCount() == 0
	})
}

func TestManager_StopWatchClearsInflightState(t *testing.T) {
	store := newTestStore(t)
	watch := Watch{ProjectID: "proj-a", AgentName: "agent-1", Source: "hook"}
	if err := store.UpsertWatch(watch); err != nil {
		t.Fatal(err)
	}

	factory := newFakeListenerFactory()
	manager := NewManager(store, factory, &fakeNotifier{})
	setRuntimeReconcileIntervalForTest(t, 10*time.Millisecond)

	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()

	_ = factory.waitForListener(t, watch)

	key := deliveryKey{projectID: "proj-a", agentName: "agent-1", messageID: 99}
	manager.mu.Lock()
	manager.inflight[key] = struct{}{}
	manager.mu.Unlock()
	manager.captureDeliveryError(watchTransportErrorKey(watch), errors.New("transport error"))
	manager.captureDeliveryError(watchDeliveryErrorKey(watch.ProjectID, watch.AgentName), errors.New("delivery error"))
	manager.captureDeliveryError("delivery-status", errors.New("status refresh error"))

	if err := store.RemoveWatch(watch.ProjectID, watch.AgentName); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "watch removal cleanup", func() bool {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		_, inflightExists := manager.inflight[key]
		_, transportExists := manager.lastDeliveryErr[watchTransportErrorKey(watch)]
		_, deliveryExists := manager.lastDeliveryErr[watchDeliveryErrorKey(watch.ProjectID, watch.AgentName)]
		_, statusExists := manager.lastDeliveryErr["delivery-status"]
		return len(manager.workers) == 0 && !inflightExists && !transportExists && !deliveryExists && !statusExists
	})
}

func TestManager_RetryPendingNotificationsUsesBacklogErrorKeyAfterWatchRemoval(t *testing.T) {
	store := newTestStore(t)
	watch := Watch{ProjectID: "proj-a", AgentName: "agent-1", Source: "hook"}
	if err := store.UpsertWatch(watch); err != nil {
		t.Fatal(err)
	}

	if _, err := store.AddRecordIfAbsent(DeliveryRecord{
		ProjectID:  "proj-a",
		AgentName:  "agent-1",
		MessageID:  404,
		FromName:   "planner",
		Body:       "retry after watch removal",
		SentAt:     time.Unix(100, 0).UTC(),
		ReceivedAt: time.Unix(101, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	factory := newFakeListenerFactory()
	manager := NewManager(store, factory, &fakeNotifier{errs: []error{errors.New("notify failed")}})
	setRuntimeReconcileIntervalForTest(t, 10*time.Millisecond)

	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()

	_ = factory.waitForListener(t, watch)
	if err := store.RemoveWatch(watch.ProjectID, watch.AgentName); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "watch removed", func() bool {
		return manager.WatchCount() == 0
	})

	if err := manager.retryPendingNotifications(); err != nil {
		t.Fatalf("retryPendingNotifications returned unexpected error: %v", err)
	}

	err := manager.LastDeliveryError()
	if err == nil {
		t.Fatal("LastDeliveryError() = nil, want backlog delivery error")
	}
	if strings.Contains(err.Error(), watchDeliveryErrorKey("proj-a", "agent-1")) {
		t.Fatalf("LastDeliveryError() = %v, want no watch-delivery key after watch removal", err)
	}
	if !strings.Contains(err.Error(), backlogDeliveryErrorKey("proj-a", "agent-1")) {
		t.Fatalf("LastDeliveryError() = %v, want backlog-delivery key", err)
	}
}

func TestManager_BacklogRetrySuccessClearsBacklogErrorKey(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.AddRecordIfAbsent(DeliveryRecord{
		ProjectID:     "proj-a",
		AgentName:     "agent-1",
		MessageID:     505,
		FromName:      "planner",
		Body:          "backlog retry clears key",
		SentAt:        time.Unix(110, 0).UTC(),
		ReceivedAt:    time.Unix(111, 0).UTC(),
		RetryAttempts: 1,
		RetryNextAt:   time.Now().UTC().Add(-time.Millisecond),
	}); err != nil {
		t.Fatal(err)
	}

	notifier := &fakeNotifier{errs: []error{errors.New("notify failed"), nil}}
	manager := NewManager(store, newFakeListenerFactory(), notifier)

	if err := manager.retryPendingNotifications(); err != nil {
		t.Fatalf("first retryPendingNotifications returned unexpected error: %v", err)
	}
	err := manager.LastDeliveryError()
	if err == nil || !strings.Contains(err.Error(), backlogDeliveryErrorKey("proj-a", "agent-1")) {
		t.Fatalf("LastDeliveryError() = %v, want backlog-delivery error after failure", err)
	}

	rec, err := store.GetRecord("proj-a", "agent-1", 505)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordNotificationFailure("proj-a", "agent-1", 505, rec.RetryAttempts, time.Now().UTC().Add(-time.Millisecond), time.Time{}); err != nil {
		t.Fatal(err)
	}

	if err := manager.retryPendingNotifications(); err != nil {
		t.Fatalf("second retryPendingNotifications returned unexpected error: %v", err)
	}
	if err := manager.LastDeliveryError(); err != nil {
		t.Fatalf("LastDeliveryError() = %v, want nil after backlog retry success", err)
	}
}

func TestManager_RefreshDeliveryErrorStateKeepsActiveErrorWhileFailureInFlight(t *testing.T) {
	store := newTestStore(t)
	watch := Watch{ProjectID: "proj-a", AgentName: "agent-1", Source: "hook"}
	if err := store.UpsertWatch(watch); err != nil {
		t.Fatal(err)
	}

	factory := newFakeListenerFactory()
	manager := NewManager(store, factory, &fakeNotifier{})
	setRuntimeReconcileIntervalForTest(t, 10*time.Millisecond)

	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()

	_ = factory.waitForListener(t, watch)

	releasePending := manager.beginPendingFailure(watchKey{projectID: "proj-a", agentName: "agent-1"})
	defer releasePending()

	if err := manager.refreshDeliveryErrorState("proj-a", "agent-1"); err != nil {
		t.Fatal(err)
	}

	err := manager.LastDeliveryError()
	if err == nil {
		t.Fatal("LastDeliveryError() = nil, want active watch-delivery error while failure is in flight")
	}
	if !strings.Contains(err.Error(), watchDeliveryErrorKey("proj-a", "agent-1")) {
		t.Fatalf("LastDeliveryError() = %v, want watch-delivery key", err)
	}
}

func TestManager_SameWatchSuccessDoesNotClearSiblingFailure(t *testing.T) {
	store := newTestStore(t)
	watch := Watch{ProjectID: "proj-a", AgentName: "agent-1", Source: "hook"}
	if err := store.UpsertWatch(watch); err != nil {
		t.Fatal(err)
	}

	for _, rec := range []DeliveryRecord{
		{
			ProjectID:     "proj-a",
			AgentName:     "agent-1",
			MessageID:     601,
			FromName:      "planner",
			Body:          "first fails",
			SentAt:        time.Unix(120, 0).UTC(),
			ReceivedAt:    time.Unix(121, 0).UTC(),
			RetryAttempts: 1,
			RetryNextAt:   time.Now().UTC().Add(-time.Millisecond),
		},
		{
			ProjectID:     "proj-a",
			AgentName:     "agent-1",
			MessageID:     602,
			FromName:      "planner",
			Body:          "second succeeds",
			SentAt:        time.Unix(122, 0).UTC(),
			ReceivedAt:    time.Unix(123, 0).UTC(),
			RetryAttempts: 1,
			RetryNextAt:   time.Now().UTC().Add(-time.Millisecond),
		},
	} {
		if _, err := store.AddRecordIfAbsent(rec); err != nil {
			t.Fatal(err)
		}
	}

	manager := NewManager(store, newFakeListenerFactory(), &fakeNotifier{
		errs: []error{errors.New("first still failing"), nil},
	})
	setRuntimeReconcileIntervalForTest(t, 10*time.Millisecond)

	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()

	if err := manager.retryPendingNotifications(); err != nil {
		t.Fatalf("retryPendingNotifications returned unexpected error: %v", err)
	}

	err := manager.LastDeliveryError()
	if err == nil {
		t.Fatal("LastDeliveryError() = nil, want active watch-delivery error")
	}
	if !strings.Contains(err.Error(), watchDeliveryErrorKey("proj-a", "agent-1")) {
		t.Fatalf("LastDeliveryError() = %v, want watch-delivery key to remain while sibling failure exists", err)
	}

	failed, err := store.GetRecord("proj-a", "agent-1", 601)
	if err != nil {
		t.Fatal(err)
	}
	if failed.NotifiedAt.IsZero() && failed.RetryAttempts == 0 {
		t.Fatalf("failed record = %+v, want unresolved failed retry state", failed)
	}
	succeeded, err := store.GetRecord("proj-a", "agent-1", 602)
	if err != nil {
		t.Fatal(err)
	}
	if succeeded.NotifiedAt.IsZero() {
		t.Fatalf("successful record = %+v, want notified", succeeded)
	}
}

func TestManager_RestartsWatchAfterListenerError(t *testing.T) {
	store := newTestStore(t)
	if err := store.UpsertWatch(Watch{ProjectID: "proj-a", AgentName: "agent-1", Source: "hook"}); err != nil {
		t.Fatal(err)
	}

	factory := newRestartingListenerFactory()
	notifier := &fakeNotifier{}
	manager := NewManager(store, factory, notifier)

	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()

	first := factory.waitForCreated(t, 1)
	first.fail(errors.New("transient listener failure"))

	second := factory.waitForCreated(t, 2)
	if err := second.emit(Delivery{
		MessageID:  99,
		FromName:   "planner",
		Body:       "after restart",
		SentAt:     time.Unix(30, 0).UTC(),
		ReceivedAt: time.Unix(31, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "notification after restart", func() bool {
		return notifier.callCount() == 1
	})

	unread, err := store.Unread("proj-a", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 1 || unread[0].MessageID != 99 {
		t.Fatalf("unread = %+v, want message 99 after listener restart", unread)
	}
}

func TestManager_RetriesPendingNotificationAfterRestart(t *testing.T) {
	store := newTestStore(t)
	if err := store.UpsertWatch(Watch{ProjectID: "proj-a", AgentName: "agent-1", Source: "hook"}); err != nil {
		t.Fatal(err)
	}

	factory := newRestartingListenerFactory()
	notifier := &fakeNotifier{errs: []error{errors.New("notify failed"), nil}}
	manager := NewManager(store, factory, notifier)

	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()

	first := factory.waitForCreated(t, 1)
	if err := first.emit(Delivery{
		MessageID:  123,
		FromName:   "planner",
		Body:       "retry after reconnect",
		SentAt:     time.Unix(50, 0).UTC(),
		ReceivedAt: time.Unix(51, 0).UTC(),
	}); err != nil {
		t.Fatalf("emit should not tear down listener on notify failure: %v", err)
	}

	waitFor(t, "pending notification retry", func() bool {
		rec, err := store.GetRecord("proj-a", "agent-1", 123)
		return err == nil && !rec.NotifiedAt.IsZero() && notifier.callCount() >= 2
	})
}

func TestManager_UsesRuntimeRetrySweepInterval(t *testing.T) {
	store := newTestStore(t)
	notifier := &fakeNotifier{errs: []error{errors.New("notify failed"), nil}}
	manager := NewManager(store, newFakeListenerFactory(), notifier)

	origPoll := config.Defaults.PollInterval
	config.Defaults.PollInterval = 5 * time.Second
	setRuntimeRetrySweepIntervalForTest(t, 10*time.Millisecond)
	defer func() {
		config.Defaults.PollInterval = origPoll
	}()

	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()

	if err := manager.handleDelivery(Watch{ProjectID: "proj-a", AgentName: "agent-1", Source: "hook"}, Delivery{
		MessageID:  222,
		FromName:   "planner",
		Body:       "retry on timer",
		SentAt:     time.Unix(90, 0).UTC(),
		ReceivedAt: time.Unix(91, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	rec, err := store.GetRecord("proj-a", "agent-1", 222)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordNotificationFailure("proj-a", "agent-1", 222, rec.RetryAttempts, time.Now().UTC().Add(-time.Millisecond), time.Time{}); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "retry sweep interval", func() bool {
		rec, err := store.GetRecord("proj-a", "agent-1", 222)
		return err == nil && !rec.NotifiedAt.IsZero() && notifier.callCount() >= 2
	})
}

func TestManager_RetryPendingNotificationsContinuesPastFailedRecord(t *testing.T) {
	store := newTestStore(t)
	manager := NewManager(store, newFakeListenerFactory(), &fakeNotifier{
		errs: []error{errors.New("first notify failed"), nil},
	})

	first := DeliveryRecord{
		ProjectID:  "proj-a",
		AgentName:  "agent-1",
		MessageID:  1,
		FromName:   "planner",
		Body:       "first",
		SentAt:     time.Unix(60, 0).UTC(),
		ReceivedAt: time.Unix(61, 0).UTC(),
	}
	second := DeliveryRecord{
		ProjectID:  "proj-a",
		AgentName:  "agent-1",
		MessageID:  2,
		FromName:   "planner",
		Body:       "second",
		SentAt:     time.Unix(62, 0).UTC(),
		ReceivedAt: time.Unix(63, 0).UTC(),
	}
	if _, err := store.AddRecordIfAbsent(first); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddRecordIfAbsent(second); err != nil {
		t.Fatal(err)
	}

	if err := manager.retryPendingNotifications(); err != nil {
		t.Fatalf("retryPendingNotifications returned unexpected error: %v", err)
	}

	rec1, err := store.GetRecord("proj-a", "agent-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	rec2, err := store.GetRecord("proj-a", "agent-1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !rec1.NotifiedAt.IsZero() {
		t.Fatal("expected first record to remain unnotified after notify failure")
	}
	if rec2.NotifiedAt.IsZero() {
		t.Fatal("expected second record to be notified despite earlier failure")
	}
}

func TestManager_PendingRetryAndLiveDeliveryDoNotDoubleNotifySameRecord(t *testing.T) {
	store := newTestStore(t)
	notifier := &blockingNotifier{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	manager := NewManager(store, newFakeListenerFactory(), notifier)

	record := DeliveryRecord{
		ProjectID:  "proj-a",
		AgentName:  "agent-1",
		MessageID:  777,
		FromName:   "planner",
		Body:       "same message",
		SentAt:     time.Unix(70, 0).UTC(),
		ReceivedAt: time.Unix(71, 0).UTC(),
	}
	if _, err := store.AddRecordIfAbsent(record); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- manager.retryPendingNotifications()
	}()

	<-notifier.started

	if err := manager.handleDelivery(Watch{ProjectID: "proj-a", AgentName: "agent-1"}, Delivery{
		MessageID:  777,
		FromName:   "planner",
		Body:       "same message",
		SentAt:     time.Unix(70, 0).UTC(),
		ReceivedAt: time.Unix(71, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	close(notifier.release)

	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if notifier.callCount() != 1 {
		t.Fatalf("notify count = %d, want 1", notifier.callCount())
	}
}

func TestManager_RestartsWatchAfterCleanListenerExit(t *testing.T) {
	store := newTestStore(t)
	if err := store.UpsertWatch(Watch{ProjectID: "proj-a", AgentName: "agent-1", Source: "hook"}); err != nil {
		t.Fatal(err)
	}

	factory := newRestartingListenerFactory()
	manager := NewManager(store, factory, &fakeNotifier{})

	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()

	first := factory.waitForCreated(t, 1)
	first.succeed()

	_ = factory.waitForCreated(t, 2)
}

func TestManager_StartContinuesAfterDeliveryHandlerError(t *testing.T) {
	store := newTestStore(t)
	if err := store.UpsertWatch(Watch{ProjectID: "proj-a", AgentName: "agent-1", Source: "hook"}); err != nil {
		t.Fatal(err)
	}

	factory := newRestartingListenerFactory()
	notifier := &fakeNotifier{
		errs: []error{errors.New("notify failed"), nil},
	}
	manager := NewManager(store, factory, notifier)

	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()

	listener := factory.waitForCreated(t, 1)
	first := Delivery{
		MessageID:  100,
		FromName:   "sender",
		Body:       "first",
		SentAt:     time.Unix(40, 0).UTC(),
		ReceivedAt: time.Unix(41, 0).UTC(),
	}
	second := Delivery{
		MessageID:  101,
		FromName:   "sender",
		Body:       "second",
		SentAt:     time.Unix(42, 0).UTC(),
		ReceivedAt: time.Unix(43, 0).UTC(),
	}

	if err := listener.emit(first); err != nil {
		t.Fatalf("listener should stay alive after notification failure: %v", err)
	}

	if err := listener.emit(second); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "second notification", func() bool {
		return notifier.callCount() >= 2
	})

	unread, err := store.Unread("proj-a", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 2 {
		t.Fatalf("unread count = %d, want 2", len(unread))
	}
	if unread[1].MessageID != 101 {
		t.Fatalf("second unread message = %d, want 101", unread[1].MessageID)
	}
}

func TestManager_RetrySweepDoesNotStarveLaterEligibleRecords(t *testing.T) {
	store := newTestStore(t)
	origBatchSize := config.Defaults.RuntimeNotificationRetryBatchSize
	config.Defaults.RuntimeNotificationRetryBatchSize = 2
	defer func() { config.Defaults.RuntimeNotificationRetryBatchSize = origBatchSize }()

	notifier := &fakeNotifier{}
	manager := NewManager(store, newFakeListenerFactory(), notifier)

	for _, rec := range []DeliveryRecord{
		{
			ProjectID:  "proj-a",
			AgentName:  "agent-1",
			MessageID:  1,
			FromName:   "planner",
			Body:       "exhausted one",
			SentAt:     time.Unix(1, 0).UTC(),
			ReceivedAt: time.Unix(2, 0).UTC(),
		},
		{
			ProjectID:  "proj-a",
			AgentName:  "agent-1",
			MessageID:  2,
			FromName:   "planner",
			Body:       "exhausted two",
			SentAt:     time.Unix(3, 0).UTC(),
			ReceivedAt: time.Unix(4, 0).UTC(),
		},
		{
			ProjectID:  "proj-b",
			AgentName:  "agent-2",
			MessageID:  3,
			FromName:   "planner",
			Body:       "eligible later",
			SentAt:     time.Unix(5, 0).UTC(),
			ReceivedAt: time.Unix(6, 0).UTC(),
		},
	} {
		if _, err := store.AddRecordIfAbsent(rec); err != nil {
			t.Fatal(err)
		}
	}

	exhaustedAt := time.Now().UTC()
	for _, messageID := range []int64{1, 2} {
		if err := store.RecordNotificationFailure("proj-a", "agent-1", messageID, config.Defaults.RuntimeNotificationRetryLimit, time.Time{}, exhaustedAt); err != nil {
			t.Fatal(err)
		}
	}

	if err := manager.retryPendingNotifications(); err != nil {
		t.Fatalf("retry sweep returned error: %v", err)
	}

	rec, err := store.GetRecord("proj-b", "agent-2", 3)
	if err != nil {
		t.Fatal(err)
	}
	if rec.NotifiedAt.IsZero() {
		t.Fatalf("eligible later record not notified: %+v", rec)
	}
	if notifier.callCount() != 1 {
		t.Fatalf("notify count = %d, want 1", notifier.callCount())
	}
}

func TestManager_MaintenancePrunesExpiredWatchesAndResolvedRecords(t *testing.T) {
	store := newTestStore(t)
	if err := store.UpsertWatch(Watch{
		ProjectID: "proj-a",
		AgentName: "agent-ephemeral",
		Source:    "spawn",
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertWatch(Watch{
		ProjectID: "proj-a",
		AgentName: "agent-durable",
		Source:    "explicit",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddRecord(DeliveryRecord{
		ProjectID:   "proj-a",
		AgentName:   "agent-durable",
		MessageID:   1,
		FromName:    "planner",
		Body:        "old resolved",
		SentAt:      time.Unix(1, 0).UTC(),
		ReceivedAt:  time.Now().UTC().Add(-40 * 24 * time.Hour),
		NotifiedAt:  time.Unix(2, 0).UTC(),
		SurfacedAt:  time.Unix(3, 0).UTC(),
		DismissedAt: time.Unix(4, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	origTTL := config.Defaults.TTLCheckPeriod
	config.Defaults.TTLCheckPeriod = 10 * time.Millisecond
	defer func() { config.Defaults.TTLCheckPeriod = origTTL }()

	manager := NewManager(store, newFakeListenerFactory(), &fakeNotifier{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()

	waitFor(t, "maintenance prune", func() bool {
		watches, err := store.ListWatches()
		if err != nil {
			return false
		}
		if len(watches) != 1 || watches[0].AgentName != "agent-durable" {
			return false
		}
		_, err = store.GetRecord("proj-a", "agent-durable", 1)
		return errors.Is(err, ErrRecordNotFound)
	})
}

func TestBrokerListenerFactory_NewListenerUsesWatchProjectID(t *testing.T) {
	factory := NewBrokerListenerFactory()

	listener, err := factory.NewListener(Watch{
		ProjectID: "proj-from-watch",
		AgentName: "agent-1",
		Source:    "hook",
	})
	if err != nil {
		t.Fatal(err)
	}

	brokerListener, ok := listener.(*brokerListener)
	if !ok {
		t.Fatalf("listener type = %T, want *brokerListener", listener)
	}

	want := config.NewPaths("proj-from-watch").Socket
	if brokerListener.socketPath != want {
		t.Fatalf("socket path = %q, want %q", brokerListener.socketPath, want)
	}
}

func TestBrokerListenerFactory_NewListenerRejectsMissingIdentity(t *testing.T) {
	factory := NewBrokerListenerFactory()

	_, err := factory.NewListener(Watch{AgentName: "agent-1"})
	if err == nil {
		t.Fatal("expected error for missing project_id")
	}

	_, err = factory.NewListener(Watch{ProjectID: "proj-a"})
	if err == nil {
		t.Fatal("expected error for missing agent_name")
	}
}

type fakeNotifier struct {
	mu    sync.Mutex
	calls []notifyCall
	errs  []error
}

type blockingNotifier struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
}

func (n *blockingNotifier) Notify(_ context.Context, _, _ string) error {
	n.mu.Lock()
	n.calls++
	n.mu.Unlock()
	select {
	case n.started <- struct{}{}:
	default:
	}
	<-n.release
	return nil
}

func (n *blockingNotifier) callCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls
}

type notifyCall struct {
	title string
	body  string
}

func (n *fakeNotifier) Notify(_ context.Context, title, body string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, notifyCall{title: title, body: body})
	if len(n.errs) == 0 {
		return nil
	}
	err := n.errs[0]
	n.errs = n.errs[1:]
	return err
}

func (n *fakeNotifier) callCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.calls)
}

func (n *fakeNotifier) last() (string, string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.calls) == 0 {
		return "", ""
	}
	call := n.calls[len(n.calls)-1]
	return call.title, call.body
}

type fakeListenerFactory struct {
	mu        sync.Mutex
	listeners map[watchKey]*fakeListener
	created   chan Watch
}

func newFakeListenerFactory() *fakeListenerFactory {
	return &fakeListenerFactory{
		listeners: make(map[watchKey]*fakeListener),
		created:   make(chan Watch, 16),
	}
}

func (f *fakeListenerFactory) NewListener(w Watch) (Listener, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	listener := &fakeListener{done: make(chan struct{})}
	f.listeners[watchKey{projectID: w.ProjectID, agentName: w.AgentName}] = listener
	f.created <- w
	return listener, nil
}

func (f *fakeListenerFactory) waitForListener(t *testing.T, watch Watch) *fakeListener {
	t.Helper()

	deadline := time.After(2 * time.Second)
	for {
		f.mu.Lock()
		listener := f.listeners[watchKey{projectID: watch.ProjectID, agentName: watch.AgentName}]
		f.mu.Unlock()
		if listener != nil {
			return listener
		}

		select {
		case <-f.created:
		case <-deadline:
			t.Fatalf("listener for %+v was not created", watch)
		}
	}
}

type fakeListener struct {
	mu       sync.Mutex
	handler  DeliveryHandler
	done     chan struct{}
	incoming chan emittedDelivery
}

func (l *fakeListener) Listen(ctx context.Context, handler DeliveryHandler) error {
	l.mu.Lock()
	l.handler = handler
	if l.incoming == nil {
		l.incoming = make(chan emittedDelivery, 8)
	}
	incoming := l.incoming
	l.mu.Unlock()

	defer close(l.done)
	for {
		select {
		case <-ctx.Done():
			return nil
		case emitted := <-incoming:
			if err := handler(emitted.delivery); err != nil {
				emitted.result <- err
				return err
			}
			emitted.result <- nil
		}
	}
}

func (l *fakeListener) emit(delivery Delivery) error {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		incoming := l.incoming
		l.mu.Unlock()
		if incoming != nil {
			result := make(chan error, 1)
			select {
			case incoming <- emittedDelivery{delivery: delivery, result: result}:
			case <-time.After(500 * time.Millisecond):
				return errors.New("listener stream not accepting deliveries")
			}
			select {
			case err := <-result:
				return err
			case <-time.After(500 * time.Millisecond):
				return errors.New("listener stream did not respond")
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("listener handler not ready")
}

type restartingListenerFactory struct {
	mu        sync.Mutex
	listeners []*restartableListener
	created   chan *restartableListener
}

func newRestartingListenerFactory() *restartingListenerFactory {
	return &restartingListenerFactory{
		created: make(chan *restartableListener, 16),
	}
}

func (f *restartingListenerFactory) NewListener(_ Watch) (Listener, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l := &restartableListener{
		done:    make(chan struct{}),
		failCh:  make(chan error, 1),
		okCh:    make(chan struct{}, 1),
		readyCh: make(chan struct{}, 1),
	}
	f.listeners = append(f.listeners, l)
	f.created <- l
	return l, nil
}

func (f *restartingListenerFactory) waitForCreated(t *testing.T, want int) *restartableListener {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		f.mu.Lock()
		if len(f.listeners) >= want {
			l := f.listeners[want-1]
			f.mu.Unlock()
			return l
		}
		f.mu.Unlock()

		select {
		case <-f.created:
		case <-deadline:
			t.Fatalf("listener %d was not created", want)
		}
	}
}

type restartableListener struct {
	mu      sync.Mutex
	handler DeliveryHandler
	done    chan struct{}
	failCh  chan error
	okCh    chan struct{}
	readyCh chan struct{}
}

func (l *restartableListener) Listen(ctx context.Context, handler DeliveryHandler) error {
	l.mu.Lock()
	l.handler = handler
	l.mu.Unlock()
	select {
	case l.readyCh <- struct{}{}:
	default:
	}

	select {
	case <-ctx.Done():
		close(l.done)
		return nil
	case <-l.okCh:
		close(l.done)
		return nil
	case err := <-l.failCh:
		close(l.done)
		return err
	}
}

func (l *restartableListener) emit(delivery Delivery) error {
	select {
	case <-l.readyCh:
		defer func() {
			select {
			case l.readyCh <- struct{}{}:
			default:
			}
		}()
	case <-time.After(2 * time.Second):
		return errors.New("restartable listener not ready")
	}

	l.mu.Lock()
	handler := l.handler
	l.mu.Unlock()
	if handler == nil {
		return errors.New("restartable listener handler missing")
	}
	return handler(delivery)
}

func (l *restartableListener) fail(err error) {
	l.failCh <- err
}

func (l *restartableListener) succeed() {
	l.okCh <- struct{}{}
}

type emittedDelivery struct {
	delivery Delivery
	result   chan error
}

func waitFor(t *testing.T, name string, fn func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", name)
}

func setRuntimeReconcileIntervalForTest(t *testing.T, d time.Duration) {
	t.Helper()

	orig := config.Defaults.RuntimeReconcileInterval
	config.Defaults.RuntimeReconcileInterval = d
	t.Cleanup(func() {
		config.Defaults.RuntimeReconcileInterval = orig
	})
}

func setRuntimeRetrySweepIntervalForTest(t *testing.T, d time.Duration) {
	t.Helper()

	orig := config.Defaults.RuntimeNotificationRetrySweepInterval
	config.Defaults.RuntimeNotificationRetrySweepInterval = d
	t.Cleanup(func() {
		config.Defaults.RuntimeNotificationRetrySweepInterval = orig
	})
}
