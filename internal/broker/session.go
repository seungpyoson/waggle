package broker

import (
	"bufio"
	"encoding/json"
	"log"
	"net"
	"strings"

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
}

// newSession creates a new session
func newSession(conn net.Conn, broker *Broker) *Session {
	return &Session{
		conn:   conn,
		enc:    json.NewEncoder(conn),
		scan:   bufio.NewScanner(conn),
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
			s.enc.Encode(resp)
			continue
		}

		resp := route(s, req)
		if err := s.enc.Encode(resp); err != nil {
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

// isConnectionClosed checks if an error is due to a closed connection (expected on disconnect)
func isConnectionClosed(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "broken pipe")
}

// cleanup releases resources on disconnect
func (s *Session) cleanup() {
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
		s.broker.mu.Lock()
		delete(s.broker.sessions, s.name)
		s.broker.mu.Unlock()
	}

	s.conn.Close()
}

