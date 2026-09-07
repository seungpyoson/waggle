package broker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/seungpyoson/waggle/internal/tasks"
)

// Session represents a client connection
type Session struct {
	name            string
	conn            net.Conn
	enc             *json.Encoder
	scan            *bufio.Scanner
	broker          *Broker
	cleanDisconnect atomic.Bool // Set to true when disconnect command is received
	streams         sync.WaitGroup
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

// readLoop owns connection cleanup within the admitted service lifetime.
// Each new RPC still requires its own admission before it can execute.
func (s *Session) readLoop(lifetime *brokerstate.Operation) {
	defer s.cleanup(lifetime)

	for s.scan.Scan() {
		req, err := protocol.DecodeRequest(s.scan.Bytes())
		if err != nil {
			resp := protocol.ErrResponse(protocol.ErrInvalidRequest, "invalid JSON")
			if errors.Is(err, protocol.ErrUnsupportedVersion) {
				resp = protocol.ErrResponse("UNSUPPORTED_VERSION", err.Error())
			}
			s.writeMu.Lock()
			s.enc.Encode(resp)
			s.writeMu.Unlock()
			continue
		}

		var resp protocol.Response
		err = s.broker.owner.Do(context.Background(), func(op *brokerstate.Operation) error {
			resp = route(&call{Session: s, op: op}, req)
			return nil
		})
		if err != nil {
			resp = protocol.ErrResponse(protocol.ErrInternalError, err.Error())
		}
		s.writeMu.Lock()
		err = s.enc.Encode(resp)
		s.writeMu.Unlock()
		if err != nil {
			// Suppress errors after disconnect — client may have already closed
			if !s.cleanDisconnect.Load() {
				log.Printf("session %s: error encoding response: %v", s.name, err)
			}
			return
		}

		// After clean disconnect, stop reading — client is closing
		if s.cleanDisconnect.Load() {
			return
		}
	}

	if err := s.scan.Err(); err != nil {
		// Suppress expected EOF/closed connection errors
		if !s.cleanDisconnect.Load() && !isConnectionClosed(err) {
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

// cleanup runs once, when this connection's read loop returns. Its writes
// finish under the existing admission, before the service can drain.
func (s *Session) cleanup(lifetime *brokerstate.Operation) {
	if s.name != "" {
		// Release all locks
		s.broker.lockMgr.ReleaseAll(s.name)

		// Re-queue tasks claimed by this session
		// Only requeue on unclean disconnect (connection dropped without disconnect command)
		// Clean disconnect means the client intentionally disconnected and wants to keep tasks claimed
		if !s.cleanDisconnect.Load() {
			var count int
			err := lifetime.Write(context.Background(), func(tx *brokerstate.WriteTx) (err error) {
				count, err = tasks.NewStore(tx).RequeueByOwner(s.name)
				return err
			})
			if err != nil {
				log.Printf("session: error requeuing tasks for %s: %v", s.name, err)
			} else if count > 0 {
				log.Printf("session: requeued %d tasks for %s", count, s.name)
			}
		}

		// Unsubscribe from all events
		s.broker.hub.UnsubscribeAll(s.name)

		// This read loop is the sole owner of its connection registration.
		s.broker.mu.Lock()
		delete(s.broker.sessions, s.name)
		s.broker.mu.Unlock()

	}

	s.conn.Close()
	s.streams.Wait()
}
