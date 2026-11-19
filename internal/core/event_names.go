package gent

// EventNameStateChange identifies a structured history selection update.
const EventNameStateChange = "gent:state_change"

// Event names are open namespace keys, not a closed enum. Applications can define new names.
// These constants identify the framework event names.
//
// # Naming Convention
//
// Event names follow the pattern: "namespace:category:timing"
//   - namespace: "gent" for framework events, application name for custom events
//   - category: what the event is about (execution, iteration, model_call, tool_call, etc.)
//   - timing: when in the lifecycle (before, after) - omitted for single events
//
// # Examples
//
//	gent:iteration:before    // Framework: before iteration
//	gent:model_call:after    // Framework: after model call
//	gent:parse_error         // Framework: parse error (no timing)
//	myapp:cache_hit          // Application: custom event
const (
	// Execution lifecycle
	EventNameExecutionBefore = "gent:execution:before"
	EventNameExecutionAfter  = "gent:execution:after"

	// Iteration lifecycle
	EventNameIterationBefore = "gent:iteration:before"
	EventNameIterationAfter  = "gent:iteration:after"

	// Model calls
	EventNameModelCallBefore = "gent:model_call:before"
	EventNameModelCallAfter  = "gent:model_call:after"

	// Tool calls
	EventNameToolCallBefore = "gent:tool_call:before"
	EventNameToolCallAfter  = "gent:tool_call:after"

	// Errors and validation
	EventNameParseError = "gent:parse_error"
	EventNameError      = "gent:error"

	// Limits
	EventNameLimitExceeded = "gent:limit_exceeded"

	// Compaction
	EventNameCompaction = "gent:compaction"
)

// ParseErrorType categorizes the source of a parse error.
type ParseErrorType string

// ParseErrorTypeToolchain identifies invalid native calls or JSON tool arguments.
const ParseErrorTypeToolchain ParseErrorType = "toolchain"
