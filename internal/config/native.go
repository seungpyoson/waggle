package config

import (
	"fmt"
	"path/filepath"
	"time"
)

// The HTTP address is framing for an exclusively Unix-socket connection.
// It is never resolved or used as a TCP destination.
const AppServerWebSocketURL = "ws://localhost/"
const AppServerClientName = "waggle"
const AppServerActiveTurnPageSize = 1

// ProviderConfig is explicit installation input. An unregistered endpoint
// cannot be supplied by an enrollment request to enable a provider.
type ProviderConfig struct {
	CodexEndpoint string
	ClientVersion string
	Transport     NativeConfig
}

func (c ProviderConfig) Validate() error {
	if c.ClientVersion == "" {
		return fmt.Errorf("native connections require the broker client version")
	}
	if c.CodexEndpoint != "" && !filepath.IsAbs(c.CodexEndpoint) {
		return fmt.Errorf("registered Codex App Server endpoint must be absolute")
	}
	return c.Transport.Validate()
}

type NativeConfig struct {
	RequestTimeout     time.Duration
	MaxFrameBytes      int64
	MaxPendingRequests int
}

func NewNativeConfig() NativeConfig {
	return NativeConfig{RequestTimeout: Defaults.ConnectTimeout, MaxFrameBytes: Defaults.MaxMessageSize, MaxPendingRequests: 32}
}

func (c NativeConfig) Validate() error {
	if c.RequestTimeout <= 0 || c.MaxFrameBytes <= 0 || c.MaxPendingRequests <= 0 {
		return fmt.Errorf("native transport requires positive request deadline, frame and concurrency limits")
	}
	return nil
}
