package broker

import (
	"bufio"
	"encoding/json"
	"errors"
	"log"
	"net"
	"strings"
	"sync"
	"syscall"

	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
)

// Session represents a client connection
type Session struct {
	name            string
	conn            net.Conn
	enc             *json.Encoder
	scan            *bufio.Scanner
	broker          *Broker
	cleanDisconnect bool // Set to true when disconnect command is received
	cleanupOnce     sync.Once
	writeMu         sync.Mutex // protects enc writes
}

// newSession creates a new session
func newSession(conn net.Conn, broker *Broker) *Session {
	scan := bufio.NewScanner(conn)
	// Match client buffer size for large AI agent payloads.
	// Uses config.Defaults.MaxMessageSize (single source of truth) to avoid asymmetry.
	bufSize := int(config.Defaults.MaxMessageSize)
	scan.Buffer(make([]byte, bufSize), bufSize)
	return &Session{
		conn:   conn,
		enc:    json.NewEncoder(conn),
		scan:   scan,
		broker: broker,
	}
}

// readLoop reads requests and sends responses
func (s *Session) readLoop() {
	defer s.cleanup()

	for s.scan.Scan() {
		var req protocol.Request
		if err := json.Unmarshal(s.scan.Bytes(), &req); err != nil {
			resp := protocol.ErrResponse(protocol.ErrInvalidRequest, "invalid JSON")
			s.writeMu.Lock()
			s.enc.Encode(resp)
			s.writeMu.Unlock()
			continue
		}

		resp := route(s, req)
		s.writeMu.Lock()
		err := s.enc.Encode(resp)
		s.writeMu.Unlock()
		if err != nil {
			// Suppress errors after disconnect — client may have already closed
			if !s.cleanDisconnect {
				log.Printf("session %s: error encoding response: %v", s.name, err)
			}
			return
		}

		// After clean disconnect, stop reading — client is closing
		if s.cleanDisconnect {
			return
		}
	}

	if err := s.scan.Err(); err != nil {
		// Suppress expected EOF/closed connection errors
		if !s.cleanDisconnect && !isConnectionClosed(err) {
			log.Printf("session %s: scan error: %v", s.name, err)
		}
	}
}

// isConnectionClosed checks if an error is due to a closed connection (expected on disconnect).
// Uses typed error matching instead of fragile string comparison.
func isConnectionClosed(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE)
}

// cleanup releases resources on disconnect. Safe to call multiple times —
// handleDisconnect calls it eagerly, and readLoop defers it as a safety net.
func (s *Session) cleanup() {
	s.cleanupOnce.Do(s.doCleanup)
}

func (s *Session) doCleanup() {
	if s.name != "" {
		// Release all locks
		s.broker.lockMgr.ReleaseAll(s.name)

		// Re-queue tasks claimed by this session
		// Only requeue on unclean disconnect (connection dropped without disconnect command)
		// Clean disconnect means the client intentionally disconnected and wants to keep tasks claimed
		if !s.cleanDisconnect {
			count, err := s.broker.store.RequeueByOwner(s.name)
			if err != nil {
				log.Printf("session: error requeuing tasks for %s: %v", s.name, err)
			} else if count > 0 {
				log.Printf("session: requeued %d tasks for %s", count, s.name)
			}
		}

		// Unsubscribe from all events
		s.broker.hub.UnsubscribeAll(s.name)

		// Remove from broker session map
		// CLASS 4 FIX (E2): Only delete if we still own this name in the sessions map
		// Prevents old session cleanup from deleting new session's entry after name collision
		var shouldPublish bool
		s.broker.mu.Lock()
		if s.broker.sessions[s.name] == s {
			delete(s.broker.sessions, s.name)
			if !strings.HasSuffix(s.name, "-push") {
				delete(s.broker.pushTokens, s.name)
			}
			shouldPublish = true
		}
		s.broker.mu.Unlock()

		// Publish presence event OUTSIDE the lock to avoid deadlock
		// If any event subscriber tries to read broker.sessions, publishing inside
		// the lock would cause: cleanup holds broker.mu (write) → hub.Publish →
		// subscriber reads broker.sessions → needs broker.mu (read) → deadlock
		if shouldPublish {
			publishPresenceEvent(s.broker, "presence.offline", s.name)
		}
	}

	s.conn.Close()
}
