package config

import (
	"fmt"
	"path/filepath"
	"time"
)

// NativeSchemaVersion is shared by canonical storage and offline conversion.
const NativeSchemaVersion = 2

const CanonicalJournalMode = "wal"
const CanonicalConnections = 1

const ProtocolVersion = 2

// StartupPipeFD is the child descriptor assigned by the daemon launcher.
const StartupPipeFD = 3

// StoreAction is explicit: opening a missing store never initializes one.
type StoreAction uint8

const (
	OpenStore StoreAction = iota + 1
	CreateStore
)

// OwnershipConfig contains the inputs to acquisition, not inferred recovery modes.
type OwnershipConfig struct {
	Database           string
	Action             StoreAction
	AcquireTimeout     time.Duration
	TransactionTimeout time.Duration
}

// BrokerEndpoints names the IPC files belonging to a canonical store.
type BrokerEndpoints struct {
	Socket string
	PID    string
}

type BrokerConfig struct {
	Messaging          MessagingConfig
	Endpoints          BrokerEndpoints
	LeaseCheckPeriod   time.Duration
	TaskTTLCheckPeriod time.Duration
	TaskStaleThreshold time.Duration
}

func NewBrokerConfig(endpoints BrokerEndpoints) BrokerConfig {
	return BrokerConfig{Endpoints: endpoints, Messaging: NewMessagingConfig(),
		LeaseCheckPeriod:   Defaults.LeaseCheckPeriod,
		TaskTTLCheckPeriod: Defaults.TaskTTLCheckPeriod,
		TaskStaleThreshold: Defaults.TaskStaleThreshold,
	}
}

func (c BrokerConfig) Validate() error {
	if err := c.Messaging.Validate(); err != nil {
		return err
	}
	if err := c.Endpoints.Validate(); err != nil {
		return err
	}
	if c.LeaseCheckPeriod <= 0 || c.TaskTTLCheckPeriod <= 0 || c.TaskStaleThreshold <= 0 {
		return fmt.Errorf("broker periods and deadlines must be positive")
	}
	return nil
}

func (p BrokerEndpoints) Validate() error {
	if !filepath.IsAbs(p.Socket) || !filepath.IsAbs(p.PID) || p.Socket == p.PID {
		return fmt.Errorf("broker socket and PID require distinct absolute paths")
	}
	return nil
}

func NewOwnershipConfig(database string, action StoreAction) OwnershipConfig {
	return OwnershipConfig{
		Database:           database,
		Action:             action,
		AcquireTimeout:     Defaults.StartupTimeout,
		TransactionTimeout: Defaults.BusyTimeout,
	}
}

func (c OwnershipConfig) Validate() error {
	if !filepath.IsAbs(c.Database) {
		return fmt.Errorf("canonical database requires an absolute path")
	}
	if c.Action != OpenStore && c.Action != CreateStore {
		return fmt.Errorf("canonical store action must explicitly be open or create")
	}
	if c.AcquireTimeout <= 0 || c.TransactionTimeout <= 0 {
		return fmt.Errorf("ownership deadlines must be positive")
	}
	return nil
}
