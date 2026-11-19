package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// StatKey is a typed key for stats (counters and gauges).
//
// Standard gent keys use the "gent:" prefix. Users should use their own
// prefix (e.g., "myapp:") for custom metrics to avoid collisions.
//
// # Naming Convention
//
// Internal standard stats use a prefix convention to distinguish type:
//   - SC prefix = Stat Counter (e.g., SCInputTokens)
//   - SG prefix = Stat Gauge (e.g., SGToolCallsErrorConsecutive)
//
// # Counter Keys vs Gauge Keys
//
// Counter keys (SC*) increase across the complete run. GetTurnCounter subtracts the baseline
// accepted with NewTurn. Continue and Restore preserve both cumulative values and that baseline.
// Gauge keys (SG*) can increase or decrease. NewTurn clears all gauges before its admission save.
// These keys form an extensible metric namespace, not a finite enum.
//
// Example custom key:
//
//	myKey := gent.StatKey("myapp:api_calls")
//	execCtx.Stats().IncrCounter(myKey, 1)
type StatKey = core.StatKey

// KeyPrefix identifies standard Gent metrics; custom metrics must use a different prefix.
const KeyPrefix = core.KeyPrefix

// SCIterations counts entered Loop.Next iterations across all turns.
//
// This key is PROTECTED - attempts to modify it via IncrCounter from
// user code will be silently ignored. Only the Executor can increment
// this counter internally.
//
// Use LimitTurn for a per-turn allowance or LimitRun for a cumulative allowance.
// A restored unfinished iteration keeps its accepted count; entering it again does not add one.
//
// Read-only access:
//
//	iterations := execCtx.Stats().GetIterations()
const SCIterations StatKey = core.SCIterations

// SCInputTokens counts reported input tokens across model calls and turns.
const SCInputTokens StatKey = core.SCInputTokens

// SCInputTokensFor prefixes a cumulative input-token counter with its model name.
const SCInputTokensFor StatKey = core.SCInputTokensFor

// SCOutputTokens counts reported output tokens across model calls and turns.
const SCOutputTokens StatKey = core.SCOutputTokens

// SCOutputTokensFor prefixes a cumulative output-token counter with its model name.
const SCOutputTokensFor StatKey = core.SCOutputTokensFor

// SCTotalTokens counts reported input and output tokens across the complete run.
const SCTotalTokens StatKey = core.SCTotalTokens

// SCTotalTokensFor prefixes a cumulative total-token counter with its model name.
const SCTotalTokensFor StatKey = core.SCTotalTokensFor

// SCToolCalls counts fresh managed tool-call attempts.
const SCToolCalls StatKey = core.SCToolCalls

// SCToolCallsFor prefixes a fresh call-attempt counter with its tool name.
const SCToolCallsFor StatKey = core.SCToolCallsFor

// SCToolCallsErrorTotal counts unexpected managed tool operation failures.
// Counters increase across the run.
const SCToolCallsErrorTotal StatKey = core.SCToolCallsErrorTotal

// SCToolCallsErrorFor prefixes an unexpected operation failure counter with its tool name.
const SCToolCallsErrorFor StatKey = core.SCToolCallsErrorFor

// SGToolCallsErrorConsecutive measures consecutive fresh tool failures; fresh success resets it.
// Gauges reset on success and at a new turn.
const SGToolCallsErrorConsecutive StatKey = core.SGToolCallsErrorConsecutive

// SGToolCallsErrorConsecutiveFor prefixes the consecutive-failure gauge with its tool name.
const SGToolCallsErrorConsecutiveFor StatKey = core.SGToolCallsErrorConsecutiveFor

// SCToolchainParseErrorTotal counts native call and argument parsing failures.
// Counters
const SCToolchainParseErrorTotal StatKey = core.SCToolchainParseErrorTotal

// SGToolchainParseErrorConsecutive measures parsing failures since the last valid call.
// Gauges
const SGToolchainParseErrorConsecutive StatKey = core.SGToolchainParseErrorConsecutive

// SGScratchpadLength measures the current selected history entry count.
//
// Auto-updated when ExecutionContext.UpdateHistory changes the selected context.
// Reflects the current number of selected history entries in the scratchpad.
// Use for limits to prevent unbounded scratchpad growth.
//
// Example limit:
//
//	{Type: LimitExactKey, Key: SGScratchpadLength, MaxValue: 50}
const SGScratchpadLength StatKey = core.SGScratchpadLength

// SGInputTokensLastIteration measures reported input tokens in the current iteration.
// Aggregate keys (all models combined)
const SGInputTokensLastIteration StatKey = core.SGInputTokensLastIteration

// SGOutputTokensLastIteration measures reported output tokens in the current iteration.
const SGOutputTokensLastIteration StatKey = core.SGOutputTokensLastIteration

// SGTotalTokensLastIteration measures reported total tokens in the current iteration.
const SGTotalTokensLastIteration StatKey = core.SGTotalTokensLastIteration

// SGInputTokensLastIterationFor prefixes the iteration input-token gauge with its model name.
// Per-model keys (append model name as suffix)
const SGInputTokensLastIterationFor StatKey = core.SGInputTokensLastIterationFor

// SGOutputTokensLastIterationFor prefixes the iteration output-token gauge with its model name.
const SGOutputTokensLastIterationFor StatKey = core.SGOutputTokensLastIterationFor

// SGTotalTokensLastIterationFor prefixes the iteration total-token gauge with its model name.
const SGTotalTokensLastIterationFor StatKey = core.SGTotalTokensLastIterationFor

// SCCodeExecutions counts programmatic tool-call scripts entered during the run.
// Counter: total code blocks executed
const SCCodeExecutions StatKey = core.SCCodeExecutions

// SCCodeExecutionsError counts programmatic tool-call scripts that failed.
// Counter: code blocks that failed
const SCCodeExecutionsError StatKey = core.SCCodeExecutionsError

// SGCodeExecutionsErrorConsecutive measures script failures since the last successful script.
// Gauge: consecutive code execution errors
// (reset on success)
const SGCodeExecutionsErrorConsecutive StatKey = core.SGCodeExecutionsErrorConsecutive

// SCCompactions counts accepted compactions across all turns.
//
// Auto-updated when CompactionEvent is published. Tracks the
// total number of successful compactions performed.
const SCCompactions StatKey = core.SCCompactions

// SCRepetitionLoopTotal counts detected model repetition failures across iterations and turns.
// Counter: total loops detected across all iterations.
const SCRepetitionLoopTotal StatKey = core.SCRepetitionLoopTotal

// SGRepetitionLoopConsecutive measures repetition failures since a successful tool call or answer.
// Gauge: consecutive loops without a successful iteration in between.
// Reset when the agent successfully executes a tool call or terminates.
const SGRepetitionLoopConsecutive StatKey = core.SGRepetitionLoopConsecutive

// SCToolCallsInvalidArgumentsTotal counts calls rejected for invalid arguments.
const SCToolCallsInvalidArgumentsTotal StatKey = core.SCToolCallsInvalidArgumentsTotal

// SCToolCallsInvalidArgumentsFor prefixes the invalid-argument counter with its tool name.
const SCToolCallsInvalidArgumentsFor StatKey = core.SCToolCallsInvalidArgumentsFor

// SCToolRecoveryErrorTotal counts failed recovery checks, separately from original tool calls.
const SCToolRecoveryErrorTotal StatKey = core.SCToolRecoveryErrorTotal

// SCToolRecoveryErrorFor prefixes the failed-recovery counter with its tool name.
const SCToolRecoveryErrorFor StatKey = core.SCToolRecoveryErrorFor
