package config

import (
	"testing"
	"time"
)

func TestMessagingSchedulingDefaultsAndValidation(t *testing.T) {
	cfg := NewMessagingConfig()
	if cfg.QueueCheckInterval != 50*time.Millisecond || cfg.IdleCheckInterval != 2*time.Second || cfg.ProbeInterval != 5*time.Second {
		t.Errorf("scheduling defaults: floor=%v idle=%v probe=%v", cfg.QueueCheckInterval, cfg.IdleCheckInterval, cfg.ProbeInterval)
	}
	for _, field := range []string{"floor", "idle", "probe", "floor exceeds idle"} {
		t.Run(field, func(t *testing.T) {
			c := NewMessagingConfig()
			switch field {
			case "floor":
				c.QueueCheckInterval = 0
			case "idle":
				c.IdleCheckInterval = 0
			case "probe":
				c.ProbeInterval = 0
			case "floor exceeds idle":
				c.QueueCheckInterval = c.IdleCheckInterval + time.Millisecond
			}
			if err := c.Validate(); err == nil {
				t.Fatal("invalid scheduling interval accepted")
			}
		})
	}
	cfg.QueueCheckInterval = cfg.IdleCheckInterval
	if err := cfg.Validate(); err != nil {
		t.Fatalf("equal positive floor and idle rejected: %v", err)
	}
}
