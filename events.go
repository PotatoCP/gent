package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// TerminationReason indicates why execution terminated.
type TerminationReason = core.TerminationReason

// ExecutionStatus distinguishes accepted completion, recoverable pause, and inability to start.
type ExecutionStatus = core.ExecutionStatus

// ExecutionFinished means a completed-turn result was accepted.
const ExecutionFinished ExecutionStatus = core.ExecutionFinished

// ExecutionPaused means execution stopped with the saved turn still unfinished.
const ExecutionPaused ExecutionStatus = core.ExecutionPaused

// ExecutionBusy means another call owns execution of this context, including its cleanup.
const ExecutionBusy ExecutionStatus = core.ExecutionBusy

// ExecutionNoActiveTurn means Continue has no unfinished turn to execute.
const ExecutionNoActiveTurn ExecutionStatus = core.ExecutionNoActiveTurn

// TerminationCanceled means NewTurn closed the previous unfinished turn.
const TerminationCanceled TerminationReason = core.TerminationCanceled

// TerminationStopped means Stop paused the turn without completing it.
const TerminationStopped TerminationReason = core.TerminationStopped

// LimitExceeded preserves the effective rule and measured value at a saved pause. Later Runner
// configuration does not change this explanation. Scope selects counter measurement; gauges
// always measure the current turn. Integer counter getters retain exact accounting.
type LimitExceeded = core.LimitExceeded

// TerminationSuccess means the Loop returned LATerminate.
const TerminationSuccess TerminationReason = core.TerminationSuccess

// TerminationRefused means the provider explicitly refused the request.
const TerminationRefused TerminationReason = core.TerminationRefused

// TerminationLimitExceeded means a configured limit was
// exceeded. Inspect ExecutionResult.ExceededLimit for
// details about which limit was hit.
const TerminationLimitExceeded TerminationReason = core.TerminationLimitExceeded

// ExecutionResult is read-only, including every reachable value. Status determines the next
// action. Busy and NoActiveTurn have no termination or output details. Unexpected operation
// failures return an error with no result; they do not create a completed error turn.
type ExecutionResult = core.ExecutionResult

// Event is the marker interface for all events in the unified event system.
//
// Events are published via ExecutionContext.PublishXXX() methods and are used for:
//   - Observability: All events are recorded in []Event for debugging
//   - Subscriptions: Subscribers receive events via type-safe interfaces
//   - Stats: Certain events automatically update execution statistics
//   - Limits: Stats updates may trigger limit checks
//
// # Publishing Events
//
// Use the typed PublishXXX methods on ExecutionContext for framework events:
//
//	ctx.PublishBeforeIteration()
//	ctx.PublishAfterModelCall(model, request, response, duration, err)
//
// For custom events, use PublishCommonEvent:
//
//	ctx.PublishCommonEvent("myapp:cache_hit", "Cache lookup succeeded", cacheData)
//
// # Accessing Events
//
//	events := execCtx.Events()
//	for _, event := range events {
//	    switch e := event.(type) {
//	    case *AfterModelCallEvent:
//	        log.Printf("Model %s used %d tokens", e.Model, e.InputTokens)
//	    case *CommonEvent:
//	        log.Printf("Event %s: %s", e.EventName, e.Description)
//	    }
//	}
type Event = core.Event

// BaseEvent contains common fields for all events.
// These fields are automatically populated by ExecutionContext when publishing.
//
// When you publish an event via ctx.PublishXXX(), the BaseEvent fields are set:
//   - EventName: Set by the PublishXXX method (e.g., "gent:iteration:before")
//   - Timestamp: Current time
//   - Iteration: Current iteration number (1-indexed, 0 if before first iteration)
//   - Source: Hierarchical display path for the publishing context
//   - ContextId: Opaque identity for the publishing context
type BaseEvent = core.BaseEvent

// BeforeExecutionEvent is published once before the first iteration starts.
// Subscribers can use this for initialization tasks.
type BeforeExecutionEvent = core.BeforeExecutionEvent

// AfterExecutionEvent is published once after execution ends, regardless of how it ended.
// Subscribers can use this for cleanup and final reporting.
type AfterExecutionEvent = core.AfterExecutionEvent

// BeforeIterationEvent is published before each iteration.
// This event is read-only evidence. The Loop owns changes to its recoverable LoopData.
type BeforeIterationEvent = core.BeforeIterationEvent

// AfterIterationEvent is published after each iteration completes.
type AfterIterationEvent = core.AfterIterationEvent

// BeforeModelCallEvent is published before each model API call.
// Hooks.BeforeModelCall can change Request.Messages before Gent freezes the effective request.
// Appended messages are sent to the model but are not persisted in conversation history.
type BeforeModelCallEvent = core.BeforeModelCallEvent

// AfterModelCallEvent is published after each model API call completes.
// Stats updated: InputTokens, OutputTokens (and per-model variants).
type AfterModelCallEvent = core.AfterModelCallEvent

// BeforeToolCallEvent is published before each tool execution.
// Hooks.BeforeToolCall can change Args before Gent freezes the effective input.
// Stats updated: ToolCalls (and per-tool variants).
type BeforeToolCallEvent = core.BeforeToolCallEvent

// AfterToolCallEvent is published after each tool execution completes.
// Stats updated: ToolCallsErrorTotal, ToolCallsErrorConsecutive (on error).
type AfterToolCallEvent = core.AfterToolCallEvent

// ParseErrorEvent is published when parsing fails.
// Stats updated: Based on ErrorType - format, toolchain, termination, or section errors.
type ParseErrorEvent = core.ParseErrorEvent

// ErrorEvent is published when an error occurs during execution.
type ErrorEvent = core.ErrorEvent

// LimitExceededEvent is published when a configured limit is exceeded.
// This event is published at the exact moment the limit is exceeded,
// before context cancellation propagates.
//
// Stats updated: None (this is an observability event only).
type LimitExceededEvent = core.LimitExceededEvent

// CompactionEvent is published after a successful compaction.
// Stats updated: SCCompactions counter is incremented.
type CompactionEvent = core.CompactionEvent

// CommonEvent is for user-defined events.
//
// Use ExecutionContext.PublishCommonEvent() to publish custom events:
//
//	ctx.PublishCommonEvent("myapp:cache_hit", "Cache lookup succeeded", cacheData)
//
// The EventName field in BaseEvent is set from the first argument.
// Use a namespaced format to avoid collisions (e.g., "myapp:event_name").
type CommonEvent = core.CommonEvent

// StateChangeEvent reports an accepted structured history transition. Context selections reference
// immutable entries, so callers can reconstruct every context revision without text diffs.
type StateChangeEvent = core.StateChangeEvent
