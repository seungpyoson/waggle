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

// Manager owns runtime watches, listener lifecycles, persistence, and notifications.
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
	lastDeliveryErr error
}

func NewManager(store *Store, factory ListenerFactory, notifier Notifier) *Manager {
	return &Manager{
		store:    store,
		factory:  factory,
		notifier: notifier,
		ctx:      context.Background(),
		inflight: make(map[deliveryKey]struct{}),
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

	watches, err := m.store.ListWatches()
	if err != nil {
		cancel()
		m.reset()
		return err
	}

	for _, watch := range watches {
		m.wg.Add(1)
		go func(w Watch) {
			defer m.wg.Done()
			m.runPendingRetryLoop(runCtx, w)
		}(watch)

		m.wg.Add(1)
		go func(w Watch) {
			defer m.wg.Done()
			for {
				if err := runCtx.Err(); err != nil {
					return
				}
				listener, err := m.factory.NewListener(w)
				if err != nil {
					m.captureDeliveryError(fmt.Errorf("create listener for %s/%s: %w", w.ProjectID, w.AgentName, err))
					if !sleepWithContext(runCtx, config.Defaults.PollInterval) {
						return
					}
					continue
				}

				err = listener.Listen(runCtx, func(d Delivery) error {
					return m.handleDelivery(w, d)
				})
				if runCtx.Err() != nil {
					return
				}
				if err != nil {
					m.captureDeliveryError(fmt.Errorf("listen for %s/%s: %w", w.ProjectID, w.AgentName, err))
				}
				if !sleepWithContext(runCtx, config.Defaults.PollInterval) {
					return
				}
			}
		}(watch)
	}

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
			return nil
		}
	}
	return m.notifyRecord(w.ProjectID, w.AgentName, d.MessageID, notificationTitle(d), d.Body)
}

func (m *Manager) retryPendingNotifications(w Watch) error {
	records, err := m.store.PendingNotifications(w.ProjectID, w.AgentName)
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
		if err := m.notifyRecord(rec.ProjectID, rec.AgentName, rec.MessageID, notificationTitle(Delivery{FromName: rec.FromName}), rec.Body); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
		release()
	}
	return firstErr
}

func (m *Manager) runPendingRetryLoop(ctx context.Context, w Watch) {
	for {
		if err := m.retryPendingNotifications(w); err != nil {
			m.captureDeliveryError(fmt.Errorf("retry pending for %s/%s: %w", w.ProjectID, w.AgentName, err))
		}
		if !sleepWithContext(ctx, config.Defaults.PollInterval) {
			return
		}
	}
}

func (m *Manager) notifyRecord(projectID, agentName string, messageID int64, title, body string) error {
	if m.notifier != nil {
		if err := m.notifier.Notify(m.ctx, title, body); err != nil {
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
