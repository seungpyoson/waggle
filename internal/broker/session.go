package broker

import (
	"bufio"
	"encoding/json"
	"log"
	"net"

	"github.com/seungpyoson/waggle/internal/protocol"
)

// Session represents a client connection
type Session struct {
	name   string
	conn   net.Conn
	enc    *json.Encoder
	scan   *bufio.Scanner
	broker *Broker
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
			log.Printf("session: error encoding response: %v", err)
			return
		}
	}

	if err := s.scan.Err(); err != nil {
		log.Printf("session: scan error: %v", err)
	}
}

// cleanup releases resources on disconnect
func (s *Session) cleanup() {
	if s.name != "" {
		// Release all locks
		s.broker.lockMgr.ReleaseAll(s.name)

		// Re-queue tasks claimed by this session
		// Skip requeue for CLI sessions - they are short-lived and don't need cleanup
		// CLI sessions use the name "cli" and are expected to complete tasks in separate invocations
		if s.name != "cli" {
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

