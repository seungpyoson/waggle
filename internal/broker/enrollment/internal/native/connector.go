// Package native selects configured provider mechanics. Canonical enrollment,
// readiness, ordering and receipts belong exclusively to the broker.
package native

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/seungpyoson/waggle/internal/broker/enrollment/internal/driver"
	"github.com/seungpyoson/waggle/internal/broker/enrollment/internal/driver/codex"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/messages"
)

type Connector struct{ config config.ProviderConfig }

func New(cfg config.ProviderConfig) (*Connector, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Connector{config: cfg}, nil
}

func (c *Connector) Check(e messages.Enrollment) error {
	if e.Provider != "codex" {
		return fmt.Errorf("native provider is unsupported in this build")
	}
	if c.config.CodexEndpoint == "" {
		return fmt.Errorf("Codex App Server endpoint is not registered; configure waggle start --codex-app-server")
	}
	if e.Endpoint != c.config.CodexEndpoint || e.Conversation == "" {
		return fmt.Errorf("Codex enrollment requires the registered endpoint and exact attached thread")
	}
	return nil
}

func (c *Connector) Open(ctx context.Context, e messages.Enrollment) (driver.Driver, error) {
	if err := c.Check(e); err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: c.config.Transport.RequestTimeout}
	connect := func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dialer.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		peer, ok := conn.(*net.UnixConn)
		if !ok {
			return nil, closeFailedPeer(conn, driver.ErrPeerIdentity)
		}
		if err := verifyPeer(peer); err != nil {
			return nil, closeFailedPeer(conn, errors.Join(driver.ErrPeerIdentity, err))
		}
		return conn, nil
	}
	return codex.Attach(ctx, e, c.config.ClientVersion, c.config.Transport, connect, func(ctx context.Context, event codex.Event) error {
		// Waggle cannot approve native requests or turn notifications into
		// receipts. Multi-client approval delivery remains an M1 conformance
		// obligation; this connection sends no approval response.
		return ctx.Err()
	})
}

func closeFailedPeer(conn net.Conn, err error) error {
	if closeErr := conn.Close(); closeErr != nil {
		return errors.Join(err, fmt.Errorf("%w: %w", driver.ErrCloseFailed, closeErr))
	}
	return err
}
