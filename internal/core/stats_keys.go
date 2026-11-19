package gent

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
type StatKey string

// KeyPrefix identifies standard Gent metrics; custom metrics must use a different prefix.
const KeyPrefix = "gent:"

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
const SCIterations StatKey = "gent:iterations"

// Token tracking keys (Counter).
//
// Auto-updated when AfterModelCallEvent is published:
//
//	execCtx.PublishAfterModelCall(
//	    "gpt-4", request, response, duration, nil,
//	)
//	// Auto-increments: SCInputTokens, SCInputTokensFor+"gpt-4",
//	//                  SCOutputTokens, SCOutputTokensFor+"gpt-4",
//	//                  SCTotalTokens, SCTotalTokensFor+"gpt-4"
//
// Use SCInputTokensFor/SCOutputTokensFor + model name for per-model
// limits:
//
//	{Type: LimitExactKey, Key: SCInputTokensFor + "gpt-4", MaxValue: 10000}
const (
	// SCInputTokens counts reported input tokens across model calls and turns.
	SCInputTokens StatKey = "gent:input_tokens"
	// SCInputTokensFor prefixes a cumulative input-token counter with its model name.
	SCInputTokensFor StatKey = "gent:input_tokens:" // + model name
	// SCOutputTokens counts reported output tokens across model calls and turns.
	SCOutputTokens StatKey = "gent:output_tokens"
	// SCOutputTokensFor prefixes a cumulative output-token counter with its model name.
	SCOutputTokensFor StatKey = "gent:output_tokens:" // + model name
)

// Total token tracking keys (Counter).
//
// Sum of input + output tokens. Auto-updated when
// AfterModelCallEvent is published alongside the individual
// input/output token counters.
//
// Use SCTotalTokensFor + model name for per-model limits:
//
//	{Type: LimitExactKey, Key: SCTotalTokensFor + "gpt-4", MaxValue: 20000}
const (
	// SCTotalTokens counts reported input and output tokens across the complete run.
	SCTotalTokens StatKey = "gent:total_tokens"
	// SCTotalTokensFor prefixes a cumulative total-token counter with its model name.
	SCTotalTokensFor StatKey = "gent:total_tokens:" // + model name
)

// Tool call tracking keys (Counter).
//
// Auto-updated when BeforeToolCallEvent is published:
//
//	execCtx.PublishBeforeToolCall("search", args)
//	// Auto-increments: SCToolCalls, SCToolCallsFor+"search"
//
// Use for per-tool limits:
//
//	// Limit specific tool to 10 calls
//	{
//	    Type: LimitExactKey,
//	    Key: SCToolCallsFor + "expensive_api",
//	    MaxValue: 10,
//	}
//
//	// Limit ANY tool to 50 calls
//	{Type: LimitKeyPrefix, Key: SCToolCallsFor, MaxValue: 50}
const (
	// SCToolCalls counts fresh managed tool-call attempts.
	SCToolCalls StatKey = "gent:tool_calls"
	// SCToolCallsFor prefixes a fresh call-attempt counter with its tool name.
	SCToolCallsFor StatKey = "gent:tool_calls:" // + tool name
)

// Tool call error tracking keys.
//
// Auto-updated when AfterToolCallEvent with Error is published.
//
// Total/per-tool error keys are cumulative counters.
// Consecutive error keys are gauges. Fresh executed or blocked calls reset them.
//
// DefaultLimits supplies an optional preset of three consecutive errors; Config has no limits.
const (
	// SCToolCallsErrorTotal counts unexpected managed tool operation failures.
	// Counters increase across the run.
	SCToolCallsErrorTotal StatKey = "gent:tool_calls_error_total"
	// SCToolCallsErrorFor prefixes an unexpected operation failure counter with its tool name.
	SCToolCallsErrorFor StatKey = "gent:tool_calls_error:" // + tool

	// SGToolCallsErrorConsecutive measures consecutive fresh tool failures; fresh success resets it.
	// Gauges reset on success and at a new turn.
	SGToolCallsErrorConsecutive StatKey = "gent:tool_calls_error_consecutive"
	// SGToolCallsErrorConsecutiveFor prefixes the consecutive-failure gauge with its tool name.
	SGToolCallsErrorConsecutiveFor StatKey = "gent:tool_calls_error_consecutive:" // + tool
)

// Toolchain parse error tracking keys.
//
// Auto-updated when ParseErrorEvent with ErrorType="toolchain" is
// published. These track errors when the ToolChain fails to parse
// native tool calls and their JSON arguments.
//
// DefaultLimits supplies an optional preset of three consecutive errors; Config has no limits.
const (
	// SCToolchainParseErrorTotal counts native call and argument parsing failures.
	// Counters
	SCToolchainParseErrorTotal StatKey = "gent:toolchain_parse_error_total"

	// SGToolchainParseErrorConsecutive measures parsing failures since the last valid call.
	// Gauges
	SGToolchainParseErrorConsecutive StatKey = "gent:toolchain_parse_error_consecutive"
)

// SGScratchpadLength measures the current selected history entry count.
//
// Auto-updated when ExecutionContext.UpdateHistory changes the selected context.
// Reflects the current number of selected history entries in the scratchpad.
// Use for limits to prevent unbounded scratchpad growth.
//
// Example limit:
//
//	{Type: LimitExactKey, Key: SGScratchpadLength, MaxValue: 50}
const SGScratchpadLength StatKey = "gent:scratchpad_length"

