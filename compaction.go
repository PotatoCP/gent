package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// CompactionTrigger decides WHEN scratchpad compaction should
// run.
//
// The executor checks the trigger at the start of each loop
// iteration (including the first turn iteration), before BeforeIterationEvent is
// published. If ShouldCompact returns true, the configured
// CompactionStrategy.Compact is called.
//
// # Available Implementations
//
//   - compaction.NewStatThresholdTrigger: fires when stat
//     thresholds are exceeded (counter deltas or gauge
//     absolutes)
//
// # Implementing Custom Triggers
//
//	type TokenBudgetTrigger struct {
//	    maxTokens int64
//	}
//
//	func (t *TokenBudgetTrigger) ShouldCompact(
//	    execCtx *ExecutionContext,
//	) (bool, error) {
//	    return execCtx.Stats().GetTotalTokens() > t.maxTokens, nil
//	}
type CompactionTrigger = core.CompactionTrigger

// CompactionStrategy decides HOW to compact the scratchpad.
//
// The strategy receives read-only records and their IDs, then proposes appended summary entries and
// a resulting selection. The executor accepts the decision, selection, trigger baseline, stats,
// and checkpoint together. Original entries remain available even when absent from the selection.
//
// # Error Handling
//
// If Compact returns an error, the attempt ends without a terminal error save. Fix the cause,
// restore accepted state, and Continue. Silent failure would permit unbounded context growth;
// accepting a partial compaction would instead lose the saved correspondence between summary,
// selection, custom data, and counters.
//
// # Available Implementations
//
//   - compaction.NewSlidingWindow: keeps last N entries
//   - compaction.NewSummarization: progressive summarization
//     with configurable keep-recent window
//
// # Implementing Custom Strategies
//
//	type MyStrategy struct{}
//
//	func (s *MyStrategy) Compact(
//	    execCtx *ExecutionContext, input *CompactionInput,
//	) (*HistoryUpdate, error) {
//	    return &HistoryUpdate{
//	        ExpectedDraftRevision: input.Revision, Active: input.EntryIds(),
//	    }, nil
//	}
type CompactionStrategy = core.CompactionStrategy

// CompactionInput is a borrowed read-only view of exactly the entries selected for this decision.
type CompactionInput = core.CompactionInput
