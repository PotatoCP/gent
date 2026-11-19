package gent

import (
	"errors"
	"time"

	"github.com/rickchristie/gent/internal/buffer"
)

// DeliveryConfig bounds one live consumer independently of checkpoint persistence.
// Zero fields use defaults. Negative fields are invalid; unbounded delivery is not supported.
type DeliveryConfig struct {
	// MaxEvents includes the event currently waiting for the consumer. The default is 4096.
	MaxEvents int
	// MaxBytes bounds encoded queued data, including the current event. The default is 8 MiB.
	// An event larger than this limit closes this subscription without stopping execution.
	MaxBytes int
	// Timeout limits each wait for the consumer, including after execution ends. Default: 30s.
	// Apply a separate write deadline in the socket worker after receiving an event.
	Timeout time.Duration
}

var (
	// ErrDeliveryOverflow means a consumer exceeded its event or byte queue limit.
	ErrDeliveryOverflow = buffer.ErrDeliveryOverflow
	// ErrDeliveryTimeout means a consumer did not receive an event within its delivery timeout.
	ErrDeliveryTimeout = buffer.ErrDeliveryTimeout
)

func deliveryConfig(cfg DeliveryConfig) (DeliveryConfig, error) {
	if cfg.MaxEvents < 0 || cfg.MaxBytes < 0 || cfg.Timeout < 0 {
		return cfg, errors.New("gent: delivery limits must not be negative")
	}
	if cfg.MaxEvents == 0 {
		cfg.MaxEvents = 4096
	}
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = 8 * 1024 * 1024
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	return cfg, nil
}
