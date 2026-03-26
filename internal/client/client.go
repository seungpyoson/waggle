package client

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
)

// Client is a connection to the waggle broker.
type Client struct {
	conn    net.Conn
	scanner *bufio.Scanner
}

// Connect establishes a connection to the broker socket.
func Connect(socketPath string) (*Client, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("connect to broker: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	// Use configurable buffer size for large payloads (default 1MB, vs 64KB default)
	bufSize := int(config.Defaults.MaxMessageSize)
	scanner.Buffer(make([]byte, bufSize), bufSize)

	return &Client{
		conn:    conn,
		scanner: scanner,
	}, nil
}

// Send sends a request and reads one response.
func (c *Client) Send(req protocol.Request) (*protocol.Response, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')

	if _, err := c.conn.Write(data); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	if !c.scanner.Scan() {
		if err := c.scanner.Err(); err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		return nil, fmt.Errorf("broker closed connection")
	}

	var resp protocol.Response
	if err := json.Unmarshal(c.scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &resp, nil
}

// Receive reads one response from the connection.
func (c *Client) Receive() (protocol.Response, error) {
	if !c.scanner.Scan() {
		if err := c.scanner.Err(); err != nil {
			return protocol.Response{}, fmt.Errorf("read response: %w", err)
		}
		return protocol.Response{}, fmt.Errorf("broker closed connection")
	}

	var resp protocol.Response
	if err := json.Unmarshal(c.scanner.Bytes(), &resp); err != nil {
		return protocol.Response{}, fmt.Errorf("parse response: %w", err)
	}
	return resp, nil
}

// ReadStream returns a channel that streams events from the broker.
// Used for subscribe connections.
func (c *Client) ReadStream() (<-chan protocol.Event, error) {
	eventChan := make(chan protocol.Event)

	go func() {
		defer close(eventChan)
		for c.scanner.Scan() {
			var event protocol.Event
			if err := json.Unmarshal(c.scanner.Bytes(), &event); err != nil {
				// Log error but continue reading
				continue
			}
			eventChan <- event
		}
	}()

	return eventChan, nil
}

// PushedMessage represents a message pushed from the broker.
type PushedMessage struct {
	ID     int64  `json:"id"`
	From   string `json:"from"`
	Body   string `json:"body"`
	SentAt string `json:"sent_at"`
}

// ReadMessages returns a channel that receives pushed messages from the broker.
// Filters out non-message responses (connect responses, etc).
//
// IMPORTANT: This method takes exclusive ownership of the connection's read stream.
// After calling ReadMessages, do NOT call Send, Receive, or ReadStream on the same client.
// The goroutine exits when the connection is closed.
func (c *Client) ReadMessages() (<-chan PushedMessage, error) {
	ch := make(chan PushedMessage, 64) // buffered to prevent goroutine leak if reader is slow
	go func() {
		defer close(ch)
		for c.scanner.Scan() {
			var resp protocol.Response
			if err := json.Unmarshal(c.scanner.Bytes(), &resp); err != nil {
				continue
			}
			if !resp.OK || len(resp.Data) == 0 {
				continue
			}
			var msg struct {
				Type   string `json:"type"`
				ID     int64  `json:"id"`
				From   string `json:"from"`
				Body   string `json:"body"`
				SentAt string `json:"sent_at"`
			}
			if err := json.Unmarshal(resp.Data, &msg); err != nil || msg.Type != "message" {
				continue
			}
			ch <- PushedMessage{
				ID:     msg.ID,
				From:   msg.From,
				Body:   msg.Body,
				SentAt: msg.SentAt,
			}
		}
	}()
	return ch, nil
}

// SetDeadline sets a deadline on the underlying connection for all future I/O.
// Returns error if the deadline cannot be set (e.g., connection already broken).
func (c *Client) SetDeadline(timeout time.Duration) error {
	return c.conn.SetDeadline(time.Now().Add(timeout))
}

// Close closes the connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