// Last-iteration token tracking keys (Gauge).
//
// These gauges track token usage for the current/last iteration
// only. Unlike cumulative counters (SCInputTokens etc.), these
// gauges reset to 0 at each iteration start via
// BeforeIterationEvent.
//
// These gauges belong to the current conversation and are cleared at each iteration boundary.
//
// Auto-updated when AfterModelCallEvent is published. Within a
// single iteration, multiple model calls accumulate (e.g., main
// agent + tool that calls a model). Reset to 0 on the next
// BeforeIterationEvent.
//
// Example limits:
//
//	// No single iteration should use more than 50000 total tokens
//	{
//	    Type: LimitExactKey,
//	    Key: SGTotalTokensLastIteration,
//	    MaxValue: 50000,
//	}
//
//	// No single iteration should use more than 10000 tokens on
//	// a specific model
//	{
//	    Type: LimitExactKey,
//	    Key: SGTotalTokensLastIterationFor + "gpt-4",
//	    MaxValue: 10000,
//	}
//
//	// Prefix limit on per-model last-iteration tokens
//	{
//	    Type: LimitKeyPrefix,
//	    Key: SGTotalTokensLastIterationFor,
//	    MaxValue: 10000,
//	}
const (
	// SGInputTokensLastIteration measures reported input tokens in the current iteration.
	// Aggregate keys (all models combined)
	SGInputTokensLastIteration StatKey = "gent:input_tokens_last_iteration"
	// SGOutputTokensLastIteration measures reported output tokens in the current iteration.
	SGOutputTokensLastIteration StatKey = "gent:output_tokens_last_iteration"
	// SGTotalTokensLastIteration measures reported total tokens in the current iteration.
	SGTotalTokensLastIteration StatKey = "gent:total_tokens_last_iteration"

	// SGInputTokensLastIterationFor prefixes the iteration input-token gauge with its model name.
	// Per-model keys (append model name as suffix)
	SGInputTokensLastIterationFor StatKey = "gent:input_tokens_last_iteration:" // + model
	// SGOutputTokensLastIterationFor prefixes the iteration output-token gauge with its model name.
	SGOutputTokensLastIterationFor StatKey = "gent:output_tokens_last_iteration:" // + model
	// SGTotalTokensLastIterationFor prefixes the iteration total-token gauge with its model name.
	SGTotalTokensLastIterationFor StatKey = "gent:total_tokens_last_iteration:" // + model
)

// Code execution tracking keys (Programmatic Tool Calling).
//
// Auto-updated by JsToolChainWrapper when code blocks
// are executed.
//
// DefaultLimits supplies an optional preset of three consecutive code errors.
const (
	// SCCodeExecutions counts programmatic tool-call scripts entered during the run.
	// Counter: total code blocks executed
	SCCodeExecutions StatKey = "gent:code_executions"

	// SCCodeExecutionsError counts programmatic tool-call scripts that failed.
	// Counter: code blocks that failed
	SCCodeExecutionsError StatKey = "gent:code_executions_error"

	// SGCodeExecutionsErrorConsecutive measures script failures since the last successful script.
	// Gauge: consecutive code execution errors
	// (reset on success)
	SGCodeExecutionsErrorConsecutive StatKey = "gent:code_executions_error_consecutive"
)

// SCCompactions counts accepted compactions across all turns.
//
// Auto-updated when CompactionEvent is published. Tracks the
// total number of successful compactions performed.
const SCCompactions StatKey = "gent:compactions"

// Repetition loop detection tracking keys.
//
// Updated by the Loop when streaming repetition detection triggers.
// These are separate from format parse errors — a loop is a distinct failure
// mode where the model repeats the same reasoning/action.
//
// Configure a limit on repetition statistics when this protection is required.
const (
	// SCRepetitionLoopTotal counts detected model repetition failures across iterations and turns.
	// Counter: total loops detected across all iterations.
	SCRepetitionLoopTotal StatKey = "gent:repetition_loop_total"

	// SGRepetitionLoopConsecutive measures repetition failures since a successful tool call or answer.
	// Gauge: consecutive loops without a successful iteration in between.
	// Reset when the agent successfully executes a tool call or terminates.
	SGRepetitionLoopConsecutive StatKey = "gent:repetition_loop_consecutive"
)

// protectedKeys contains keys that cannot be modified by user code
// via IncrCounter. Protected keys can still be incremented internally
// by the framework (e.g., the executor increments SCIterations).
var protectedKeys = map[StatKey]bool{
	SCIterations: true,
}

// isProtectedKey returns true if the key is protected from user
// modification.
func isProtectedKey(key StatKey) bool {
	return protectedKeys[key]
}

// Input rejection is expected control flow, separate from unexpected operation failures.
const (
	// SCToolCallsInvalidArgumentsTotal counts calls rejected for invalid arguments.
	SCToolCallsInvalidArgumentsTotal StatKey = "gent:tool_calls_invalid_arguments_total"
	// SCToolCallsInvalidArgumentsFor prefixes the invalid-argument counter with its tool name.
	SCToolCallsInvalidArgumentsFor StatKey = "gent:tool_calls_invalid_arguments:"
	// SCToolRecoveryErrorTotal counts failed recovery checks, separately from original tool calls.
	SCToolRecoveryErrorTotal StatKey = "gent:tool_recovery_error_total"
	// SCToolRecoveryErrorFor prefixes the failed-recovery counter with its tool name.
	SCToolRecoveryErrorFor StatKey = "gent:tool_recovery_error:"
)
