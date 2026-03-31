package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
)

// Notifier surfaces a delivery to the local machine.
type Notifier interface {
	Notify(ctx context.Context, title, body string) error
}

// Delivery is a pushed broker message scoped to a watch.
type Delivery struct {
	MessageID  int64
	FromName   string
	Body       string
	SentAt     time.Time
	ReceivedAt time.Time
}

// DeliveryHandler handles one received delivery.
type DeliveryHandler func(Delivery) error

// Listener streams deliveries for a single watch until the context is canceled.
type Listener interface {
	Listen(ctx context.Context, handler DeliveryHandler) error
}

// ListenerFactory creates listeners for persisted watches.
type ListenerFactory interface {
	NewListener(w Watch) (Listener, error)
	CatchUp(w Watch, handler DeliveryHandler) error
}

type watchKey struct {
	projectID string
	agentName string
}

type watchWorker struct {
	watch   Watch
	cancel  context.CancelFunc
	stopped chan struct{}
}

// Manager owns runtime watch reconciliation, listener lifecycles, persistence, and notifications.
type Manager struct {
	store           *Store
	factory         ListenerFactory
	notifier        Notifier
	mu              sync.Mutex
	cancel          context.CancelFunc
	ctx             context.Context
	wg              sync.WaitGroup
	started         bool
	inflight        map[deliveryKey]struct{}
	pendingFailures map[watchKey]int
	deliveryCauses  map[watchKey]error
	lastDeliveryErr map[string]error
	workers         map[watchKey]*watchWorker
	recentErrors    []ErrorEntry // ring buffer, capped at config.Defaults.RuntimeRecentErrorCap
	signalDir       string       // set via SetSignalDir; enables shell-hook signal files
}

func NewManager(store *Store, factory ListenerFactory, notifier Notifier) *Manager {
	return &Manager{
		store:           store,
		factory:         factory,
		notifier:        notifier,
		ctx:             context.Background(),
		inflight:        make(map[deliveryKey]struct{}),
		pendingFailures: make(map[watchKey]int),
		deliveryCauses:  make(map[watchKey]error),
		lastDeliveryErr: make(map[string]error),
		workers:         make(map[watchKey]*watchWorker),
	}
}

// SetSignalDir enables signal file writing for shell-hook delivery.
func (m *Manager) SetSignalDir(dir string) {
	m.signalDir = dir
}

func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return fmt.Errorf("runtime manager already started")
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.ctx = runCtx
	m.started = true
	m.mu.Unlock()

	if err := m.reconcile(runCtx); err != nil {
		cancel()
		m.reset()
		return err
	}

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.runReconcileLoop(runCtx)
	}()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.runMaintenanceLoop(runCtx)
	}()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.runRetrySweepLoop(runCtx)
	}()

	return nil
}

func (m *Manager) Stop() error {
	m.mu.Lock()
	cancel := m.cancel
	started := m.started
	m.mu.Unlock()

	if !started {
		return nil
	}
	if cancel != nil {
		cancel()
	}
	m.wg.Wait()
	m.reset()
	return nil
}

func (m *Manager) WatchCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.workers)
}

func (m *Manager) LastDeliveryError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.lastDeliveryErr) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m.lastDeliveryErr))
	for key := range m.lastDeliveryErr {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s: %v", key, m.lastDeliveryErr[key]))
	}
	return fmt.Errorf("%s", strings.Join(parts, "; "))
}

// RecentErrors returns a snapshot copy of the error ring buffer (thread-safe).
func (m *Manager) RecentErrors() []ErrorEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]ErrorEntry, len(m.recentErrors))
	copy(result, m.recentErrors)
	return result
}

func (m *Manager) runReconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(config.Defaults.RuntimeReconcileInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.stopAllWorkers()
			return
		case <-ticker.C:
			if err := m.reconcile(ctx); err != nil {
				m.captureDeliveryError("reconcile", fmt.Errorf("reconcile watches: %w", err))
			}
		}
	}
}

func (m *Manager) reconcile(ctx context.Context) error {
	watches, err := m.store.ListWatches()
	if err != nil {
		return err
	}

	desired := make(map[watchKey]Watch, len(watches))
	for _, watch := range watches {
		key := watchKey{projectID: watch.ProjectID, agentName: watch.AgentName}
		desired[key] = watch
	}

	for key, watch := range desired {
		m.startWatch(ctx, key, watch)
	}

	m.mu.Lock()
	var stale []watchKey
	for key := range m.workers {
		if _, ok := desired[key]; !ok {
			stale = append(stale, key)
		}
	}
	m.mu.Unlock()

	for _, key := range stale {
		m.stopWatch(key)
	}
	m.clearDeliveryError("reconcile")
	return nil
}

