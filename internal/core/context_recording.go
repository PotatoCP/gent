package gent

import (
	"fmt"
	"reflect"
)

func registerFrameworkCodecs(registry *CodecRegistry) {
	values := []any{
		BaseEvent{},
		BeforeExecutionEvent{},
		AfterExecutionEvent{},
		BeforeIterationEvent{},
		AfterIterationEvent{},
		BeforeModelCallEvent{},
		AfterModelCallEvent{},
		BeforeToolCallEvent{},
		AfterToolCallEvent{},
		ParseErrorEvent{},
		OutputValidationEvent{},
		OutputValidationStage(""),
		ErrorEvent{},
		LimitExceededEvent{},
		CompactionEvent{},
		CommonEvent{},
		ModelCallRequest{},
		AgentLoopResult{},
		ExecutionResult{}, ExecutionStatus(""), LimitExceeded{},
		Limit{},
		StatKey(""),
		LimitType(""),
		LimitScope(""),
		ParseErrorType(""),
		TerminationReason(""),
		LoopAction(""),
	}
	values = append(values, PlannedToolCall{}, SavedToolCall{}, ToolPlan{}, EncodedValue{})
	values = append(values, HistoryRevision(0), EntryId(""))
	for _, value := range values {
		typ := reflect.TypeOf(value)
		registry.add(registeredValue{id: valueTypeId(typ), version: 1, typ: typ})
	}
	chunkCodec := streamChunkCodec{registry: registry}
	registry.add(registeredValue{id: chunkCodec.Type(), version: chunkCodec.Version(),
		typ: reflect.TypeFor[StreamChunk](), codec: chunkCodec})
}

func recordedEventType(event Event) string {
	switch event.(type) {
	case *BeforeExecutionEvent:
		return "execution_started"
	case *AfterExecutionEvent:
		return "execution_finished"
	case *BeforeIterationEvent:
		return "iteration_started"
	case *AfterIterationEvent:
		return "iteration_finished"
	case *BeforeModelCallEvent:
		return "model_call_started"
	case *AfterModelCallEvent:
		return "model_call_finished"
	case *BeforeToolCallEvent:
		return "tool_call_started"
	case *AfterToolCallEvent:
		return "tool_call_finished"
	case *ParseErrorEvent:
		return "parse_error"
	case *OutputValidationEvent:
		return "output_validation"
	case *ErrorEvent:
		return "error"
	case *LimitExceededEvent:
		return "limit_exceeded"
	case *CompactionEvent:
		return "compaction"
	case *CommonEvent:
		return "common"
	default:
		return "custom"
	}
}

func (execCtx *ExecutionContext) recordEvent(event Event) error {
	if err := execCtx.RecordingError(); err != nil {
		return err
	}
	stored, err := execCtx.snapshotToolEvent(event)
	if err != nil {
		return execCtx.stopRecording(err)
	}
	value, err := execCtx.run.codecs.Clone(stored)
	if err != nil {
		return execCtx.stopRecording(fmt.Errorf("gent: record %T: %w", event, err))
	}
	base := eventBase(event)
	recorded := &RecordedEvent{Type: recordedEventType(event), ContextId: execCtx.ContextId(),
		Iteration: execCtx.Iteration(), AttemptId: execCtx.AttemptId(), Payload: value}
	if base != nil {
		if base.AttemptId != "" {
			recorded.AttemptId = base.AttemptId
		}
		recorded.EventName = base.EventName
		recorded.ContextId = base.ContextId
		recorded.Iteration = base.Iteration
		recorded.Ts = base.Timestamp
	}
	switch e := event.(type) {
	case *BeforeModelCallEvent:
		recorded.ModelCallId = e.ModelCallId
	case *AfterModelCallEvent:
		recorded.ModelCallId = e.ModelCallId
	case *BeforeToolCallEvent:
		recorded.ToolCallId = e.ToolCallId
	case *AfterToolCallEvent:
		recorded.ToolCallId = e.ToolCallId
	}
	execCtx.run.mu.Lock()
	if recorded.Type == "execution_finished" {
		// Assign this event's sequence only inside its terminal save group. Other contexts can
		// still save progress while completion hooks finish, without exposing an unsaved outcome.
		err = execCtx.run.err
		if err == nil {
			execCtx.terminalEvent = recorded
		}
	} else {
		err = execCtx.run.queueEvent(recorded)
	}
	execCtx.run.mu.Unlock()
	if err != nil {
		return execCtx.stopRecording(err)
	}
	return execCtx.flushPendingLimit()
}

// Live hooks keep their typed input. Saved event arguments are opaque snapshots, so reading
// completed history never needs an old tool registration or input type on the next worker.
func (execCtx *ExecutionContext) snapshotToolEvent(event Event) (Event, error) {
	snapshot := func(value any) (*EncodedValue, error) {
		if encoded, ok := value.(*EncodedValue); ok {
			return encoded, nil
		}
		return execCtx.SnapshotDiagnostic(value)
	}
	switch event := event.(type) {
	case *BeforeToolCallEvent:
		copy := *event
		value, err := snapshot(event.Args)
		copy.Args = value
		return &copy, err
	case *AfterToolCallEvent:
		copy := *event
		value, err := snapshot(event.Args)
		copy.Args = value
		return &copy, err
	default:
		return event, nil
	}
}

func (execCtx *ExecutionContext) recordChunk(chunk *StreamChunk, call *BeforeModelCallEvent) error {
	value, err := cloneOwned(execCtx.run.codecs, chunk)
	if err != nil {
		return execCtx.stopRecording(err)
	}
	recorded := &RecordedEvent{Type: "model_stream_chunk", ContextId: execCtx.ContextId(),
		Iteration: execCtx.Iteration(), Payload: value}
	if call != nil {
		recorded.ContextId, recorded.Iteration = call.ContextId, call.Iteration
		recorded.ModelCallId = call.ModelCallId
	}
	execCtx.run.mu.Lock()
	recorded.AttemptId = execCtx.run.attempt
	err = execCtx.run.queueEvent(recorded)
	execCtx.run.mu.Unlock()
	if err != nil {
		return execCtx.stopRecording(err)
	}
	return nil
}

func (execCtx *ExecutionContext) DecodeEvent(event *RecordedEvent) (any, error) {
	return execCtx.run.codecs.DecodeEvent(event)
}

// DecodeEvent reads a frozen event using the run's registered content types.
func (registry *CodecRegistry) DecodeEvent(event *RecordedEvent) (any, error) {
	if event == nil {
		return nil, fmt.Errorf("gent: nil recorded event")
	}
	value, err := registry.Clone(event.Payload)
	switch value := value.(type) {
	case *StreamChunk:
		return *value, err
	case *HistoryChange:
		return *value, err
	default:
		return value, err
	}
}
