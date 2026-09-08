package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"

	"github.com/seungpyoson/waggle/internal/broker/enrollment/internal/driver"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/messages"
)

// thread is a native subscription bound to one canonical recipient incarnation.
// Only Attach constructs it, after the server confirms the exact loaded thread.
// It holds no canonical readiness or receipt state.
type thread struct {
	connection *connection
	target     messages.Enrollment
}

// Attach subscribes without loading history or starting a turn. A failed
// subscription closes and joins its transport; there is no resume alternative.
func Attach(ctx context.Context, target messages.Enrollment, version string, cfg config.NativeConfig, dial func(context.Context, string, string) (net.Conn, error), observe func(context.Context, Event) error) (driver.Driver, error) {
	if target.ID == "" || target.Provider != "codex" || target.Conversation == "" {
		return nil, fmt.Errorf("Codex attachment requires an enrolled incarnation and exact thread")
	}
	c, err := openConnection(ctx, target.Endpoint, version, cfg, dial, observe)
	if err != nil {
		return nil, err
	}
	result, _, err := c.call(ctx, "thread/subscribe", struct {
		ThreadID string `json:"threadId"`
	}{target.Conversation})
	if err == nil {
		var response struct {
			ThreadID string `json:"threadId"`
		}
		if json.Unmarshal(result, &response) != nil || response.ThreadID != target.Conversation {
			err = fmt.Errorf("App Server subscription did not confirm the enrolled thread")
		}
	}
	if err != nil {
		if closeErr := c.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("%w: %w", driver.ErrCloseFailed, closeErr))
		}
		return nil, err
	}
	return &thread{connection: c, target: target}, nil
}

// Close releases this client connection and joins its reader and observer.
// It does not terminate the provider thread or withdraw retained native input.
func (t *thread) Close() error { return t.connection.Close() }
