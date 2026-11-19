package trace

import (
	"fmt"

	"github.com/rickchristie/gent"
)

// project decodes one accepted payload directly into its display representation. Hook objects
// are never dispatched or rewritten during replay; the accepted envelope owns identity/order.
func (s *Sequencer) project(
	recorded *gent.RecordedEvent, registry *gent.CodecRegistry,
) (*Event, error) {
	if recorded == nil {
		return nil, fmt.Errorf("trace: nil recorded event")
	}
	if recorded.RunId != s.runId {
		return nil, gent.ErrWrongRun
	}
	value, err := registry.Clone(recorded.Payload)
	if err != nil {
		return nil, err
	}
	event := &Event{
		EventSequenceNumber: recorded.EventSequenceNumber,
		RunId:               recorded.RunId,
		Ts:                  recorded.Ts,
		Type:                EventType(recorded.Type),
		EventName:           recorded.EventName,
		ContextId:           recorded.ContextId,
		Iteration:           recorded.Iteration,
		TurnId:              recorded.TurnId,
		AttemptId:           recorded.AttemptId,
		ModelCallId:         recorded.ModelCallId,
		ToolCallId:          recorded.ToolCallId,
	}
	if withBase, ok := value.(interface{ EventBase() *gent.BaseEvent }); ok {
		base := withBase.EventBase()
		event.Source = base.Source
	}
	payload := map[string]any{}
	event.Payload = payload
	switch saved := value.(type) {
	case *gent.BeforeExecutionEvent:
		event.Type = EventTypeExecutionStarted
		payload["name"] = saved.Name
	case *gent.AfterExecutionEvent:
		event.Type = EventTypeExecutionFinished
		event.Error = redactError(s.cfg.redactor, saved.Error)
		result := &RunResult{TurnId: event.TurnId, TerminationReason: saved.TerminationReason,
			Error: event.Error}
		if s.cfg.IncludeRunOutput {
			result.Output = jsonSafeValue(s.cfg.redactor.RedactRunOutput(saved.Output))
		}
		if saved.Refusal != nil {
			result.Refusal = &gent.ModelRefusal{Code: saved.Refusal.Code}
			if s.cfg.IncludeRunOutput {
				result.Refusal.Content = redactedContent(s.cfg.redactor, saved.Refusal.Content)
			}
		}
		payload["terminationReason"], payload["result"] = saved.TerminationReason, result
		payload["status"] = runStatus(saved.TerminationReason, event.Error)
		payload["durationMs"] = saved.Duration.Milliseconds()
	case *gent.ExecutionResult:
		payload["result"] = jsonSafeValue(saved)
		payload["terminationReason"] = saved.TerminationReason
	case *gent.BeforeIterationEvent:
		event.Payload = nil
	case *gent.AfterIterationEvent:
		payload["durationMs"] = saved.Duration.Milliseconds()
		event.Error = redactError(s.cfg.redactor, saved.Error)
		if saved.Result != nil {
			payload["action"] = saved.Result.Action
			if s.cfg.IncludeRunOutput && len(saved.Result.Result) > 0 {
				payload["result"] = jsonSafeValue(s.cfg.redactor.RedactRunOutput(saved.Result.Result))
			}
		}
	case *gent.BeforeModelCallEvent:
		event.StreamId, event.StreamTopicId = saved.StreamId, saved.StreamTopicId
		payload["model"], payload["provider"] = saved.Model, saved.Provider
		payload["streamId"], payload["streamTopicId"] = saved.StreamId, saved.StreamTopicId
		if s.cfg.IncludeModelRequests {
			payload["request"] = jsonSafeValue(s.cfg.redactor.RedactModelRequest(saved.Request))
		}
	case *gent.AfterModelCallEvent:
		event.StreamId, event.StreamTopicId = saved.StreamId, saved.StreamTopicId
		event.Error = redactError(s.cfg.redactor, saved.Error)
		payload["model"], payload["provider"] = saved.Model, saved.Provider
		payload["streamId"], payload["streamTopicId"] = saved.StreamId, saved.StreamTopicId
		payload["durationMs"] = saved.Duration.Milliseconds()
		payload["usage"] = modelUsage(saved.Response, saved.InputTokens, saved.OutputTokens)
		if s.cfg.IncludeModelResponses {
			payload["response"] = jsonSafeValue(s.cfg.redactor.RedactModelResponse(saved.Response))
		}
	case *gent.BeforeToolCallEvent:
		payload["name"] = saved.ToolName
		if s.cfg.IncludeToolArgs {
			payload["args"] = jsonSafeValue(s.cfg.redactor.RedactToolArgs(saved.Args))
		}
	case *gent.AfterToolCallEvent:
		event.Error = redactError(s.cfg.redactor, saved.Error)
		payload["name"], payload["durationMs"] = saved.ToolName, saved.Duration.Milliseconds()
		if s.cfg.IncludeToolOutput {
			payload["output"] = jsonSafeValue(s.cfg.redactor.RedactToolOutput(saved.Output))
		}
	case *gent.ParseErrorEvent:
		event.Error = redactError(s.cfg.redactor, saved.Error)
		payload["errorType"] = saved.ErrorType
	case *gent.OutputValidationEvent:
		payload["name"], payload["stage"], payload["accepted"] = saved.Name, saved.Stage, saved.Accepted
		payload["steeringCount"] = len(saved.Steering)
		event.Error = redactError(s.cfg.redactor, saved.Error)
	case *gent.ErrorEvent:
		event.Error, event.Payload = redactError(s.cfg.redactor, saved.Error), nil
	case *gent.LimitExceededEvent:
		err := fmt.Errorf("limit exceeded: %s = %v", saved.MatchedKey, saved.CurrentValue)
		event.Error = redactError(s.cfg.redactor, err)
		payload["limit"], payload["currentValue"] = saved.Limit, saved.CurrentValue
		payload["matchedKey"] = saved.MatchedKey
	case *gent.CompactionEvent:
		payload["scratchpadLengthBefore"] = saved.ScratchpadLengthBefore
		payload["scratchpadLengthAfter"] = saved.ScratchpadLengthAfter
		payload["durationMs"] = saved.Duration.Milliseconds()
	case *gent.CommonEvent:
		payload["description"] = saved.Description
		if s.cfg.IncludeCommonPayload {
			payload["data"] = jsonSafeValue(s.cfg.redactor.RedactCommonPayload(saved.EventName, saved.Data))
		}

	case *gent.HistoryChange:
		if s.cfg.IncludeStateChanges {
			payload["data"] = jsonSafeValue(s.cfg.redactor.RedactStateChange(recorded.EventName, *saved))
		}
	case *gent.StreamChunk:
		// Display redaction can hide or replace every part. Keep the accepted item's identity
		// separate from display content so a UI can still replace its linked previews.
		if recorded.Type == "accepted_answer" {
			for _, part := range saved.Content {
				if item, ok := part.(*gent.AssistantContent); ok {
					payload["messageId"] = item.MessageId
					break
				}
			}
		}
		chunk := s.cfg.redactor.RedactChunk(*saved)
		if call := s.modelById[callKey(recorded.ModelCallId, recorded.AttemptId)]; call != nil {
			event.StreamId, event.StreamTopicId = call.StreamId, call.StreamTopicId
			event.Source = call.Source
		}
		payload["streamId"], payload["streamTopicId"] = event.StreamId, event.StreamTopicId
		payload["sourceMessageIds"] = append([]string(nil), chunk.SourceMessageIds...)
		if recorded.Type == "accepted_answer" {
			if s.cfg.IncludeRunOutput {
				payload["content"] = redactedContent(s.cfg.redactor, chunk.Content)
			}
		} else if s.cfg.IncludeChunkText {
			payload["content"] = chunk.Content
		}

	default:
		event.Payload = nil
	}
	return event, nil
}

