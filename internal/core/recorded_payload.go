package gent

import (
	"fmt"
	"reflect"
)

func recordedHistoryChange(event *RecordedEvent) *HistoryChange {
	change, _ := event.Payload.(*HistoryChange)
	return change
}

func validateRecordedPayload(event *RecordedEvent) error {
	var expected any
	switch event.Type {
	case "state_change":
		expected = (*HistoryChange)(nil)
	case "execution_started":
		expected = (*BeforeExecutionEvent)(nil)
	case "execution_finished":
		expected = (*AfterExecutionEvent)(nil)
	case "execution_paused":
		expected = (*ExecutionResult)(nil)
	case "iteration_started":
		expected = (*BeforeIterationEvent)(nil)
	case "iteration_finished":
		expected = (*AfterIterationEvent)(nil)
	case "model_call_started":
		expected = (*BeforeModelCallEvent)(nil)
	case "model_call_finished":
		expected = (*AfterModelCallEvent)(nil)
	case "tool_call_started":
		expected = (*BeforeToolCallEvent)(nil)
	case "tool_call_finished":
		expected = (*AfterToolCallEvent)(nil)
	case "model_stream_chunk", "accepted_answer":
		expected = (*StreamChunk)(nil)
	case "parse_error":
		expected = (*ParseErrorEvent)(nil)
	case "output_validation":
		expected = (*OutputValidationEvent)(nil)
	case "error":
		expected = (*ErrorEvent)(nil)
	case "limit_exceeded":
		expected = (*LimitExceededEvent)(nil)
	case "compaction":
		expected = (*CompactionEvent)(nil)
	case "common":
		expected = (*CommonEvent)(nil)
	case "attempt_interrupted":
		expected = (*SavedError)(nil)
	case "checkpoint", "attempt_started":
		if event.Payload != nil {
			return fmt.Errorf("gent: structural event %q must have no payload", event.Type)
		}
		return nil
	default:
		// Type remains open for application event kinds. Their registered codec owns the payload.
		return nil
	}
	if reflect.TypeOf(event.Payload) != reflect.TypeOf(expected) ||
		reflect.ValueOf(event.Payload).IsNil() {
		return fmt.Errorf("gent: event %q requires non-nil %T, received %T",
			event.Type, expected, event.Payload)
	}
	return nil
}
