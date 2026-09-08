// Package codex implements the selected App Server protocol over Unix
// WebSocket connections. It owns connection mechanics, never canonical state.
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/coder/websocket"
	"github.com/seungpyoson/waggle/internal/broker/enrollment/internal/driver"
	"github.com/seungpyoson/waggle/internal/config"
)

var ErrProtocol = errors.New("invalid App Server framing or request correlation")
var ErrClosed = errors.New("App Server connection closed")
var ErrCapacity = errors.New("App Server request capacity reached")
var ErrTransport = errors.New("App Server transport failed")

// Event includes server-initiated requests as well as notifications. Receiving
// one grants no authority to approve it. This client has no approval-reply API.
type Event struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

type wireMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Native errors are classified by code without copying provider text, which
// may quote input or credentials, into broker diagnostics.
func (e *rpcError) Error() string { return fmt.Sprintf("App Server request failed (code %d)", e.Code) }

type response struct {
	result json.RawMessage
	err    error
}

type connection struct {
	ws         *websocket.Conn
	endpoint   string
	config     config.NativeConfig
	mu         sync.Mutex
	sequence   uint64
	pending    map[string]chan response
	failure    error
	done       chan struct{}
	closeOnce  sync.Once
	closeErr   error
	cancelRead context.CancelFunc
}

