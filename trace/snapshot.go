package trace

import (
	"fmt"
	"unicode/utf8"

	"github.com/rickchristie/gent"
)

func (s *Sequencer) applyContextStarted(event *Event) {
	ctx := s.contextById[event.ContextId]
	if ctx == nil {
		ctx = &Context{
			TurnId:                     event.TurnId,
			Id:                         event.ContextId,
			Source:                     event.Source,
			StartedTs:                  event.Ts,
			StartedEventSequenceNumber: event.EventSequenceNumber,
			Status:                     StepStatusRunning,
		}
		s.contextById[event.ContextId] = ctx
		s.snapshot.Contexts = append(s.snapshot.Contexts, ctx)
		s.snapshot.Stats.ContextCount++
	}
	payload, _ := event.Payload.(map[string]any)
	if ctx.TurnId != event.TurnId {
		ctx.Content, ctx.SourceMessageIds, ctx.MessageId = nil, nil, ""
		ctx.ContentTruncated = false
	}
	if name, ok := payload["name"].(string); ok {
		ctx.Name = name
	}
	ctx.TurnId = event.TurnId
	ctx.Status, ctx.CompletedTs, ctx.Error = StepStatusRunning, nil, nil
	ctx.CompletedEventSequenceNumber, ctx.DurationMs = 0, 0
	ctx.LastUpdatedTs = event.Ts
	ctx.LastEventSequenceNumber = event.EventSequenceNumber
	if s.snapshot.Status == RunStatusPending {
		s.snapshot.Status = RunStatusRunning
	}
}

func (s *Sequencer) applyContextFinished(event *Event) {
	ctx := s.contextById[event.ContextId]
	if ctx == nil {
		s.applyContextStarted(event)
		ctx = s.contextById[event.ContextId]
	}
	payload, _ := event.Payload.(map[string]any)
	ctx.Status = contextStatusFromPayload(payload, event.Error)
	ctx.LastUpdatedTs = event.Ts
	ctx.LastEventSequenceNumber = event.EventSequenceNumber
	ctx.CompletedEventSequenceNumber = event.EventSequenceNumber
	ctx.CompletedTs = &event.Ts
	if durationMs, ok := payload["durationMs"].(int64); ok {
		ctx.DurationMs = durationMs
	}
	if event.Error != nil {
		ctx.Error = cloneError(event.Error)
		return
	}
	// A successful completion clears any earlier display error.
	if ctx.Status == StepStatusSucceeded {
		ctx.Error = nil
	}
}

func (s *Sequencer) applyExecutionStarted(event *Event) {
	s.snapshot.TurnId = event.TurnId
	s.snapshot.CompletedTs = nil
	s.snapshot.Status = RunStatusRunning
	s.snapshot.StartedTs = event.Ts
}

func (s *Sequencer) applyExecutionFinished(event *Event) {
	payload, _ := event.Payload.(map[string]any)
	status, _ := payload["status"].(RunStatus)
	if status == "" {
		if event.Error != nil {
			status = RunStatusFailed
		} else {
			status = RunStatusSucceeded
		}
	}
	s.snapshot.Status = status
	s.snapshot.CompletedTs = &event.Ts
	s.snapshot.Stats.DurationMs = event.Ts.Sub(s.snapshot.StartedTs).Milliseconds()
	if result, ok := payload["result"].(*RunResult); ok {
		s.snapshot.Result = cloneRunResult(result)
	}
	if event.Error != nil {
		s.appendError(event.Error)
	}
}

// A pause is saved execution control, not a completed answer. Keep the previous completed
// result and mark only the current attempt's open operations as interrupted.
func (s *Sequencer) applyExecutionPaused(event *Event) {
	s.snapshot.Status, s.snapshot.CompletedTs = RunStatusPaused, nil
	if ctx := s.contextById[event.ContextId]; ctx != nil {
		ctx.Status, ctx.CompletedTs = StepStatusPaused, nil
		ctx.LastUpdatedTs, ctx.LastEventSequenceNumber = event.Ts, event.EventSequenceNumber
	}
	interrupted := *event
	interrupted.Type = EventTypeAttemptInterrupted
	s.applyAttempt(&interrupted)
}

