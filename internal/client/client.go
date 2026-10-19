package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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

// Query performs one bounded anonymous request. A rejected response is an
// error; reaching the socket alone is never a successful observation.
func Query(ctx context.Context, socket string, timeout time.Duration, req protocol.Request) (data json.RawMessage, err error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, fmt.Errorf("connect to broker: %w", err)
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer close(interrupted); conn.SetDeadline(time.Now()) })
	defer func() {
		if !stop() {
			<-interrupted
		}
	}()
	return query(newClient(conn), req)
}

func query(c *Client, req protocol.Request) (json.RawMessage, error) {
	resp, err := c.Send(req)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("broker rejected %s: %s: %s", req.Cmd, resp.Code, resp.Error)
	}
	if len(resp.Data) == 0 || string(resp.Data) == "null" {
		return nil, fmt.Errorf("broker returned no %s observation", req.Cmd)
	}
	return resp.Data, nil
}

// Connect establishes a connection to the broker socket with a timeout.
func Connect(socketPath string, timeout time.Duration) (*Client, error) {
	conn, err := net.DialTimeout("unix", socketPath, timeout)
	if err != nil {
		return nil, fmt.Errorf("connect to broker: %w", err)
	}

	return newClient(conn), nil
}

func newClient(conn net.Conn) *Client {
	scanner := bufio.NewScanner(conn)
	// Use configurable buffer size for large payloads (default 1MB, vs 64KB default)
	bufSize := int(config.Defaults.MaxMessageSize)
	scanner.Buffer(make([]byte, bufSize), bufSize)

	return &Client{conn: conn, scanner: scanner}
}

// Send sends a request and reads one response.
func (c *Client) Send(req protocol.Request) (*protocol.Response, error) {
	req.Version = config.ProtocolVersion
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

// SetDeadline sets a deadline on the underlying connection for all future I/O.
// Returns error if the deadline cannot be set (e.g., connection already broken).
func (c *Client) SetDeadline(timeout time.Duration) error {
	return c.conn.SetDeadline(time.Now().Add(timeout))
}

// ClearDeadline removes any deadline set on the underlying connection.
// Used after handshake completes for streaming commands that must not time out.
func (c *Client) ClearDeadline() error {
	return c.conn.SetDeadline(time.Time{})
}

// Close closes the connection.
func (c *Client) Close() error {
	return c.conn.Close()
}
