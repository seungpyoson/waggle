package config

import (
	"fmt"
	"time"
)

const IncarnationCredentialEnv = "WAGGLE_INCARNATION_CREDENTIAL"

const ClaudeMessagingSocketEnv = "CLAUDE_CODE_MESSAGING_SOCKET"
const ClaudeEnvironmentFileEnv = "CLAUDE_ENV_FILE"

// MessagingConfig bounds the canonical queue and its inputs. Provider drivers
// do not choose queue limits, message deadlines, or conversation budgets.
type MessagingConfig struct {
	PendingTTL         time.Duration
	DefaultDeadline    time.Duration
	MaxDeadline        time.Duration
	ScanPeriod         time.Duration
	EnrollmentCapacity int
	QueueCapacity      int
	ScanLimit          int
	DefaultHops        int
	MaxHops            int
	MaxBodyBytes       int
	MaxEvidenceBytes   int
	MaxFieldBytes      int
}

func NewMessagingConfig() MessagingConfig {
	return MessagingConfig{
		PendingTTL: time.Minute, DefaultDeadline: 5 * time.Minute,
		MaxDeadline: time.Hour, ScanPeriod: time.Second,
		EnrollmentCapacity: 256, QueueCapacity: 100, ScanLimit: 100, DefaultHops: 8, MaxHops: 32,
		MaxBodyBytes: 64 * 1024, MaxEvidenceBytes: 16 * 1024,
		MaxFieldBytes: Defaults.MaxFieldLength,
	}
}

func (c MessagingConfig) Validate() error {
	if c.PendingTTL <= 0 || c.DefaultDeadline <= 0 || c.MaxDeadline < c.DefaultDeadline || c.ScanPeriod <= 0 {
		return fmt.Errorf("messaging requires positive periods and a bounded default deadline")
	}
	if c.EnrollmentCapacity <= 0 || c.QueueCapacity <= 0 || c.ScanLimit <= 0 || c.DefaultHops <= 0 || c.MaxHops < c.DefaultHops || c.MaxBodyBytes <= 0 || c.MaxEvidenceBytes <= 0 || c.MaxFieldBytes <= 0 {
		return fmt.Errorf("messaging limits must be positive and default hops must not exceed the maximum")
	}
	return nil
}