func (s *Sequencer) applyIterationStarted(event *Event) {
	key := iterationKey(event.ContextId, event.Iteration, event.AttemptId)
	iteration := &Iteration{
		TurnId:                     event.TurnId,
		AttemptId:                  event.AttemptId,
		Iteration:                  event.Iteration,
		Source:                     event.Source,
		ContextId:                  event.ContextId,
		Status:                     StepStatusRunning,
		StartedTs:                  event.Ts,
		LastUpdatedTs:              event.Ts,
		StartedEventSequenceNumber: event.EventSequenceNumber,
		LastEventSequenceNumber:    event.EventSequenceNumber,
	}
	s.iterationByKey[key] = iteration
	s.snapshot.Iterations = append(s.snapshot.Iterations, iteration)
	s.snapshot.Stats.IterationCount++
}

func (s *Sequencer) applyIterationFinished(event *Event) {
	iteration := s.iterationByKey[iterationKey(event.ContextId, event.Iteration, event.AttemptId)]
	if iteration == nil {
		return
	}
	iteration.Status = statusFromError(event.Error)
	iteration.LastUpdatedTs = event.Ts
	iteration.CompletedTs = &event.Ts
	iteration.DurationMs = durationMsFromPayload(event.Payload)
	iteration.LastEventSequenceNumber = event.EventSequenceNumber
	iteration.CompletedEventSequenceNumber = event.EventSequenceNumber
	iteration.Error = cloneError(event.Error)
}

func (s *Sequencer) applyModelCallStarted(event *Event) {
	if event.ModelCallId == "" {
		return
	}
	payload, _ := event.Payload.(map[string]any)
	call := &ModelCall{
		TurnId:                     event.TurnId,
		AttemptId:                  event.AttemptId,
		Id:                         event.ModelCallId,
		Model:                      stringFromPayload(payload, "model"),
		Provider:                   stringFromPayload(payload, "provider"),
		StreamId:                   event.StreamId,
		StreamTopicId:              event.StreamTopicId,
		Source:                     event.Source,
		Iteration:                  event.Iteration,
		ContextId:                  event.ContextId,
		Status:                     StepStatusRunning,
		StartedTs:                  event.Ts,
		LastUpdatedTs:              event.Ts,
		StartedEventSequenceNumber: event.EventSequenceNumber,
		LastEventSequenceNumber:    event.EventSequenceNumber,
	}
	if request, ok := payload["request"]; ok {
		call.Request = request
	}
	s.modelById[callKey(event.ModelCallId, event.AttemptId)] = call
	s.snapshot.ModelCalls = append(s.snapshot.ModelCalls, call)
	s.snapshot.Stats.ModelCallCount++
	s.attachModelToIteration(event, event.ModelCallId)
}

func (s *Sequencer) applyModelCallFinished(event *Event) {
	payload, _ := event.Payload.(map[string]any)
	usage := modelUsageFromPayload(payload)
	s.addUsage(usage)
	s.snapshot.Stats.ModelDurationMs += durationMsFromPayload(event.Payload)

	call := s.modelById[callKey(event.ModelCallId, event.AttemptId)]
	if call == nil {
		if event.Error != nil {
			s.appendError(event.Error)
		}
		return
	}
	call.Status = statusFromError(event.Error)
	call.LastUpdatedTs = event.Ts
	call.CompletedTs = &event.Ts
	call.DurationMs = durationMsFromPayload(event.Payload)
	call.LastEventSequenceNumber = event.EventSequenceNumber
	call.CompletedEventSequenceNumber = event.EventSequenceNumber
	call.Usage = usage
	if response, ok := payload["response"]; ok {
		call.Response = response
	}
	call.Error = cloneError(event.Error)
	if event.Error != nil {
		s.appendError(event.Error)
	}
}

func (s *Sequencer) applyModelChunk(event *Event) {
	call := s.modelById[callKey(event.ModelCallId, event.AttemptId)]
	if call == nil {
		if event.Error != nil {
			s.appendError(event.Error)
		}
		return
	}
	if call.Stream == nil {
		call.Stream = &ModelStream{}
	}
	call.Stream.ChunkCount++
	call.Stream.LastChunkEventSequenceNumber = event.EventSequenceNumber
	call.LastUpdatedTs = event.Ts
	call.LastEventSequenceNumber = event.EventSequenceNumber
	payload, _ := event.Payload.(map[string]any)
	if items, ok := payload["content"].([]gent.ContentPart); ok {
		for _, item := range items {
			s.appendStreamPart(call.Stream, item)
		}
	}
	if event.Error != nil {
		call.Error = cloneError(event.Error)
		s.appendError(event.Error)
	}
}

