package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// ExecutionContext owns the mutable state of one conversation. Runner creates or restores it.
// NewTurn and Continue run separate attempts with fresh cancellation and delivery resources.
// Stats, managed model/tool calls, and history changes use the same accepted-save contract.
// Full archived history belongs in RunView; this context retains state needed for execution.
//
// Methods synchronize framework state. Data returns application LoopData, whose author must
// synchronize custom fields when managed operations can access them concurrently. Borrowed
// configuration, saved messages, results, and subscriber values are read-only.
type ExecutionContext = core.ExecutionContext

// ModelCallEventOption configures model call event metadata.
type ModelCallEventOption = core.ModelCallEventOption

// WithModelCallId associates before/after model events with a specific model call.
func WithModelCallId(id string) ModelCallEventOption {
	return core.WithModelCallId(id)
}

// WithModelStream associates model events with a stream ID and topic ID.
func WithModelStream(streamId string, streamTopicId string) ModelCallEventOption {
	return core.WithModelStream(streamId, streamTopicId)
}

// WithModelCallSource pins model event source when completion happens asynchronously.
func WithModelCallSource(source string) ModelCallEventOption {
	return core.WithModelCallSource(source)
}

// WithModelProvider records the provider name when the model wrapper knows it.
func WithModelProvider(provider string) ModelCallEventOption {
	return core.WithModelProvider(provider)
}

// WithToolCallAttempt identifies the physical attempt reconciled by a recovery adapter.
func WithToolCallAttempt(id string) ToolCallEventOption {
	return core.WithToolCallAttempt(id)
}

// ToolCallEventOption configures tool call identity, source, or effective input validation.
type ToolCallEventOption = core.ToolCallEventOption

// WithToolCallInputValidation checks effective input after the Hook and before the tool-ready
// savepoint. Custom chains can check their typed input and schema here. Return an unexpected
// operation error on invalid effective input; do not send code/configuration defects to the model.
func WithToolCallInputValidation(validate func(any) error) ToolCallEventOption {
	return core.WithToolCallInputValidation(validate)
}

// WithToolCallId associates before/after tool events with a specific tool call.
func WithToolCallId(id string) ToolCallEventOption {
	return core.WithToolCallId(id)
}

// WithToolCallSource pins tool event source when completion happens asynchronously.
func WithToolCallSource(source string) ToolCallEventOption {
	return core.WithToolCallSource(source)
}
