package broker

import (
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/events"
	"github.com/seungpyoson/waggle/internal/locks"
	"github.com/seungpyoson/waggle/internal/tasks"
)

// Config holds broker configuration
type Config struct {
	SocketPath       string
	DBPath           string
	LeaseCheckPeriod time.Duration
	IdleTimeout      time.Duration
}

// Broker is the main broker orchestrator
type Broker struct {
	config   Config
	hub      *events.Hub
	store    *tasks.Store
	lockMgr  *locks.Manager
	listener net.Listener
	sessions map[string]*Session
	mu       sync.RWMutex
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// New creates a new broker instance
func New(cfg Config) (*Broker, error) {
	// Set defaults
	if cfg.LeaseCheckPeriod == 0 {
		cfg.LeaseCheckPeriod = 30 * time.Second
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = config.Defaults.IdleTimeout
	}

	// Open task store
	store, err := tasks.NewStore(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("opening task store: %w", err)
	}

	// Clean up stale socket
	if err := cleanupSocket(cfg.SocketPath); err != nil {
		store.Close()
		return nil, err
	}

	// Create listener
	listener, err := net.Listen("unix", cfg.SocketPath)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("listening on socket: %w", err)
	}

	// Set socket permissions to 0700
	if err := os.Chmod(cfg.SocketPath, 0700); err != nil {
		listener.Close()
		store.Close()
		return nil, fmt.Errorf("setting socket permissions: %w", err)
	}

	b := &Broker{
		config:   cfg,
		hub:      events.NewHub(),
		store:    store,
		lockMgr:  locks.NewManager(),
		listener: listener,
		sessions: make(map[string]*Session),
		stopCh:   make(chan struct{}),
	}

	return b, nil
}

// Serve starts the broker's accept loop and background tasks
func (b *Broker) Serve() error {
	// Crash recovery: re-queue all claimed tasks
	count, err := b.store.RequeueAllClaimed()
	if err != nil {
		log.Printf("broker: error requeuing claimed tasks on startup: %v", err)
	} else if count > 0 {
		log.Printf("broker: requeued %d claimed tasks on startup", count)
	}

	// Start lease checker
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		tasks.StartLeaseChecker(b.store, b.config.LeaseCheckPeriod, b.stopCh)
	}()

	// Start idle timeout monitor
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		b.monitorIdleTimeout()
	}()

	// Accept loop
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			select {
			case <-b.stopCh:
				return nil
			default:
				log.Printf("broker: accept error: %v", err)
				continue
			}
		}

		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			sess := newSession(conn, b)
			sess.readLoop()
		}()
	}
}

// Shutdown gracefully shuts down the broker
func (b *Broker) Shutdown() error {
	close(b.stopCh)

	// Close listener
	if b.listener != nil {
		b.listener.Close()
	}

	// Close all session connections (cleanup will be called by readLoop)
	b.mu.Lock()
	for _, sess := range b.sessions {
		sess.conn.Close()
	}
	b.mu.Unlock()

	// Wait for goroutines
	b.wg.Wait()

	// Close store
	if b.store != nil {
		b.store.Close()
	}

	// Remove socket file
	if b.config.SocketPath != "" {
		os.Remove(b.config.SocketPath)
	}

	return nil
}

// monitorIdleTimeout monitors session count and shuts down broker after idle timeout
func (b *Broker) monitorIdleTimeout() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var idleStart time.Time

	for {
		select {
		case <-b.stopCh:
			return
		case <-ticker.C:
			b.mu.RLock()
			sessionCount := len(b.sessions)
			b.mu.RUnlock()

			if sessionCount == 0 {
				if idleStart.IsZero() {
					idleStart = time.Now()
				} else if time.Since(idleStart) >= b.config.IdleTimeout {
					log.Printf("broker: idle timeout reached, shutting down")
					// Shutdown the broker
					go b.Shutdown()
					return
				}
			} else {
				idleStart = time.Time{} // reset
			}
		}
	}
}

// cleanupSocket removes stale socket file
func cleanupSocket(path string) error {
	if _, err := os.Stat(path); err == nil {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("removing stale socket: %w", err)
		}
	}
	return nil
}