func (m *Manager) startWatch(ctx context.Context, key watchKey, watch Watch) {
	workerCtx, cancel := context.WithCancel(ctx)
	worker := &watchWorker{
		watch:   watch,
		cancel:  cancel,
		stopped: make(chan struct{}),
	}

	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		cancel()
		return
	}
	if _, exists := m.workers[key]; exists {
		m.mu.Unlock()
		cancel()
		return
	}
	m.workers[key] = worker
	m.mu.Unlock()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer close(worker.stopped)
		m.runWatch(workerCtx, watch)
	}()
}

func (m *Manager) stopWatch(key watchKey) {
	m.mu.Lock()
	worker, ok := m.workers[key]
	if ok {
		delete(m.workers, key)
	}
	m.mu.Unlock()

	if !ok {
		return
	}
	worker.cancel()
	<-worker.stopped
	m.clearDeliveryStateForWatch(key)
	m.clearDeliveryError(watchListenerErrorKey(worker.watch))
	m.clearDeliveryError(watchCatchUpErrorKey(worker.watch))
	m.clearDeliveryError(watchDeliveryErrorKey(worker.watch.ProjectID, worker.watch.AgentName))
	_ = m.refreshWatchDeliveryState(worker.watch.ProjectID, worker.watch.AgentName)
}

func (m *Manager) stopAllWorkers() {
	m.mu.Lock()
	workers := make([]*watchWorker, 0, len(m.workers))
	for key, worker := range m.workers {
		delete(m.workers, key)
		workers = append(workers, worker)
	}
	m.mu.Unlock()

	for _, worker := range workers {
		worker.cancel()
		<-worker.stopped
	}

	m.mu.Lock()
	m.inflight = make(map[deliveryKey]struct{})
	m.mu.Unlock()
}

func (m *Manager) runWatch(ctx context.Context, w Watch) {
	backoff := config.Defaults.PollInterval
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		listener, err := m.factory.NewListener(w)
		if err != nil {
			m.captureDeliveryError(watchListenerErrorKey(w), fmt.Errorf("create listener for %s/%s: %w", w.ProjectID, w.AgentName, err))
			if !sleepWithContext(ctx, backoff) {
				return
			}
			backoff = nextReconnectBackoff(backoff)
			continue
		}
		m.clearDeliveryError(watchListenerErrorKey(w))
		// Catch up on messages queued in the broker during disconnect.
		// handleDelivery deduplicates via AddRecordIfAbsent, so overlaps with
		// listener delivery are harmless.
		for attempt := 1; attempt <= config.Defaults.CatchUpMaxRetries; attempt++ {
			if err := m.factory.CatchUp(w, func(d Delivery) error {
				return m.handleDelivery(w, d)
			}); err != nil {
				m.captureDeliveryError(watchCatchUpErrorKey(w),
					fmt.Errorf("catch-up inbox for %s/%s (attempt %d/%d): %w",
						w.ProjectID, w.AgentName, attempt, config.Defaults.CatchUpMaxRetries, err))
				if attempt < config.Defaults.CatchUpMaxRetries {
					if !sleepWithContext(ctx, config.Defaults.PollInterval) {
						return
					}
				}
				continue
			}
			m.clearDeliveryError(watchCatchUpErrorKey(w))
			break
		}

		err = listener.Listen(ctx, func(d Delivery) error {
			return m.handleDelivery(w, d)
		})
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			m.captureDeliveryError(watchListenerErrorKey(w), fmt.Errorf("listen for %s/%s: %w", w.ProjectID, w.AgentName, err))
			if !sleepWithContext(ctx, backoff) {
				return
			}
			backoff = nextReconnectBackoff(backoff)
			continue
		}
		backoff = config.Defaults.PollInterval
		if !sleepWithContext(ctx, config.Defaults.PollInterval) {
			return
		}
	}
}