// openConnection is the transport initialization used by Attach. Production supplies net.Dialer.DialContext;
// deterministic tests supply a byte-stream peer to this same Unix dial path.
// Observe must finish within its context and must not issue blocking RPC calls
// on this connection. Its execution is joined before Close returns.
func openConnection(ctx context.Context, path, version string, cfg config.NativeConfig, dial func(context.Context, string, string) (net.Conn, error), observe func(context.Context, Event) error) (*connection, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(path) || version == "" || dial == nil || observe == nil {
		return nil, fmt.Errorf("App Server requires an absolute registered endpoint, client version, dialer and event observer")
	}
	transport := &http.Transport{
		DialContext:       func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx, "unix", path) },
		DisableKeepAlives: true, ResponseHeaderTimeout: cfg.RequestTimeout, MaxResponseHeaderBytes: cfg.MaxFrameBytes,
	}
	httpClient := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return fmt.Errorf("App Server endpoint redirects are unsupported")
	}}
	connectCtx, cancel := context.WithTimeout(ctx, cfg.RequestTimeout)
	defer cancel()
	ws, _, err := websocket.Dial(connectCtx, config.AppServerWebSocketURL, &websocket.DialOptions{HTTPClient: httpClient})
	if err != nil {
		transport.CloseIdleConnections()
		failure := errors.Join(ErrTransport, connectCtx.Err())
		if errors.Is(err, driver.ErrPeerIdentity) {
			failure = driver.ErrPeerIdentity
		}
		if errors.Is(err, driver.ErrCloseFailed) {
			failure = errors.Join(failure, driver.ErrCloseFailed)
		}
		return nil, fmt.Errorf("connect registered App Server: %w", failure)
	}
	ws.SetReadLimit(cfg.MaxFrameBytes)
	readCtx, cancelRead := context.WithCancel(ctx)
	c := &connection{ws: ws, endpoint: path, config: cfg, pending: make(map[string]chan response), done: make(chan struct{}), cancelRead: cancelRead}
	go func() {
		defer close(c.done)
		err := c.read(readCtx, observe)
		c.fail(err)
		c.closeSocket()
	}()
	params := struct {
		ClientInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"clientInfo"`
		Capabilities struct {
			Experimental bool `json:"experimentalApi"`
		} `json:"capabilities"`
	}{}
	params.ClientInfo.Name, params.ClientInfo.Version = config.AppServerClientName, version
	params.Capabilities.Experimental = true
	result, _, err := c.call(ctx, "initialize", params)
	if err == nil {
		var initialized struct {
			Home      string `json:"codexHome"`
			Family    string `json:"platformFamily"`
			OS        string `json:"platformOs"`
			UserAgent string `json:"userAgent"`
		}
		if json.Unmarshal(result, &initialized) != nil || !filepath.IsAbs(initialized.Home) || initialized.Family != "unix" || initialized.OS == "" || initialized.UserAgent == "" {
			err = fmt.Errorf("App Server initialization lacks required native Unix identity fields")
		}
	}
	if err == nil {
		err = c.notify(ctx, "initialized")
	}
	if err != nil {
		if closeErr := c.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("%w: %w", driver.ErrCloseFailed, closeErr))
		}
		return nil, fmt.Errorf("initialize App Server: %w", err)
	}
	return c, nil
}

func (c *connection) notify(ctx context.Context, method string) error {
	data, err := json.Marshal(wireMessage{Method: method})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, c.config.RequestTimeout)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageText, data); err != nil {
		return fmt.Errorf("write App Server notification: %w", errors.Join(ErrTransport, ctx.Err()))
	}
	return nil
}

// call reports whether a WebSocket write was attempted. Any subsequent error
// leaves native possession uncertain. Cancellation closes the connection and
// invalidates all pending requests; it never withdraws or resends native input.
func (c *connection) call(ctx context.Context, method string, params any) (json.RawMessage, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, c.config.RequestTimeout)
	defer cancel()
	p, err := json.Marshal(params)
	if err != nil {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	c.mu.Lock()
	if c.failure != nil {
		err := c.failure
		c.mu.Unlock()
		return nil, false, err
	}
	if len(c.pending) >= c.config.MaxPendingRequests {
		c.mu.Unlock()
		return nil, false, ErrCapacity
	}
	if c.sequence == ^uint64(0) {
		c.mu.Unlock()
		return nil, false, fmt.Errorf("App Server request identifier exhausted")
	}
	c.sequence++
	id := strconv.FormatUint(c.sequence, 10)
	idJSON, _ := json.Marshal(id)
	data, err := json.Marshal(wireMessage{ID: idJSON, Method: method, Params: p})
	if err != nil {
		c.mu.Unlock()
		return nil, false, err
	}
	if int64(len(data)) > c.config.MaxFrameBytes {
		c.mu.Unlock()
		return nil, false, fmt.Errorf("App Server request exceeds frame limit")
	}
	result := make(chan response, 1)
	c.pending[id] = result
	c.mu.Unlock()
	if err := c.ws.Write(ctx, websocket.MessageText, data); err != nil {
		err = fmt.Errorf("write App Server request: %w", errors.Join(ErrTransport, ctx.Err()))
		c.fail(err)
		c.closeSocket()
		return nil, true, err
	}
	select {
	case r := <-result:
		return r.result, true, r.err
	case <-ctx.Done():
		c.fail(ctx.Err())
		c.closeSocket()
		return nil, true, ctx.Err()
	}
}

func (c *connection) read(ctx context.Context, observe func(context.Context, Event) error) error {
	for {
		kind, data, err := c.ws.Read(ctx)
		if err != nil {
			return fmt.Errorf("read App Server: %w", errors.Join(ErrTransport, ctx.Err()))
		}
		if kind != websocket.MessageText {
			return ErrProtocol
		}
		var msg wireMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return ErrProtocol
		}
		if msg.Method != "" {
			if msg.Result != nil || msg.Error != nil {
				return ErrProtocol
			}
			if err := observe(ctx, Event{ID: msg.ID, Method: msg.Method, Params: msg.Params}); err != nil {
				return fmt.Errorf("App Server event observer: %w", err)
			}
			continue
		}
		var id string
		if json.Unmarshal(msg.ID, &id) != nil || id == "" || (msg.Result == nil) == (msg.Error == nil) {
			return ErrProtocol
		}
		c.mu.Lock()
		pending, ok := c.pending[id]
		if !ok {
			c.mu.Unlock()
			return ErrProtocol
		}
		delete(c.pending, id)
		result := response{result: msg.Result}
		if msg.Error != nil {
			result.err = msg.Error
		}
		pending <- result
		c.mu.Unlock()
	}
}

func (c *connection) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failure != nil {
		return
	}
	c.failure = err
	for id, result := range c.pending {
		result <- response{err: err}
		delete(c.pending, id)
	}
}

func (c *connection) closeSocket() error {
	c.closeOnce.Do(func() { c.cancelRead(); c.closeErr = c.ws.CloseNow() })
	return c.closeErr
}

// Close terminates only this transport and joins its reader and observer.
// It sends no native thread termination or turn interruption command.
func (c *connection) Close() error {
	c.fail(ErrClosed)
	err := c.closeSocket()
	<-c.done
	// The WebSocket reader may already have closed the transport. Its work
	// has joined here, so the documented already-closed result is complete.
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
