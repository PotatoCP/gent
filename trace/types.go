package trace

import (
	"time"

	"github.com/rickchristie/gent"
)

// SchemaVersion is the JSON schema version for trace snapshots and events.
const SchemaVersion = gent.RunSchemaVersion

// EventType is the normalized trace event type used by UIs and replay code.
type EventType string

const (
	// EventTypeAttemptStarted identifies a new worker attempt on the saved task.
	EventTypeAttemptStarted EventType = "attempt_started"
	// EventTypeAttemptInterrupted identifies an earlier attempt without accepted completion.
	EventTypeAttemptInterrupted EventType = "attempt_interrupted"
	// EventTypeExecutionStarted identifies the admission of a new turn.
	EventTypeExecutionStarted EventType = "execution_started"
	// EventTypeExecutionFinished identifies an accepted completed turn.
	EventTypeExecutionFinished EventType = "execution_finished"
	// EventTypeExecutionPaused identifies an accepted pause of an unfinished turn.
	EventTypeExecutionPaused EventType = "execution_paused"
	// EventTypeIterationStarted identifies entry into a loop iteration.
	EventTypeIterationStarted EventType = "iteration_started"
	// EventTypeIterationFinished identifies a completed loop iteration.
	EventTypeIterationFinished EventType = "iteration_finished"
	// EventTypeModelCallStarted identifies the effective model request.
	EventTypeModelCallStarted EventType = "model_call_started"
	// EventTypeModelCallFinished identifies the complete model response.
	EventTypeModelCallFinished EventType = "model_call_finished"
	// EventTypeModelStreamChunk identifies provisional model output.
	EventTypeModelStreamChunk EventType = "model_stream_chunk"
	// EventTypeAcceptedAnswer identifies complete output accepted by Gent.
	EventTypeAcceptedAnswer EventType = "accepted_answer"
	// EventTypeToolCallStarted identifies a prepared managed tool call.
	EventTypeToolCallStarted EventType = "tool_call_started"
	// EventTypeToolCallFinished identifies a managed tool result or recovery report.
	EventTypeToolCallFinished EventType = "tool_call_finished"
	// EventTypeParseError identifies invalid native tool input.
	EventTypeParseError EventType = "parse_error"
	// EventTypeOutputValidation identifies an output validation decision.
	EventTypeOutputValidation EventType = "output_validation"
	// EventTypeError identifies diagnostic error evidence.
	EventTypeError EventType = "error"
	// EventTypeLimitExceeded identifies a configured limit being exceeded.
	EventTypeLimitExceeded EventType = "limit_exceeded"
	// EventTypeCompaction identifies an accepted compaction.
	EventTypeCompaction EventType = "compaction"
	// EventTypeCommon identifies an application event.
	EventTypeCommon EventType = "common"
	// EventTypeStateChange identifies an immutable history transition.
	EventTypeStateChange EventType = "state_change"
)

// RunStatus is the materialized status of a traced run.
type RunStatus string

const (
	// RunStatusPending means no execution-start event has been observed.
	RunStatusPending RunStatus = "pending"
	// RunStatusRunning means a turn has active execution evidence.
	RunStatusRunning RunStatus = "running"
	// RunStatusPaused means the current turn has an accepted Stop or limit pause.
	RunStatusPaused RunStatus = "paused"
	// RunStatusSucceeded means a successful completed-turn result is accepted.
	RunStatusSucceeded RunStatus = "succeeded"
	// RunStatusFailed shows an observed execution failure in the trace.
	RunStatusFailed RunStatus = "failed"
	// RunStatusRefused means an accepted turn ended with a provider refusal.
	RunStatusRefused RunStatus = "refused"
	// RunStatusCanceled means a new task replaced the prior unfinished turn.
	RunStatusCanceled RunStatus = "canceled"
	// RunStatusLimitExceeded shows a limit event before its accepted pause is projected.
	RunStatusLimitExceeded RunStatus = "limit_exceeded"
)

// StepStatus is the materialized status of an execution step.
type StepStatus string

const (
	// StepStatusInterrupted means an earlier attempt ended without an accepted completion.
	StepStatusInterrupted StepStatus = "interrupted"
	// StepStatusRunning means the traced operation started and has not completed.
	StepStatusRunning StepStatus = "running"
	// StepStatusPaused means the execution context has an accepted unfinished turn.
	StepStatusPaused StepStatus = "paused"
	// StepStatusSucceeded means the traced operation completed successfully.
	StepStatusSucceeded StepStatus = "succeeded"
	// StepStatusRefused means the traced operation returned a provider refusal.
	StepStatusRefused StepStatus = "refused"
	// StepStatusFailed means the traced operation failed.
	StepStatusFailed StepStatus = "failed"
	// StepStatusCanceled means cancellation ended the traced operation.
	StepStatusCanceled StepStatus = "canceled"
)

