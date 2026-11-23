package gent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

// Config supplies shared services and execution policy. Set it before creating or restoring a
// context, then treat it and its reachable values as read-only. Per-run progress belongs in
// ExecutionContext and LoopData. Shared callbacks and services own their concurrency contracts.
type Config struct {
	ToolChain          ToolChain
	Recorder           Recorder
	Codecs             *CodecRegistry
	Subscribers        []*SubscriberRegistration
	Hooks              *Hooks
	Limits             []Limit
	Recording          *RecordingConfig
	SteeringMessages   *SteeringMessages
	CompactionTrigger  CompactionTrigger
	CompactionStrategy CompactionStrategy
	NewLoopData        func() LoopData
}

// RecordingConfig bounds unsaved event evidence. These are hard allowances, not save triggers.
// Zero fields use defaults. Negative values are invalid. A failed or uncertain Save requires
// storage reconciliation and accepted-state restoration before another execution attempt.
type RecordingConfig struct {
	// MaxPendingEvents defaults to 65,536 unsaved events.
	MaxPendingEvents int
	// MaxPendingBytes defaults to 64 MiB of uncompressed encoded events, not total heap memory.
	// Storage compression does not increase this allowance.
	MaxPendingBytes int
	// SaveTimeout defaults to 30 seconds. Save uses a context independent of model cancellation.
	// A timeout does not prove storage rejection; the recorder must return its actual result.
	SaveTimeout time.Duration
}

// Hooks can change effective operation inputs before Gent freezes them. Both callbacks are
// optional and run synchronously. Return an error to prevent the dependent operation. Do not
// change operation identities, accepted history, or the saved task. Compose multiple changes in
// one callback in an explicit order. Callbacks can repeat after interruption; use managed tools
// for external actions that need recovery. Do not keep mutable access after returning.
type Hooks struct {
	BeforeModelCall func(ctx context.Context, event *BeforeModelCallEvent) error
	BeforeToolCall  func(ctx context.Context, event *BeforeToolCallEvent) error
}

func resolveConfig(input *Config) (*Config, error) {
	// Resolve zero values in a separate envelope. The caller's configuration remains unchanged;
	// configured services, slices, and callbacks are shared under the read-only contract.
	config := *input
	if (config.CompactionTrigger == nil) != (config.CompactionStrategy == nil) {
		return nil, errors.New("gent: configure both CompactionTrigger and CompactionStrategy")
	}
	if config.Codecs == nil {
		config.Codecs = NewCodecRegistry()
	}
	if config.NewLoopData == nil {
		config.NewLoopData = func() LoopData { return &BasicLoopData{} }
	}
	recording := RecordingConfig{}
	if config.Recording != nil {
		recording = *config.Recording
	}
	if recording.MaxPendingEvents < 0 || recording.MaxPendingBytes < 0 || recording.SaveTimeout < 0 {
		return nil, errors.New("gent: Recording bounds must not be negative")
	}
	if recording.MaxPendingEvents == 0 {
		recording.MaxPendingEvents = 65536
	}
	if recording.MaxPendingBytes == 0 {
		recording.MaxPendingBytes = 64 * 1024 * 1024
	}
	if recording.SaveTimeout == 0 {
		recording.SaveTimeout = 30 * time.Second
	}
	config.Recording = &recording
	for i, limit := range config.Limits {
		if (limit.Type != LimitExactKey && limit.Type != LimitKeyPrefix) || limit.Key == "" ||
			(limit.Scope != "" && limit.Scope != LimitTurn && limit.Scope != LimitRun) ||
			limit.MaxValue < 0 || math.IsNaN(limit.MaxValue) || math.IsInf(limit.MaxValue, 0) {
			return nil, fmt.Errorf("gent: invalid limit %d: type %q, key %q, scope %q, maximum %v",
				i, limit.Type, limit.Key, limit.Scope, limit.MaxValue)
		}
	}
	for i, registration := range config.Subscribers {
		if _, err := subscriberConfig(registration); err != nil {
			return nil, fmt.Errorf("gent: subscriber registration %d: %w", i, err)
		}
	}
	return &config, nil
}