func (m *Manager) handleDelivery(w Watch, d Delivery) error {
	key := deliveryKey{
		projectID: w.ProjectID,
		agentName: w.AgentName,
		messageID: d.MessageID,
	}
	release, ok := m.beginInflight(key)
	if !ok {
		return nil
	}
	defer release()

	record := DeliveryRecord{
		ProjectID:  w.ProjectID,
		AgentName:  w.AgentName,
		MessageID:  d.MessageID,
		FromName:   d.FromName,
		Body:       d.Body,
		SentAt:     d.SentAt,
		ReceivedAt: d.ReceivedAt,
	}
	inserted, err := m.store.AddRecordIfAbsent(record)
	if err != nil {
		return err
	}
	currentRecord := record
	if !inserted {
		existing, err := m.store.GetRecord(w.ProjectID, w.AgentName, d.MessageID)
		if err != nil {
			return err
		}
		currentRecord = existing
		if !existing.NotifiedAt.IsZero() {
			return nil
		}
		if !existing.RetryExhaustedAt.IsZero() {
			return nil
		}
		if !existing.RetryNextAt.IsZero() && existing.RetryNextAt.After(time.Now().UTC()) {
			return nil
		}
	}
	if err := m.notifyRecord(w.ProjectID, w.AgentName, d.MessageID, notificationTitle(d), d.Body); err != nil {
		releasePending := m.beginPendingFailure(watchKey{projectID: w.ProjectID, agentName: w.AgentName})
		recordErr := m.recordNotificationFailure(currentRecord)
		if recordErr != nil {
			releasePending()
			return recordErr
		}
		m.setDeliveryFailureCause(watchKey{projectID: w.ProjectID, agentName: w.AgentName}, err)
		releasePending()
		_ = m.refreshWatchDeliveryState(w.ProjectID, w.AgentName)
		return nil
	}
	_ = m.refreshWatchDeliveryState(w.ProjectID, w.AgentName)
	return nil
}

func (m *Manager) retryPendingNotifications() error {
	records, err := m.store.PendingNotificationsBatch(config.Defaults.RuntimeNotificationRetryBatchSize)
	if err != nil {
		return err
	}
	var firstErr error
	affected := make(map[watchKey]struct{})
	for _, rec := range records {
		key := deliveryKey{
			projectID: rec.ProjectID,
			agentName: rec.AgentName,
			messageID: rec.MessageID,
		}
		watch := watchKey{projectID: rec.ProjectID, agentName: rec.AgentName}
		affected[watch] = struct{}{}
		release, ok := m.beginInflight(key)
		if !ok {
			continue
		}
		if err := m.notifyRecord(rec.ProjectID, rec.AgentName, rec.MessageID, notificationTitle(Delivery{FromName: rec.FromName}), rec.Body); err != nil {
			releasePending := m.beginPendingFailure(watch)
			recordErr := m.recordNotificationFailure(rec)
			if recordErr != nil {
				if firstErr == nil {
					firstErr = recordErr
				}
			} else {
				m.setDeliveryFailureCause(watch, err)
			}
			releasePending()
		}
		release()
	}
	for watch := range affected {
		_ = m.refreshWatchDeliveryState(watch.projectID, watch.agentName)
	}
	if firstErr == nil {
		m.clearDeliveryError("retry-sweep")
	}
	return firstErr
}

func (m *Manager) runRetrySweepLoop(ctx context.Context) {
	ticker := time.NewTicker(config.Defaults.RuntimeNotificationRetrySweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.retryPendingNotifications(); err != nil {
				m.captureDeliveryError("retry-sweep", fmt.Errorf("retry pending notifications: %w", err))
			}
		}
	}
}

func (m *Manager) notifyRecord(projectID, agentName string, messageID int64, title, body string) error {
	if m.notifier != nil {
		notifyCtx, cancel := context.WithTimeout(m.ctx, config.Defaults.RuntimeNotificationTimeout)
		defer cancel()
		if err := m.notifier.Notify(notifyCtx, title, body); err != nil {
			return err
		}
	}
	if m.signalDir != "" {
		_ = WriteSignal(m.signalDir, projectID, agentName, senderFromTitle(title), body, config.Defaults.SignalMaxBytes)
	}
	if err := m.store.MarkNotified(projectID, agentName, messageID, time.Now().UTC()); err != nil {
		return err
	}
	return nil
}

func (m *Manager) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancel = nil
	m.ctx = context.Background()
	m.started = false
	m.inflight = make(map[deliveryKey]struct{})
	m.pendingFailures = make(map[watchKey]int)
	m.deliveryCauses = make(map[watchKey]error)
	m.lastDeliveryErr = make(map[string]error)
	m.workers = make(map[watchKey]*watchWorker)
}

