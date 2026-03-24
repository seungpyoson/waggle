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

		// Re-queue all claimed tasks
		// Note: This re-queues ALL claimed tasks, not just this session's.
		// This is intentional per the spec requirement.
		count, err := s.broker.store.RequeueAllClaimed()
		if err != nil {
			log.Printf("session: error requeuing tasks: %v", err)
		} else if count > 0 {
			log.Printf("session: requeued %d tasks on disconnect", count)
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

