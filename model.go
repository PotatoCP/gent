package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// Model is a provider adapter. The framework owns request capture, IDs, statistics and streaming.
// Implementations honor cancellation and complete Response only after their producer has exited.
type Model = core.Model

// ModelCall identifies a logical call independently from its physical worker attempt.
// Id is optional. An empty Id uses call order after the last checkpoint for recovery.
// Supply a stable, unique Id when calls can run concurrently or in a different order on recovery.
// StreamId and StreamTopicId group display output; they do not identify a logical call.
type ModelCall = core.ModelCall

// NewModelCallRequest resolves option functions exactly once, before mutable request hooks.
func NewModelCallRequest(
	messages []*ModelMessage,
	options ...CallOption,
) *ModelCallRequest {
	return core.NewModelCallRequest(messages, options...)
}

// ModelCallResponse is a provider response. It does not establish execution success.
type ModelCallResponse = core.ModelCallResponse

// ContentChoice contains the ordered readable response and its native continuation. Parts holds
// assistant groups and tool-call parts. Completion describes provider state, not Gent acceptance.
// Read-only derived views avoid parallel mutable copies of candidate content and tool calls.
type ContentChoice = core.ContentChoice

// GenerationInfo contains normalized token counts, provider identity, and local timings.
// Counts use plain values: zero can mean a reported zero or an unavailable count. Empty strings
// mean not reported. Accepted model evidence preserves these values and timings on replay.
type GenerationInfo = core.GenerationInfo

// ModelCallRequest captures the debuggable and mutable parts of a model request.
// Model-call events use this typed shape instead of raw message slices so subscribers, trace
// capture, and tests all share one request contract.
// Hooks.BeforeModelCall can change Messages before freezing. Subscribers receive read-only values.
// Options contains meaningful resolved call options when the model wrapper can capture them.
// OptionCaptureComplete must be false when provider-specific options cannot be fully introspected.
type ModelCallRequest = core.ModelCallRequest

// Stream supplies provider progress and a complete response. Chunks do not carry an execution
// envelope or errors. Use Subscriber for UI events; use Response for provider-operation failures.
// Response waits for producer cleanup, including cancellation and final usage accounting.
// Close stops chunk delivery and requests producer cancellation. Response still waits for cleanup.
// The execution attempt closes managed streams when it ends, including saved response replay.
type Stream = core.Stream

// StreamChunk contains ordered progress parts or the complete accepted answer. Assistant groups
// own item identity. RecordedEvent supplies run, turn, attempt, sequence, time, and call identity.
// accepted_answer links its complete processed content to source previews with SourceMessageIds.
// All published parts and reachable values are borrowed and read-only.
type StreamChunk = core.StreamChunk
