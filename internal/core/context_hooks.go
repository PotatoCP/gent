package gent

import "fmt"

func (execCtx *ExecutionContext) publishConfigured(event Event, iteration *int, validate func() error) {
	if execCtx.RecordingError() != nil {
		return
	}
	execCtx.mu.Lock()
	execCtx.populateBaseEvent(event, execCtx.name)
	if iteration != nil {
		eventBase(event).Iteration = *iteration
	}
	execCtx.mu.Unlock()
	execCtx.updateStatsForEvent(event)
	if err := execCtx.applyHook(event); err != nil {
		execCtx.stopRecording(err)
		return
	}
	if validate != nil {
		if err := validate(); err != nil {
			execCtx.stopRecording(err)
			return
		}
	}
	// RecordedEvent owns the frozen evidence. Do not also accumulate an unbounded Events list
	// on the live context: full historical inspection belongs in the separately replayed RunView.
	execCtx.recordEvent(event)
}

func (execCtx *ExecutionContext) applyHook(event Event) error {
	hooks := execCtx.config.Hooks
	if hooks == nil {
		return nil
	}
	var base BaseEvent
	var stage, callId string
	var err error
	switch event := event.(type) {
	case *BeforeModelCallEvent:
		if hooks.BeforeModelCall == nil {
			return nil
		}
		base = event.BaseEvent
		stage, callId = "BeforeModelCall", event.ModelCallId
		err = hooks.BeforeModelCall(execCtx.Context(), event)
		if err == nil && event.ModelCallId != callId {
			err = fmt.Errorf("hook changed ModelCallId from %q to %q", callId, event.ModelCallId)
		}
	case *BeforeToolCallEvent:
		if hooks.BeforeToolCall == nil {
			return nil
		}
		base = event.BaseEvent
		stage, callId = "BeforeToolCall", event.ToolCallId
		err = hooks.BeforeToolCall(execCtx.Context(), event)
		if err == nil && event.ToolCallId != callId {
			err = fmt.Errorf("hook changed ToolCallId from %q to %q", callId, event.ToolCallId)
		}
	default:
		return nil
	}
	if err == nil && *eventBase(event) != base {
		err = fmt.Errorf("hook changed event identity or execution metadata")
	}
	if err != nil {
		return fmt.Errorf("gent: Hooks.%s in run %q turn %q context %q call %q: %w", stage,
			execCtx.RunId(), execCtx.TurnId(), execCtx.ContextId(), callId, err)
	}
	return nil
}