// Event is a sequenced trace event.
type Event struct {
	TurnId              string    `json:"turnId"`
	AttemptId           string    `json:"attemptId,omitempty"`
	EventSequenceNumber uint64    `json:"eventSequenceNumber"`
	RunId               string    `json:"runId"`
	Ts                  time.Time `json:"ts"`
	Type                EventType `json:"type"`

	EventName string `json:"eventName,omitempty"`
	Iteration int    `json:"iteration,omitempty"`
	Source    string `json:"source,omitempty"`

	ContextId string `json:"contextId,omitempty"`

	ModelCallId   string `json:"modelCallId,omitempty"`
	ToolCallId    string `json:"toolCallId,omitempty"`
	StreamId      string `json:"streamId,omitempty"`
	StreamTopicId string `json:"streamTopicId,omitempty"`

	Payload any    `json:"payload,omitempty"`
	Error   *Error `json:"error,omitempty"`
}

// Snapshot is the current materialized trace state for a run.
type Snapshot struct {
	TurnId        string    `json:"turnId"`
	SchemaVersion int       `json:"schemaVersion"`
	RunId         string    `json:"runId"`
	Status        RunStatus `json:"status"`

	StartedTs     time.Time  `json:"startedTs"`
	LastUpdatedTs time.Time  `json:"lastUpdatedTs"`
	CompletedTs   *time.Time `json:"completedTs,omitempty"`

	LastEventSequenceNumber uint64 `json:"lastEventSequenceNumber"`

	Result *RunResult `json:"result,omitempty"`
	Stats  RunStats   `json:"stats"`

	Contexts   []*Context   `json:"contexts,omitempty"`
	Iterations []*Iteration `json:"iterations,omitempty"`
	ModelCalls []*ModelCall `json:"modelCalls,omitempty"`
	ToolCalls  []*ToolCall  `json:"toolCalls,omitempty"`
	Errors     []*Error     `json:"errors,omitempty"`

	RecentEvents          []*Event `json:"recentEvents,omitempty"`
	RecentLifecycleEvents []*Event `json:"recentLifecycleEvents,omitempty"`
	RecentChunkEvents     []*Event `json:"recentChunkEvents,omitempty"`
}

// RunResult contains final run output when configured.
type RunResult struct {
	TurnId            string                 `json:"turnId"`
	Refusal           *gent.ModelRefusal     `json:"refusal,omitempty"`
	TerminationReason gent.TerminationReason `json:"terminationReason"`
	Output            any                    `json:"output,omitempty"`
	Error             *Error                 `json:"error,omitempty"`
}

// Context is a materialized execution context record.
type Context struct {
	TurnId           string             `json:"turnId"`
	Content          []gent.ContentPart `json:"content,omitempty"`
	MessageId        string             `json:"messageId,omitempty"`
	SourceMessageIds []string           `json:"sourceMessageIds,omitempty"`
	ContentTruncated bool               `json:"contentTruncated,omitempty"`

	Id     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Source string `json:"source,omitempty"`

	Status StepStatus `json:"status"`

	StartedTs     time.Time  `json:"startedTs"`
	LastUpdatedTs time.Time  `json:"lastUpdatedTs"`
	CompletedTs   *time.Time `json:"completedTs,omitempty"`
	DurationMs    int64      `json:"durationMs,omitempty"`

	StartedEventSequenceNumber   uint64 `json:"startedEventSequenceNumber"`
	LastEventSequenceNumber      uint64 `json:"lastEventSequenceNumber"`
	CompletedEventSequenceNumber uint64 `json:"completedEventSequenceNumber,omitempty"`

	Error *Error `json:"error,omitempty"`
}

// Iteration is a materialized agent-loop iteration record.
type Iteration struct {
	TurnId    string `json:"turnId"`
	AttemptId string `json:"attemptId"`
	Iteration int    `json:"iteration"`
	Source    string `json:"source,omitempty"`

	ContextId string `json:"contextId"`

	Status StepStatus `json:"status"`

	StartedTs     time.Time  `json:"startedTs"`
	LastUpdatedTs time.Time  `json:"lastUpdatedTs"`
	CompletedTs   *time.Time `json:"completedTs,omitempty"`
	DurationMs    int64      `json:"durationMs,omitempty"`

	StartedEventSequenceNumber   uint64 `json:"startedEventSequenceNumber"`
	LastEventSequenceNumber      uint64 `json:"lastEventSequenceNumber"`
	CompletedEventSequenceNumber uint64 `json:"completedEventSequenceNumber,omitempty"`

	ModelCallIds []string `json:"modelCallIds,omitempty"`
	ToolCallIds  []string `json:"toolCallIds,omitempty"`

	Result any    `json:"result,omitempty"`
	Error  *Error `json:"error,omitempty"`
}