func (s *Sequencer) applyEvent(event *Event) {
	switch event.Type {
	case EventTypeExecutionStarted:
		s.applyContextStarted(event)
		s.applyExecutionStarted(event)
	case EventTypeExecutionFinished:
		s.applyContextFinished(event)
		s.applyExecutionFinished(event)
	case EventTypeExecutionPaused:
		s.applyExecutionPaused(event)
	case EventTypeIterationStarted:
		s.applyIterationStarted(event)
	case EventTypeIterationFinished:
		s.applyIterationFinished(event)
	case EventTypeModelCallStarted:
		s.applyModelCallStarted(event)
	case EventTypeModelCallFinished:
		s.applyModelCallFinished(event)
	case EventTypeAcceptedAnswer:
		s.applyAcceptedAnswer(event)
	case EventTypeModelStreamChunk:
		s.applyModelChunk(event)
	case EventTypeToolCallStarted:
		s.applyToolCallStarted(event)
	case EventTypeToolCallFinished:
		s.applyToolCallFinished(event)
	case EventTypeParseError:
		s.snapshot.Stats.ParseErrorCount++
		s.appendError(event.Error)
	case EventTypeOutputValidation:
		payload, _ := event.Payload.(map[string]any)
		if accepted, _ := payload["accepted"].(bool); !accepted && event.Error == nil &&
			payload["stage"] == gent.OutputValidationProcess {
			s.snapshot.Stats.OutputRejectionCount++
		}
	case EventTypeError:
		s.snapshot.Stats.ErrorCount++
		s.appendError(event.Error)
	case EventTypeLimitExceeded:
		s.snapshot.Status = RunStatusLimitExceeded
		s.snapshot.Stats.LimitExceededCount++
		s.appendError(event.Error)
	case EventTypeCompaction:
		s.snapshot.Stats.CompactionCount++
	default:
		s.applyAttempt(event)
	}
}

func modelUsage(response *gent.ModelCallResponse, inputTokens int, outputTokens int) *ModelUsage {
	usage := &ModelUsage{InputTokens: inputTokens, OutputTokens: outputTokens}
	if response != nil && response.Info != nil {
		usage.InputTokens = response.Info.InputTokens
		usage.OutputTokens = response.Info.OutputTokens
		usage.CachedInputTokens = response.Info.CachedInputTokens
		usage.ReasoningTokens = response.Info.ReasoningTokens
		usage.TotalTokens = response.Info.TotalTokens
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	return usage
}

func intFromAny(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

// A redactor can replace output with a scalar or structured marker. Only explicit content parts
// or text are delivered as content; other replacements omit the original to prevent disclosure.
func redactedContent(redactor Redactor, content []gent.ContentPart) []gent.ContentPart {
	value := redactor.RedactRunOutput(content)
	switch output := value.(type) {
	case string:
		return []gent.ContentPart{gent.TextContent{Text: output}}
	case []gent.ContentPart:
		return gent.CloneContentParts(output)
	default:
		return nil
	}
}