func (s *Sequencer) applyToolCallStarted(event *Event) {
	if event.ToolCallId == "" {
		return
	}
	payload, _ := event.Payload.(map[string]any)
	call := &ToolCall{
		TurnId:                     event.TurnId,
		AttemptId:                  event.AttemptId,
		Id:                         event.ToolCallId,
		Name:                       stringFromPayload(payload, "name"),
		Source:                     event.Source,
		Iteration:                  event.Iteration,
		ContextId:                  event.ContextId,
		Status:                     StepStatusRunning,
		StartedTs:                  event.Ts,
		LastUpdatedTs:              event.Ts,
		StartedEventSequenceNumber: event.EventSequenceNumber,
		LastEventSequenceNumber:    event.EventSequenceNumber,
	}
	if args, ok := payload["args"]; ok {
		call.Args = args
	}
	s.toolById[callKey(event.ToolCallId, event.AttemptId)] = call
	s.snapshot.ToolCalls = append(s.snapshot.ToolCalls, call)
	s.snapshot.Stats.ToolCallCount++
	s.attachToolToIteration(event, event.ToolCallId)
}

func (s *Sequencer) applyToolCallFinished(event *Event) {
	s.snapshot.Stats.ToolDurationMs += durationMsFromPayload(event.Payload)
	call := s.toolById[callKey(event.ToolCallId, event.AttemptId)]
	if call == nil {
		if event.Error != nil {
			s.appendError(event.Error)
		}
		return
	}
	payload, _ := event.Payload.(map[string]any)
	call.Status = statusFromError(event.Error)
	call.LastUpdatedTs = event.Ts
	call.CompletedTs = &event.Ts
	call.DurationMs = durationMsFromPayload(event.Payload)
	call.LastEventSequenceNumber = event.EventSequenceNumber
	call.CompletedEventSequenceNumber = event.EventSequenceNumber
	if output, ok := payload["output"]; ok {
		call.Output = output
	}
	call.Error = cloneError(event.Error)
	if event.Error != nil {
		s.appendError(event.Error)
	}
}

func (s *Sequencer) appendError(err *Error) {
	if err == nil {
		return
	}
	s.snapshot.Errors = append(s.snapshot.Errors, cloneError(err))
}

func (s *Sequencer) addUsage(usage *ModelUsage) {
	if usage == nil {
		return
	}
	s.snapshot.Stats.InputTokens += usage.InputTokens
	s.snapshot.Stats.OutputTokens += usage.OutputTokens
	s.snapshot.Stats.ReasoningTokens += usage.ReasoningTokens
	s.snapshot.Stats.CachedInputTokens += usage.CachedInputTokens
	s.snapshot.Stats.TotalTokens += usage.TotalTokens
}

func (s *Sequencer) attachModelToIteration(event *Event, id string) {
	iteration := s.iterationByKey[iterationKey(event.ContextId, event.Iteration, event.AttemptId)]
	if iteration == nil {
		return
	}
	iteration.ModelCallIds = append(iteration.ModelCallIds, id)
}

func (s *Sequencer) attachToolToIteration(event *Event, id string) {
	iteration := s.iterationByKey[iterationKey(event.ContextId, event.Iteration, event.AttemptId)]
	if iteration == nil {
		return
	}
	iteration.ToolCallIds = append(iteration.ToolCallIds, id)
}

func iterationKey(contextId string, iteration int, attempt string) string {
	return fmt.Sprintf("%s:%d:%s", contextId, iteration, attempt)
}
func callKey(id, attempt string) string { return id + "/" + attempt }

func statusFromError(err *Error) StepStatus {
	if err != nil {
		return StepStatusFailed
	}
	return StepStatusSucceeded
}

