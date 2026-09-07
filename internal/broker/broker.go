package broker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/seungpyoson/waggle/internal/brokerstate"

	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/driver"
	"github.com/seungpyoson/waggle/internal/events"
	"github.com/seungpyoson/waggle/internal/locks"
	"github.com/seungpyoson/waggle/internal/messages"
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/seungpyoson/waggle/internal/tasks"
)

// Broker is the main broker orchestrator
type Broker struct {
	config   config.BrokerConfig
	hub      *events.Hub
	owner    *brokerstate.Owner
	native   driver.Connector
	wake     chan struct{}
	lockMgr  *locks.Manager
	sessions map[string]*Session
	mu       sync.RWMutex
	stopCh   <-chan struct{}
	wg       sync.WaitGroup
}

// New attaches the broker to an acquired canonical owner. It has no database
// constructor and no endpoint cleanup authority of its own.
func New(ctx context.Context, owner *brokerstate.Owner, cfg config.BrokerConfig, native driver.Connector) (*Broker, error) {
	if native == nil {
		return nil, fmt.Errorf("broker requires its native connector")
	}
	if err := config.ValidateDefaults(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	stopping, err := owner.Stopping()
	if err != nil {
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
	return &Broker{
		native: native, wake: make(chan struct{}, 1),
		config: cfg, owner: owner, hub: events.NewHub(), lockMgr: locks.NewManager(),
		sessions: make(map[string]*Session),
		stopCh:   stopping,
	}, nil
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

func (b *Broker) Serve() error {
	return b.owner.Do(context.Background(), func(lifetime *brokerstate.Operation) error {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		workers := []func(context.Context) error{b.maintain, b.schedule}
		failures := make(chan error, len(workers))
		for _, work := range workers {
			b.wg.Add(1)
			go func() {
				defer b.wg.Done()
				if err := work(ctx); err != nil {
					failures <- err
					go b.Shutdown()
				}
			}()
		}
		err := b.owner.Serve(ctx, func(conn net.Conn) { newSession(conn, b).readLoop(lifetime) })
		if err != nil {
			cancel()
		}
		b.wg.Wait()
		close(failures)
		for failure := range failures {
			err = errors.Join(err, failure)
		}
		return err
	})
}

func (b *Broker) Shutdown() error { return b.owner.Shutdown(context.Background()) }

func (b *Broker) maintain(ctx context.Context) error {
	lease := time.NewTicker(b.config.LeaseCheckPeriod)
	taskTTL := time.NewTicker(b.config.TaskTTLCheckPeriod)
	defer lease.Stop()
	defer taskTTL.Stop()
	for {
		var change func(*brokerstate.WriteTx) error
		var effects []protocol.Event
		select {
		case <-ctx.Done():
			return nil
		case <-b.stopCh:
			return nil
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
		err := b.owner.Do(ctx, func(op *brokerstate.Operation) error { return op.Write(ctx, change) })
		if errors.Is(err, brokerstate.ErrAdmissionClosed) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("canonical maintenance: %w", err)
		}
		for _, event := range effects {
			b.hub.Publish(event.Topic, mustMarshal(event))
		}
	}
}
