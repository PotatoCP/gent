package gent

import (
	"context"
	"time"
)

// Model is a provider adapter. The framework owns request capture, IDs, statistics and streaming.
// Implementations honor cancellation and complete Response only after their producer has exited.
type Model interface {
	// PrepareRequest resolves the current tool definitions and search configuration. It does
	// not call the provider or run domain tools. The managed boundary then runs hooks and saves
	// the effective request. chain is optional when a call has no tools.
	PrepareRequest(*ExecutionContext, *ModelCallRequest, ToolChain) (*ModelCallRequest, error)

	GenerateContentStream(context.Context, *ModelCallRequest) (Stream, error)
}

// ModelCall identifies a logical call independently from its physical worker attempt.
// Id is optional. An empty Id uses call order after the last checkpoint for recovery.
// Supply a stable, unique Id when calls can run concurrently or in a different order on recovery.
// StreamId and StreamTopicId group display output; they do not identify a logical call.
type ModelCall struct {
	Id            string
	StreamId      string
	StreamTopicId string
	Source        string
	Model         string
	Provider      string
}

// NewModelCallRequest resolves option functions exactly once, before mutable request hooks.
func NewModelCallRequest(
	messages []*ModelMessage,
	options ...CallOption,
) *ModelCallRequest {
	var resolved CallOptions
	WithStreamThinking(true)(&resolved)
	for _, option := range options {
		if option != nil {
			option(&resolved)
		}
	}
	return &ModelCallRequest{Messages: messages, Options: resolved, OptionCaptureComplete: true}
}

// ModelCallResponse is a provider response. It does not establish execution success.
type ModelCallResponse struct {
	// Choices contains the generated content choices.
	Choices []*ContentChoice

	// Info contains generation metadata including normalized token counts.
	Info *GenerationInfo
}

// ContentChoice contains the ordered readable response and its native continuation. Parts holds
// assistant groups and tool-call parts. Completion describes provider state, not Gent acceptance.
// Read-only derived views avoid parallel mutable copies of candidate content and tool calls.
type ContentChoice struct {
	Parts           []ContentPart
	Continuation    *ProviderContinuation
	CandidateSource *ContentSource
	Completion      ModelCompletion
}

// CandidateContent returns selected assistant items' inner parts in response order. A missing
// selection returns no candidate. The view and all reachable values are borrowed and read-only.
// The managed boundary validates source identities and classifications before output processing.
func (choice *ContentChoice) CandidateContent() []ContentPart {
	if choice.CandidateSource == nil {
		return nil
	}
	selected := make(map[string]bool, len(choice.CandidateSource.MessageIds))
	for _, id := range choice.CandidateSource.MessageIds {
		selected[id] = true
	}
	var result []ContentPart
	for _, part := range choice.Parts {
		if item, ok := part.(*AssistantContent); ok && selected[item.MessageId] {
			result = append(result, item.Content...)
		}
	}
	return result
}

// ToolCalls returns tool-call parts in response order. This view and reachable values are
// read-only.
// Completion and argument validation still determine whether any recorded call can execute.
func (choice *ContentChoice) ToolCalls() []*ModelToolCallRequest {
	var calls []*ModelToolCallRequest
	for _, part := range choice.Parts {
		switch part := part.(type) {
		case *ModelToolCallRequest:
			calls = append(calls, part)
		case ModelToolCallRequest:
			call := part
			calls = append(calls, &call)
		}
	}
	return calls
}

// GenerationInfo contains response metadata and normalized token counts. Counts use plain values:
// zero can mean a reported zero or an unavailable count. Empty strings mean not reported. These
// fields do not prove billing or a cache miss. Accepted model evidence preserves them on replay.
type GenerationInfo struct {
	// InputTokens is the normalized input count, including cached input. For Anthropic, it
	// includes ordinary input, cache reads, and cache writes. Do not add those counts again.
	InputTokens int

	// OutputTokens is the normalized output count. It can include reasoning tokens.
	OutputTokens int

	// TotalTokens is the total token count (InputTokens + OutputTokens).
	// Some providers return this directly; otherwise it's computed.
	TotalTokens int

	// CachedInputTokens is the number of input tokens read from cache.
	CachedInputTokens int

	// CacheWriteInputTokens is the number of input tokens written to cache, when reported.
	// It is separate from cache reads. Uncached input does not imply a cache write.
	CacheWriteInputTokens int

	// ReasoningTokens is the reported reasoning count. It is not an extra output charge.
	ReasoningTokens int

	// ProviderRequestId identifies the returned HTTP response for provider support. It is
	// not Gent's ModelCallId or a record of all requests made during an adapter retry.
	ProviderRequestId string

	// ProviderResponseId is the response or message ID supplied by the provider.
	ProviderResponseId string

	// ResponseModel is the returned model name/version. The requested alias is not a fallback.
	ResponseModel string

	// ResponseServiceTier is the returned processing tier. The requested tier is not a fallback.
	ResponseServiceTier string

	// RawGenerationInfo contains selected provider fields and Gent diagnostics, such as the
	// output-schema decision. It is not a complete copy of the provider response or usage.
	RawGenerationInfo map[string]any

	// Duration is local adapter elapsed time through response assembly and cleanup. Zero means
	// not measured. This is not provider compute time or the managed AfterModelCallEvent duration.
	Duration time.Duration

	// TimeToFirstContent is local elapsed time from the same start as Duration until the adapter
	// first observes text, exposed reasoning, tool-call content, or media. Measurement precedes
	// callbacks and blocked delivery; heartbeats and status-only events do not count. Zero means
	// not measured or no content observed. A non-streamed response is measured after parsing, so
	// this is not time to first token. Replayed results retain their original measurements.
	TimeToFirstContent time.Duration
}

// ModelCallRequest captures the debuggable and mutable parts of a model request.
// Model-call events use this typed shape instead of raw message slices so subscribers, trace
// capture, and tests all share one request contract.
// Hooks.BeforeModelCall can change Messages before freezing. Subscribers receive read-only values.
// Options contains meaningful resolved call options when the model wrapper can capture them.
// OptionCaptureComplete must be false when provider-specific options cannot be fully introspected.
type ModelCallRequest struct {
	Messages          []*ModelMessage
	Options           CallOptions
	SelectionRevision HistoryRevision
	ActiveEntryIds    []EntryId

	OptionCaptureComplete bool
	OptionCaptureNotes    []string
}

// Stream supplies provider progress and a complete response. Chunks do not carry an execution
// envelope or errors. Use Subscriber for UI events; use Response for provider-operation failures.
// Response waits for producer cleanup, including cancellation and final usage accounting.
// Close stops chunk delivery and requests producer cancellation. Response still waits for cleanup.
// The execution attempt closes managed streams when it ends, including saved response replay.
type Stream interface {
	Chunks() <-chan StreamChunk
	Response() (*ModelCallResponse, error)
	Close()
}

// StreamChunk contains ordered progress parts or the complete accepted answer. Assistant groups
// own item identity. RecordedEvent supplies run, turn, attempt, sequence, time, and call identity.
// accepted_answer links its complete processed content to source previews with SourceMessageIds.
// All published parts and reachable values are borrowed and read-only.
type StreamChunk struct {
	Content          []ContentPart
	SourceMessageIds []string
}