func contextStatusFromPayload(payload map[string]any, err *Error) StepStatus {
	if status, ok := payload["status"].(RunStatus); ok {
		return stepStatusFromRunStatus(status, err)
	}
	if reason, ok := payload["terminationReason"].(gent.TerminationReason); ok {
		return stepStatusFromTerminationReason(reason, err)
	}
	if reason, ok := payload["terminationReason"].(string); ok {
		return stepStatusFromTerminationReason(gent.TerminationReason(reason), err)
	}
	return statusFromError(err)
}

func stepStatusFromRunStatus(status RunStatus, err *Error) StepStatus {
	switch status {
	case RunStatusSucceeded:
		return StepStatusSucceeded
	case RunStatusCanceled:
		return StepStatusCanceled
	case RunStatusRefused:
		return StepStatusRefused
	case RunStatusFailed, RunStatusLimitExceeded:
		return StepStatusFailed
	default:
		return statusFromError(err)
	}
}

func stepStatusFromTerminationReason(reason gent.TerminationReason, err *Error) StepStatus {
	switch reason {
	case gent.TerminationSuccess:
		return StepStatusSucceeded
	case gent.TerminationRefused:
		return StepStatusRefused
	case gent.TerminationCanceled:
		return StepStatusCanceled
	case gent.TerminationStopped, gent.TerminationLimitExceeded:
		return StepStatusPaused
	default:
		return statusFromError(err)
	}
}

func durationMsFromPayload(payload any) int64 {
	data, _ := payload.(map[string]any)
	switch value := data["durationMs"].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	default:
		return 0
	}
}

func stringFromPayload(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}

func modelUsageFromPayload(payload map[string]any) *ModelUsage {
	usage, ok := payload["usage"].(*ModelUsage)
	if ok {
		return usage
	}
	return nil
}

func appendLimitedUTF8(current string, chunk string, max int) (string, bool) {
	if max == NoContentLimit {
		return current + chunk, false
	}
	combined := current + chunk
	if len(combined) <= max {
		return combined, false
	}
	if max <= 0 {
		return "", true
	}
	for max > 0 && !utf8.ValidString(combined[:max]) {
		max--
	}
	return combined[:max], true
}

func runStatus(reason gent.TerminationReason, err *Error) RunStatus {
	switch reason {
	case gent.TerminationSuccess:
		return RunStatusSucceeded
	case gent.TerminationRefused:
		return RunStatusRefused
	case gent.TerminationCanceled:
		return RunStatusCanceled
	case gent.TerminationStopped, gent.TerminationLimitExceeded:
		return RunStatusPaused
	default:
		if err != nil {
			return RunStatusFailed
		}
		return RunStatusSucceeded
	}
}

// The trace view is bounded independently of the archive. Omit oversize media whole and keep
// the item identity when text is clipped. This preview is never an execution-state replacement.
func (s *Sequencer) appendStreamPart(stream *ModelStream, part gent.ContentPart) {
	remaining := s.cfg.maxStreamContentBytes
	if remaining >= 0 {
		remaining -= contentSize(stream.Content)
	}
	parts, truncated := limitContent([]gent.ContentPart{part}, remaining)
	stream.ContentTruncated = stream.ContentTruncated || truncated
	for _, part := range parts {
		item, ok := part.(*gent.AssistantContent)
		if !ok {
			stream.Content = append(stream.Content, part)
			continue
		}
		var existing *gent.AssistantContent
		for _, old := range stream.Content {
			if old, ok := old.(*gent.AssistantContent); ok && old.MessageId == item.MessageId {
				existing = old
				break
			}
		}
		if existing == nil {
			stream.Content = append(stream.Content, item)
			continue
		}
		existing.Type = item.Type
		existing.Content = append(existing.Content, item.Content...)
	}
}

func (s *Sequencer) applyAcceptedAnswer(event *Event) {
	ctx := s.contextById[event.ContextId]
	if ctx == nil {
		s.applyContextStarted(event)
		ctx = s.contextById[event.ContextId]
	}
	payload, _ := event.Payload.(map[string]any)
	ctx.LastEventSequenceNumber = event.EventSequenceNumber
	if ids, ok := payload["sourceMessageIds"].([]string); ok {
		ctx.SourceMessageIds = append([]string(nil), ids...)
	}
	parts, _ := payload["content"].([]gent.ContentPart)
	ctx.MessageId, _ = payload["messageId"].(string)
	ctx.Content, ctx.ContentTruncated = limitContent(parts, s.cfg.maxContentBytes)
}