// ModelCall is a materialized model call record.
type ModelCall struct {
	TurnId        string `json:"turnId"`
	AttemptId     string `json:"attemptId"`
	Id            string `json:"id"`
	Model         string `json:"model"`
	Provider      string `json:"provider,omitempty"`
	StreamId      string `json:"streamId,omitempty"`
	StreamTopicId string `json:"streamTopicId,omitempty"`
	Source        string `json:"source,omitempty"`
	Iteration     int    `json:"iteration,omitempty"`

	ContextId string `json:"contextId"`

	Status StepStatus `json:"status"`

	StartedTs     time.Time  `json:"startedTs"`
	LastUpdatedTs time.Time  `json:"lastUpdatedTs"`
	CompletedTs   *time.Time `json:"completedTs,omitempty"`
	DurationMs    int64      `json:"durationMs,omitempty"`

	StartedEventSequenceNumber   uint64 `json:"startedEventSequenceNumber"`
	LastEventSequenceNumber      uint64 `json:"lastEventSequenceNumber"`
	CompletedEventSequenceNumber uint64 `json:"completedEventSequenceNumber,omitempty"`

	Request  any          `json:"request,omitempty"`
	Response any          `json:"response,omitempty"`
	Usage    *ModelUsage  `json:"usage,omitempty"`
	Stream   *ModelStream `json:"stream,omitempty"`
	Error    *Error       `json:"error,omitempty"`
}

// ModelStream is the materialized stream state for a model call.
type ModelStream struct {
	Content                      []gent.ContentPart `json:"content,omitempty"`
	ContentTruncated             bool               `json:"contentTruncated,omitempty"`
	ChunkCount                   int                `json:"chunkCount"`
	LastChunkEventSequenceNumber uint64             `json:"lastChunkEventSequenceNumber,omitempty"`
}

// ModelUsage contains normalized token usage.
type ModelUsage struct {
	InputTokens       int `json:"inputTokens"`
	OutputTokens      int `json:"outputTokens"`
	ReasoningTokens   int `json:"reasoningTokens,omitempty"`
	CachedInputTokens int `json:"cachedInputTokens,omitempty"`
	TotalTokens       int `json:"totalTokens"`
}

// ToolCall is a materialized tool call record.
type ToolCall struct {
	TurnId    string `json:"turnId"`
	AttemptId string `json:"attemptId"`
	Id        string `json:"id"`
	Name      string `json:"name"`
	Source    string `json:"source,omitempty"`
	Iteration int    `json:"iteration,omitempty"`

	ContextId string `json:"contextId"`

	Status StepStatus `json:"status"`

	StartedTs     time.Time  `json:"startedTs"`
	LastUpdatedTs time.Time  `json:"lastUpdatedTs"`
	CompletedTs   *time.Time `json:"completedTs,omitempty"`
	DurationMs    int64      `json:"durationMs,omitempty"`

	StartedEventSequenceNumber   uint64 `json:"startedEventSequenceNumber"`
	LastEventSequenceNumber      uint64 `json:"lastEventSequenceNumber"`
	CompletedEventSequenceNumber uint64 `json:"completedEventSequenceNumber,omitempty"`

	Args   any    `json:"args,omitempty"`
	Output any    `json:"output,omitempty"`
	Error  *Error `json:"error,omitempty"`
}

// Error is a redacted, JSON-safe error representation.
type Error struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`

	EventSequenceNumber uint64    `json:"eventSequenceNumber,omitempty"`
	Ts                  time.Time `json:"ts,omitempty"`
	EventName           string    `json:"eventName,omitempty"`
	Source              string    `json:"source,omitempty"`
	Iteration           int       `json:"iteration,omitempty"`

	ContextId string `json:"contextId,omitempty"`

	ModelCallId string `json:"modelCallId,omitempty"`
	ToolCallId  string `json:"toolCallId,omitempty"`
}

// RunStats contains counts and durations derived from sequenced trace events.
type RunStats struct {
	ContextCount   int `json:"contextCount"`
	IterationCount int `json:"iterationCount"`
	ModelCallCount int `json:"modelCallCount"`
	ToolCallCount  int `json:"toolCallCount"`

	InputTokens       int `json:"inputTokens"`
	OutputTokens      int `json:"outputTokens"`
	ReasoningTokens   int `json:"reasoningTokens"`
	CachedInputTokens int `json:"cachedInputTokens"`
	TotalTokens       int `json:"totalTokens"`

	DurationMs      int64 `json:"durationMs"`
	ModelDurationMs int64 `json:"modelDurationMs"`
	ToolDurationMs  int64 `json:"toolDurationMs"`

	ParseErrorCount      int `json:"parseErrorCount"`
	OutputRejectionCount int `json:"outputRejectionCount"`
	ErrorCount           int `json:"errorCount"`
	LimitExceededCount   int `json:"limitExceededCount"`
	CompactionCount      int `json:"compactionCount"`
}

// PayloadCaptureError replaces payloads that cannot be made JSON-safe.
type PayloadCaptureError struct {
	Type  string `json:"type"`
	Error string `json:"error"`
}
