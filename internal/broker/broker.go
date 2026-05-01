package broker

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/events"
	"github.com/seungpyoson/waggle/internal/locks"
	"github.com/seungpyoson/waggle/internal/messages"
	"github.com/seungpyoson/waggle/internal/spawn"
	"github.com/seungpyoson/waggle/internal/tasks"
)

// Config holds broker configuration
type Config struct {
	SocketPath         string
	DBPath             string
	LeaseCheckPeriod   time.Duration
	TTLCheckPeriod     time.Duration
	TaskTTLCheckPeriod time.Duration
	TaskStaleThreshold time.Duration
	IdleTimeout        time.Duration
}

// Broker is the main broker orchestrator
type Broker struct {
	config       Config
	hub          *events.Hub
	store        *tasks.Store
	msgStore     *messages.Store
	lockMgr      *locks.Manager
	spawnMgr     *spawn.Manager
	listener     net.Listener
	sessions     map[string]*Session
	pushTokens   map[string]string
	mu           sync.RWMutex
	stopCh       chan struct{}
	wg           sync.WaitGroup
	ackWaiters   map[int64]chan struct{}
	ackWaitersMu sync.Mutex
}

// New creates a new broker instance
func New(cfg Config) (*Broker, error) {
	if err := config.ValidateDefaults(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Apply defaults and validate all duration fields.
	// Pattern: default-then-validate pairs so no field can slip through.
	type durField struct {
		name string
		val  *time.Duration
		def  time.Duration
	}
	for _, f := range []durField{
		{"LeaseCheckPeriod", &cfg.LeaseCheckPeriod, config.Defaults.LeaseCheckPeriod},
		{"TTLCheckPeriod", &cfg.TTLCheckPeriod, config.Defaults.TTLCheckPeriod},
		{"TaskTTLCheckPeriod", &cfg.TaskTTLCheckPeriod, config.Defaults.TaskTTLCheckPeriod},
		{"TaskStaleThreshold", &cfg.TaskStaleThreshold, config.Defaults.TaskStaleThreshold},
		{"IdleTimeout", &cfg.IdleTimeout, config.Defaults.IdleTimeout},
	} {
		if *f.val == 0 {
			*f.val = f.def
		}
		if *f.val <= 0 {
			return nil, fmt.Errorf("broker.Config.%s must be positive, got %v", f.name, *f.val)
		}
	}

	// Open database
	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite serializes writers

	// Set pragmas
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("setting WAL mode: %w", err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA busy_timeout=%d", config.Defaults.BusyTimeout.Milliseconds())); err != nil {
		db.Close()
		return nil, fmt.Errorf("setting busy_timeout: %w", err)
	}

	// Open task store (shares DB connection)
	store, err := tasks.NewStore(db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("opening task store: %w", err)
	}

	// Open message store (shares DB connection)
	msgStore, err := messages.NewStore(db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("opening message store: %w", err)
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
		config:     cfg,
		hub:        events.NewHub(),
		store:      store,
		msgStore:   msgStore,
		lockMgr:    locks.NewManager(),
		spawnMgr:   spawn.NewManager(),
		listener:   listener,
		sessions:   make(map[string]*Session),
		pushTokens: make(map[string]string),
		stopCh:     make(chan struct{}),
		ackWaiters: make(map[int64]chan struct{}),
	}
	return b, nil
}

func (b *Broker) GetPushToken(agent string) string {
	if b == nil {
		return ""
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.pushTokens[agent]
}

func (b *Broker) GeneratePushToken(agent string) (string, error) {
	if b == nil {
		return "", fmt.Errorf("broker unavailable")
	}
	if agent == "" {
		return "", fmt.Errorf("agent required")
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if token := b.pushTokens[agent]; token != "" {
		return token, nil
	}

	token, err := newPushToken()
	if err != nil {
		return "", err
	}
	b.pushTokens[agent] = token
	return token, nil
}

func (b *Broker) DeletePushToken(agent string) {
	if b == nil || agent == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.pushTokens, agent)
}

func newPushToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate push token: %w", err)
	}
	return hex.EncodeToString(buf), nil
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

	// Start TTL checker
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		messages.StartTTLChecker(b.msgStore, b.config.TTLCheckPeriod, b.stopCh)
	}()

	// Start task TTL checker
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		tasks.StartTaskTTLChecker(b.store, b.hub, b.config.TaskTTLCheckPeriod, b.config.TaskStaleThreshold, b.stopCh)
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
	// Kill spawned agents before stopping
	if b.spawnMgr != nil {
		b.spawnMgr.StopAll()
	}

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
		// Best-effort cleanup: listener shutdown may already have removed the socket.
		os.Remove(b.config.SocketPath)
	}

	return nil
}

// monitorIdleTimeout monitors session count and shuts down broker after idle timeout
func (b *Broker) monitorIdleTimeout() {
	ticker := time.NewTicker(config.Defaults.IdleCheckInterval)
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