func (m *Manager) captureDeliveryError(key string, err error) {
	if key == "" || err == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastDeliveryErr[key] = err

	// Add to ring buffer, evicting oldest if at capacity
	entry := ErrorEntry{
		Timestamp: time.Now().UTC(),
		WatchKey:  key,
		Error:     err.Error(),
	}
	if len(m.recentErrors) >= config.Defaults.RuntimeRecentErrorCap {
		m.recentErrors = m.recentErrors[1:]
	}
	m.recentErrors = append(m.recentErrors, entry)
}

func (m *Manager) clearDeliveryError(key string) {
	if key == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.lastDeliveryErr, key)
}

func watchListenerErrorKey(w Watch) string {
	return fmt.Sprintf("watch-listener/%s/%s", w.ProjectID, w.AgentName)
}

func watchCatchUpErrorKey(w Watch) string {
	return fmt.Sprintf("watch-catchup/%s/%s", w.ProjectID, w.AgentName)
}

func watchDeliveryErrorKey(projectID, agentName string) string {
	return fmt.Sprintf("watch-delivery/%s/%s", projectID, agentName)
}

func backlogDeliveryErrorKey(projectID, agentName string) string {
	return fmt.Sprintf("backlog-delivery/%s/%s", projectID, agentName)
}

func refreshDeliveryStatusErrorKey(projectID, agentName string) string {
	return fmt.Sprintf("delivery-status/%s/%s", projectID, agentName)
}

func (m *Manager) refreshDeliveryErrorState(projectID, agentName string) error {
	key := watchKey{projectID: projectID, agentName: agentName}

	m.mu.Lock()
	defer m.mu.Unlock()

	summary, err := m.store.DeliveryFailureSummary(projectID, agentName)
	if err != nil {
		return err
	}
	if summary.Retrying == 0 && summary.Exhausted == 0 && m.pendingFailures[key] > 0 {
		summary.Retrying = 1
	}
	watchKey := watchDeliveryErrorKey(projectID, agentName)
	backlogKey := backlogDeliveryErrorKey(projectID, agentName)
	if summary.Retrying == 0 && summary.Exhausted == 0 {
		delete(m.lastDeliveryErr, watchKey)
		delete(m.lastDeliveryErr, backlogKey)
		delete(m.deliveryCauses, key)
		return nil
	}

	errText := fmt.Sprintf("%d unresolved delivery failures for %s/%s (%d retrying, %d exhausted)", summary.Retrying+summary.Exhausted, projectID, agentName, summary.Retrying, summary.Exhausted)
	if cause := m.deliveryCauses[key]; cause != nil {
		errText = fmt.Sprintf("%s; last notifier error: %v", errText, cause)
	}
	errMsg := fmt.Errorf("%s", errText)
	if _, active := m.workers[key]; active {
		delete(m.lastDeliveryErr, backlogKey)
		m.lastDeliveryErr[watchKey] = errMsg
		return nil
	}
	delete(m.lastDeliveryErr, watchKey)
	m.lastDeliveryErr[backlogKey] = errMsg
	return nil
}

func (m *Manager) refreshWatchDeliveryState(projectID, agentName string) error {
	err := m.refreshDeliveryErrorState(projectID, agentName)
	refreshKey := refreshDeliveryStatusErrorKey(projectID, agentName)
	if err != nil {
		m.captureDeliveryError(refreshKey, fmt.Errorf("refresh delivery status for %s/%s: %w", projectID, agentName, err))
		return err
	}
	m.clearDeliveryError(refreshKey)
	return nil
}

func notificationTitle(d Delivery) string {
	if d.FromName == "" {
		return "New waggle message"
	}
	return fmt.Sprintf("Message from %s", d.FromName)
}

func senderFromTitle(title string) string {
	const prefix = "Message from "
	if strings.HasPrefix(title, prefix) {
		return strings.TrimPrefix(title, prefix)
	}
	return "unknown"
}

type deliveryKey struct {
	projectID string
	agentName string
	messageID int64
}

func (m *Manager) beginInflight(key deliveryKey) (func(), bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.inflight[key]; exists {
		return nil, false
	}
	m.inflight[key] = struct{}{}

	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		delete(m.inflight, key)
	}, true
}

func (m *Manager) clearDeliveryStateForWatch(key watchKey) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// stopWatch only owns worker-scoped state. Detached retry-sweep state may still
	// hold inflight dedupe or pending-failure bookkeeping for the same watch/message,
	// and clearing it here would corrupt state owned by another active path.
}

