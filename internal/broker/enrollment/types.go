// Package enrollment implements owner-registered workers that own a native
// transport for exactly one reserved enrollment. It exposes the worker start
// entrypoint and observation types; it never returns a callable transport.
package enrollment

import (
	"github.com/seungpyoson/waggle/internal/broker/enrollment/internal/driver"
	"github.com/seungpyoson/waggle/internal/broker/enrollment/internal/native"
	"github.com/seungpyoson/waggle/internal/config"
)

type (
	Connector    = driver.Connector
	Driver       = driver.Driver
	Readiness    = driver.Readiness
	Outcome      = driver.Outcome
	Availability = driver.Availability
	Possession   = driver.Possession
)

const (
	Unavailable = driver.Unavailable
	Idle        = driver.Idle
	Busy        = driver.Busy
)

const (
	NotSubmitted = driver.NotSubmitted
	Accepted     = driver.Accepted
	Held         = driver.Held
	Refused      = driver.Refused
	Uncertain    = driver.Uncertain
)

// NewConnector selects the configured production provider mechanics.
func NewConnector(cfg config.ProviderConfig) (Connector, error) {
	return native.New(cfg)
}
