package broker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/seungpyoson/waggle/internal/broker/enrollment"
	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/events"
	"github.com/seungpyoson/waggle/internal/locks"
	"github.com/seungpyoson/waggle/internal/messages"
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/seungpyoson/waggle/internal/tasks"
)

// Broker is the main broker orchestrator
type Broker struct {
	config     config.BrokerConfig
	hub        *events.Hub
	owner      *brokerstate.Owner
	enrollment *enrollment.Supervisor
	lockMgr    *locks.Manager
	sessions   map[string]*Session
	mu         sync.RWMutex
}

// New attaches the broker to an acquired canonical owner, binds its endpoints
// and registers maintenance and enrollment work. It has no database
// constructor and no endpoint cleanup authority of its own.
func New(ctx context.Context, owner *brokerstate.Owner, cfg config.BrokerConfig, provider config.ProviderConfig) (*Broker, error) {
	connector, err := enrollment.NewConnector(provider)
	if err != nil {
		return nil, err
	}
	return newWithConnector(ctx, owner, cfg, provider.Transport, connector)
}

func newWithConnector(ctx context.Context, owner *brokerstate.Owner, cfg config.BrokerConfig, transport config.NativeConfig, connector enrollment.Connector) (*Broker, error) {
	if err := config.ValidateDefaults(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	// Complete startup persistence before binding or reporting daemon readiness.
	if err := owner.Do(ctx, func(op *brokerstate.Operation) error {
		return op.Write(ctx, func(tx *brokerstate.WriteTx) error {
			if err := tx.RequireActive(); err != nil {
				return err
			}
			if _, err := tasks.NewStore(tx).RequeueAllClaimed(); err != nil {
				return err
			}
			_, err := messages.NewStore(tx, cfg.Messaging, time.Now()).Apply(messages.Restart{})
			return err
		})
	}); err != nil {
		return nil, fmt.Errorf("recover task claims: %w", err)
	}
	if err := owner.Bind(ctx, cfg.Endpoints); err != nil {
		return nil, err
	}
	b := &Broker{
		config: cfg, owner: owner, hub: events.NewHub(), lockMgr: locks.NewManager(),
		sessions: make(map[string]*Session),
	}
	if err := owner.StartWork(ctx, "maintenance", brokerstate.ManagedWork{Run: b.maintain}); err != nil {
		return nil, err
	}
	supervisor, err := enrollment.Start(ctx, owner, connector, cfg.Messaging, transport)
	if err != nil {
		return nil, err
	}
	b.enrollment = supervisor
	return b, nil
}

// Initialize creates domain tables and activates an explicitly prepared fresh
// store in one owned transaction. Opening an existing store never calls it.
func Initialize(ctx context.Context, owner *brokerstate.Owner) error {
	return owner.Do(ctx, func(op *brokerstate.Operation) error {
		return op.Write(ctx, func(tx *brokerstate.WriteTx) error {
			if _, err := tx.Exec(tasks.Schema()); err != nil {
				return err
			}
			if _, err := tx.Exec(messages.Schema); err != nil {
				return err
			}
			_, err := tx.Exec("UPDATE cutover SET state = 'active' WHERE singleton = 1 AND state = 'prepared'")
			return err
		})
	})
}

// Serve runs ingress until the owner closes the listener. Workers are
// registered separately and joined by the owner, not by this call.
func (b *Broker) Serve(ctx context.Context) error {
	return b.owner.Serve(ctx, func(conn net.Conn) {
		// Each connection holds its own admission so its cleanup transaction
		// stays admitted until this read loop returns.
		_ = b.owner.Do(ctx, func(lifetime *brokerstate.Operation) error { newSession(conn, b).readLoop(lifetime); return nil })
	})
}

// Shutdown begins orderly draining and waits under the caller's bound only.
func (b *Broker) Shutdown(ctx context.Context) error {
	b.owner.BeginShutdown(nil)
	return b.owner.Wait(ctx)
}

func (b *Broker) maintain(lifetime brokerstate.WorkLifetime, reportFatal func(error)) {
	lease := time.NewTicker(b.config.LeaseCheckPeriod)
	taskTTL := time.NewTicker(b.config.TaskTTLCheckPeriod)
	defer lease.Stop()
	defer taskTTL.Stop()
	for {
		var change func(*brokerstate.WriteTx) error
		var effects []protocol.Event
		select {
		case <-lifetime.Stop:
			return
		case <-lease.C:
			change = func(tx *brokerstate.WriteTx) error { _, err := tasks.NewStore(tx).RequeueExpiredLeases(); return err }
		case <-taskTTL.C:
			change = func(tx *brokerstate.WriteTx) error {
				store := tasks.NewStore(tx)
				if _, err := store.CancelExpiredTTL(); err != nil {
					return err
				}
				health, err := store.QueueHealth(b.config.TaskStaleThreshold)
				if err != nil {
					return err
				}
				if health.StaleCount > 0 {
					effects = append(effects, protocol.Event{
						Topic: "task.events", Event: "task.stale",
						Data: mustMarshal(map[string]any{
							"stale_count": health.StaleCount, "oldest_age_seconds": health.OldestPendingAge,
						}),
						TS: time.Now().UTC().Format(time.RFC3339),
					})
				}
				return nil
			}
		}
		err := b.owner.Do(lifetime.Interrupt, func(op *brokerstate.Operation) error { return op.Write(lifetime.Interrupt, change) })
		if errors.Is(err, brokerstate.ErrAdmissionClosed) {
			return
		}
		if err != nil {
			// An owner interruption cancels admitted work; it is the owner's
			// own signal, not a new maintenance failure to report back to it.
			if cause := lifetime.Interrupt.Err(); cause != nil && errors.Is(err, cause) {
				return
			}
			reportFatal(fmt.Errorf("canonical maintenance: %w", err))
			return
		}
		for _, event := range effects {
			b.hub.Publish(event.Topic, mustMarshal(event))
		}
	}
}