func (m *Manager) recordNotificationFailure(rec DeliveryRecord) error {
	attempts := rec.RetryAttempts + 1
	now := time.Now().UTC()
	if attempts >= config.Defaults.RuntimeNotificationRetryLimit {
		return m.store.RecordNotificationFailure(rec.ProjectID, rec.AgentName, rec.MessageID, attempts, time.Time{}, now)
	}
	return m.store.RecordNotificationFailure(rec.ProjectID, rec.AgentName, rec.MessageID, attempts, now.Add(retryDelayForAttempt(attempts)), time.Time{})
}

func (m *Manager) beginPendingFailure(key watchKey) func() {
	m.mu.Lock()
	m.pendingFailures[key]++
	m.mu.Unlock()

	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.pendingFailures[key] <= 1 {
			delete(m.pendingFailures, key)
			return
		}
		m.pendingFailures[key]--
	}
}

func (m *Manager) setDeliveryFailureCause(key watchKey, err error) {
	if err == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deliveryCauses[key] = err
}

func (m *Manager) trackedDeliveryStatusWatches() []watchKey {
	m.mu.Lock()
	defer m.mu.Unlock()

	seen := make(map[watchKey]struct{})
	for key := range m.deliveryCauses {
		seen[key] = struct{}{}
	}
	for errKey := range m.lastDeliveryErr {
		if key, ok := trackedDeliveryStatusWatch(errKey); ok {
			seen[key] = struct{}{}
		}
	}

	watches := make([]watchKey, 0, len(seen))
	for key := range seen {
		watches = append(watches, key)
	}
	return watches
}

func trackedDeliveryStatusWatch(errKey string) (watchKey, bool) {
	for _, prefix := range []string{"watch-delivery/", "backlog-delivery/", "delivery-status/"} {
		if !strings.HasPrefix(errKey, prefix) {
			continue
		}
		parts := strings.SplitN(strings.TrimPrefix(errKey, prefix), "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return watchKey{}, false
		}
		return watchKey{projectID: parts[0], agentName: parts[1]}, true
	}
	return watchKey{}, false
}

func (m *Manager) refreshTrackedDeliveryStates() error {
	var firstErr error
	for _, key := range m.trackedDeliveryStatusWatches() {
		if err := m.refreshWatchDeliveryState(key.projectID, key.agentName); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func retryDelayForAttempt(attempt int) time.Duration {
	if attempt <= 0 {
		return config.Defaults.PollInterval
	}

	delay := config.Defaults.PollInterval
	for i := 1; i < attempt; i++ {
		if delay >= 32*config.Defaults.PollInterval {
			return 32 * config.Defaults.PollInterval
		}
		delay *= 2
	}
	if delay > 32*config.Defaults.PollInterval {
		return 32 * config.Defaults.PollInterval
	}
	return delay
}

func nextReconnectBackoff(current time.Duration) time.Duration {
	if current <= 0 {
		return config.Defaults.PollInterval
	}
	if current >= config.Defaults.RuntimeReconnectMaxBackoff {
		return config.Defaults.RuntimeReconnectMaxBackoff
	}
	next := current * 2
	if next > config.Defaults.RuntimeReconnectMaxBackoff {
		return config.Defaults.RuntimeReconnectMaxBackoff
	}
	return next
}

func (m *Manager) runMaintenanceLoop(ctx context.Context) {
	ticker := time.NewTicker(config.Defaults.TTLCheckPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().UTC()
			var hadErr bool
			if _, err := m.store.PruneExpiredWatches(now); err != nil {
				hadErr = true
				m.captureDeliveryError("maintenance", fmt.Errorf("prune expired watches: %w", err))
			}
			if _, err := m.store.PruneDeliveryRecords(now.Add(-config.Defaults.RuntimeDeliveryRetention)); err != nil {
				hadErr = true
				m.captureDeliveryError("maintenance", fmt.Errorf("prune delivery records: %w", err))
			}
			if m.signalDir != "" {
				PruneStaleFiles(filepath.Dir(m.signalDir), "agent-ppid-", 24*time.Hour)
				PruneStaleSignals(m.signalDir, 24*time.Hour)
			}
			if err := m.refreshTrackedDeliveryStates(); err != nil {
				hadErr = true
				m.captureDeliveryError("maintenance", fmt.Errorf("refresh tracked delivery states: %w", err))
			}
			if !hadErr {
				m.clearDeliveryError("maintenance")
			}
		}
	}
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
