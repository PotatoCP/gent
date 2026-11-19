package examples

import (
	"context"
	"fmt"
	"strings"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/compaction"
	"github.com/rickchristie/gent/format"
)

func configuration() []*gent.Config {
	// docs:start config-limits
	unlimited := &gent.Config{}
	standard := &gent.Config{Limits: gent.DefaultLimits()}
	oneRule := &gent.Config{Limits: []gent.Limit{{
		Type:     gent.LimitExactKey,
		Key:      gent.SCIterations,
		MaxValue: 8,
		Scope:    gent.LimitTurn,
	}}}
	// One rule replaces the preset. Gent does not add hidden execution-limit rules.
	// docs:end config-limits
	// docs:start config-run-limit
	runLimit := &gent.Config{Limits: []gent.Limit{{
		Type:     gent.LimitExactKey,
		Key:      gent.SCIterations,
		MaxValue: 80,
		Scope:    gent.LimitRun,
	}}}
	// A new turn resets its baseline. It does not reset the run counter.
	// docs:end config-run-limit
	return []*gent.Config{unlimited, standard, oneRule, runLimit}
}

func counters(execCtx *gent.ExecutionContext) (int64, int64) {
	// docs:start stats-read
	stats := execCtx.Stats()
	total := stats.GetCounter(gent.SCIterations)
	thisTurn := stats.GetTurnCounter(gent.SCIterations)
	// Continue preserves both the accepted counter and the turn baseline.
	// docs:end stats-read
	// docs:start stats-custom
	const checkedFiles gent.StatKey = "app:checked_files"
	stats.IncrCounter(checkedFiles, 1)
	// Use the same key in a configured Limit to bound this work.
	// docs:end stats-custom
	return total, thisTurn
}

func requestHooks() *gent.Hooks {
	// docs:start hooks-model
	hooks := &gent.Hooks{
		BeforeModelCall: func(ctx context.Context, event *gent.BeforeModelCallEvent) error {
			event.Request.Messages = append(event.Request.Messages,
				gent.TextParts(gent.ChatMessageTypeHuman, "Use metric units."))
			return ctx.Err()
		},
	}
	// This changes the effective request. It does not change the accepted Task.
	// docs:end hooks-model
	// docs:start hooks-tool
	hooks.BeforeToolCall = func(ctx context.Context, event *gent.BeforeToolCallEvent) error {
		if event.ToolName == "lookup_order" {
			input := event.Args.(*lookupInput)
			input.ID = strings.TrimSpace(input.ID)
		}
		return ctx.Err()
	}
	// A returned error prevents the call. Hooks can repeat after interruption.
	// docs:end hooks-tool
	return hooks
}

func compactConfig(model gent.Model) *gent.Config {
	// docs:start compaction-summary
	trigger := compaction.NewStatThresholdTrigger().OnCounter(gent.SCIterations, 20)
	strategy := compaction.NewSummarization(model, format.NewXML()).WithKeepRecent(4)
	config := &gent.Config{
		CompactionTrigger:  trigger,
		CompactionStrategy: strategy,
	}
	// Supply both. The summary model can differ from the main loop's model.
	// docs:end compaction-summary
	// docs:start compaction-retention
	strategy.WithUserMessageRetention(&gent.UserMessageRetentionConfig{
		MaxBytes: 32 * 1024,
	})
	// The default keeps newer user input and trims the boundary at valid UTF-8.
	// This policy runs at compaction, not on each NewTurn.
	// docs:end compaction-retention
	return config
}

func windowConfig() *gent.Config {
	// docs:start compaction-window
	config := &gent.Config{
		CompactionTrigger:  compaction.NewStatThresholdTrigger().OnCounter(gent.SCIterations, 10),
		CompactionStrategy: compaction.NewSlidingWindow(6),
	}
	// Dropped active entries remain in the accepted archive.
	// docs:end compaction-window
	return config
}

// docs:start retention-custom
type NoUserRetention struct{}

func (*NoUserRetention) Retain(input *gent.UserMessageRetentionInput) (
	*gent.UserMessageRetentionResult, error,
) {
	if len(input.Messages) == 0 && !input.EarlierContentOmitted {
		return &gent.UserMessageRetentionResult{}, nil
	}
	if len(input.Marker) > input.MaxBytes {
		return nil, fmt.Errorf("omission notice needs %d bytes; allowance is %d",
			len(input.Marker), input.MaxBytes)
	}
	// The marker counts toward the allowance. No input or history is changed.
	return &gent.UserMessageRetentionResult{
		EarlierContentOmitted: true,
		RetainedBytes:         len(input.Marker),
	}, nil
}

// docs:end retention-custom
