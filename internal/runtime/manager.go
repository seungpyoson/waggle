package runtime

import (
	"context"
	"fmt"
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
	retries         map[deliveryKey]notificationRetry
	lastDeliveryErr error
	workers         map[watchKey]*watchWorker
}

type notificationRetry struct {
	attempts int
	nextTry  time.Time
}

func NewManager(store *Store, factory ListenerFactory, notifier Notifier) *Manager {
	return &Manager{
		store:    store,
		factory:  factory,
		notifier: notifier,
		ctx:      context.Background(),
		inflight: make(map[deliveryKey]struct{}),
		retries:  make(map[deliveryKey]notificationRetry),
		workers:  make(map[watchKey]*watchWorker),
	}
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
	return m.lastDeliveryErr
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
				m.captureDeliveryError(fmt.Errorf("reconcile watches: %w", err))
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
	m.retries = make(map[deliveryKey]notificationRetry)
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
			m.captureDeliveryError(fmt.Errorf("create listener for %s/%s: %w", w.ProjectID, w.AgentName, err))
			if !sleepWithContext(ctx, backoff) {
				return
			}
			backoff = nextReconnectBackoff(backoff)
			continue
		}

		err = listener.Listen(ctx, func(d Delivery) error {
			return m.handleDelivery(w, d)
		})
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			m.captureDeliveryError(fmt.Errorf("listen for %s/%s: %w", w.ProjectID, w.AgentName, err))
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
	if !inserted {
		existing, err := m.store.GetRecord(w.ProjectID, w.AgentName, d.MessageID)
		if err != nil {
			return err
		}
		if !existing.NotifiedAt.IsZero() {
			m.clearRetry(key)
			return nil
		}
	}
	if err := m.notifyRecord(w.ProjectID, w.AgentName, d.MessageID, notificationTitle(d), d.Body); err != nil {
		m.recordRetry(key)
		m.captureDeliveryError(fmt.Errorf("notify %s/%s/%d: %w", w.ProjectID, w.AgentName, d.MessageID, err))
		return nil
	}
	m.clearRetry(key)
	return nil
}

func (m *Manager) retryPendingNotifications() error {
	records, err := m.store.PendingNotificationsBatch(config.Defaults.RuntimeNotificationRetryBatchSize)
	if err != nil {
		return err
	}
	var firstErr error
	for _, rec := range records {
		key := deliveryKey{
			projectID: rec.ProjectID,
			agentName: rec.AgentName,
			messageID: rec.MessageID,
		}
		release, ok := m.beginInflight(key)
		if !ok {
			continue
		}
		if !m.shouldRetry(key) {
			release()
			continue
		}
		if err := m.notifyRecord(rec.ProjectID, rec.AgentName, rec.MessageID, notificationTitle(Delivery{FromName: rec.FromName}), rec.Body); err != nil {
			m.recordRetry(key)
			if firstErr == nil {
				firstErr = err
			}
		} else {
			m.clearRetry(key)
		}
		release()
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
				m.captureDeliveryError(fmt.Errorf("retry pending notifications: %w", err))
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
	m.retries = make(map[deliveryKey]notificationRetry)
	m.workers = make(map[watchKey]*watchWorker)
}

func (m *Manager) captureDeliveryError(err error) {
	if err == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastDeliveryErr = err
}

func notificationTitle(d Delivery) string {
	if d.FromName == "" {
		return "New waggle message"
	}
	return fmt.Sprintf("Message from %s", d.FromName)
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

func (m *Manager) shouldRetry(key deliveryKey) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.retries[key]
	if !ok {
		return true
	}
	if state.attempts >= config.Defaults.RuntimeNotificationRetryLimit {
		return false
	}
	return time.Now().UTC().After(state.nextTry) || time.Now().UTC().Equal(state.nextTry)
}

func (m *Manager) recordRetry(key deliveryKey) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.retries[key]
	state.attempts++
	state.nextTry = time.Now().UTC().Add(retryDelayForAttempt(state.attempts))
	m.retries[key] = state
}

func (m *Manager) clearRetry(key deliveryKey) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.retries, key)
}

func (m *Manager) clearDeliveryStateForWatch(key watchKey) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for deliveryKey := range m.inflight {
		if deliveryKey.projectID == key.projectID && deliveryKey.agentName == key.agentName {
			delete(m.inflight, deliveryKey)
		}
	}
	for deliveryKey := range m.retries {
		if deliveryKey.projectID == key.projectID && deliveryKey.agentName == key.agentName {
			delete(m.retries, deliveryKey)
		}
	}
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
			if _, err := m.store.PruneExpiredWatches(now); err != nil {
				m.captureDeliveryError(fmt.Errorf("prune expired watches: %w", err))
			}
			if _, err := m.store.PruneDeliveryRecords(now.Add(-config.Defaults.RuntimeDeliveryRetention)); err != nil {
				m.captureDeliveryError(fmt.Errorf("prune delivery records: %w", err))
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
