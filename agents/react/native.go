package react

import (
	"errors"
	"fmt"

	"github.com/rickchristie/gent"
	agentutil "github.com/rickchristie/gent/agents"
)

func (r *Agent[T]) buildMessages(execCtx *gent.ExecutionContext) ([]*gent.ModelMessage, error) {
	// Only the current prefix and selected conversation belong in a request. Prepending Task
	// again would duplicate user input and restore content that compaction intentionally omitted.
	messages := append([]*gent.ModelMessage(nil), execCtx.Instructions()...)
	records, err := execCtx.Scratchpad()
	if err != nil {
		return nil, err
	}
	history, _ := agentutil.ScratchpadToMessages(records, execCtx.Iteration())
	messages = append(messages, history...)
	return messages, execCtx.RecordingError()
}

func (r *Agent[T]) nextNative(
	execCtx *gent.ExecutionContext, frame *iterationFrame,
) (*gent.AgentLoopResult, error) {
	if len(frame.Response.Choices) == 0 || frame.Response.Choices[0] == nil {
		return r.nativeRetry(execCtx, nil, "The model returned no response. Please try again.")
	}
	choice := frame.Response.Choices[0]
	if err := choice.ValidateCompletion(); err != nil {
		return nil, err
	}
	if choice.Completion.Status == gent.ModelStatusRefused {
		if err := execCtx.AppendRecord(r.answerEntry(choice, nil), true); err != nil {
			return nil, err
		}
		return &gent.AgentLoopResult{Action: gent.LATerminate,
			TerminationReason: gent.TerminationRefused, Refusal: choice.Completion.Refusal}, nil
	}
	if choice.Completion.Status == gent.ModelStatusFailed {
		return nil, fmt.Errorf(
			"model stopped without a usable response: %q", choice.Completion.ProviderReason)
	}
	// Provider completion is authoritative. A refusal or failed response must never turn
	// into a request to try again because its retained evidence is long or repetitive.
	if repetition := frame.Repetition; repetition != nil {
		if errors.Is(repetition.Err, agentutil.ErrMaxResponseSize) {
			return r.handleResponseTooLong(execCtx, frame.Response)
		}
		return r.handleRepetition(execCtx, frame.Response, repetition)
	}
	if choice.Completion.Status == gent.ModelStatusIncomplete {
		return r.nativeRetry(execCtx, choice,
			"The response was incomplete. Use a shorter answer or fewer tool calls.")
	}
	if choice.Completion.Status == gent.ModelStatusContinue {
		if choice.Continuation == nil {
			return nil, fmt.Errorf(
				"model continuation %q requires a lossless adapter", choice.Completion.ProviderReason)
		}
		if err := execCtx.AppendRecord(r.answerEntry(choice, nil), true); err != nil {
			return nil, err
		}
		return &gent.AgentLoopResult{Action: gent.LAContinue}, nil
	}
	if len(choice.ToolCalls()) > 0 {
		return r.nextNativeTools(execCtx, frame, choice)
	}

	return r.nextNativeAnswer(execCtx, choice)
}

func (r *Agent[T]) nextNativeTools(
	execCtx *gent.ExecutionContext, frame *iterationFrame, choice *gent.ContentChoice,
) (*gent.AgentLoopResult, error) {
	if frame.ToolResultsPrepared {
		return &gent.AgentLoopResult{Action: gent.LAContinue}, nil
	}
	chain := r.tools(execCtx)
	if !frame.ToolPlanActive {
		normalized, err := normalizeCallIDs(execCtx, choice)
		if err != nil {
			execCtx.PublishParseError(gent.ParseErrorTypeToolchain,
				gent.ContentText(choice.CandidateContent()), err)
			return r.nativeRetry(execCtx, choice, err.Error())
		}
		if normalized != choice {
			response := *frame.Response
			response.Choices = append([]*gent.ContentChoice(nil), response.Choices...)
			response.Choices[0] = normalized
			frame.Response, choice = &response, normalized
		}
	}
	result, err := r.executeToolPlan(execCtx, frame, func() (*gent.ToolPlan, error) {
		return chain.PrepareCalls(execCtx, choice.ToolCalls())
	}, chain.ExecuteNext)
	// Chain errors are execution failures, including before a plan exists. Preserve the cause
	// before checking cancellation so Stop cannot turn a failed operation into an accepted pause.
	if err != nil {
		return nil, errors.Join(err, execCtx.RecordingError())
	}
	if recordErr := execCtx.RecordingError(); recordErr != nil {
		return nil, recordErr
	}
	if execCtx.Context().Err() != nil {
		return nil, execCtx.Context().Err()
	}
	entry := nativeToolEntry(choice, result)
	if err := execCtx.AppendRecord(entry, true); err != nil {
		return nil, err
	}
	execCtx.Stats().ResetGauge(gent.SGRepetitionLoopConsecutive)
	return &gent.AgentLoopResult{Action: gent.LAContinue}, nil
}

// Some endpoints omit tool IDs. The plan needs stable local IDs, but the original response and
// its borrowed derived views must stay unchanged. Copy only call wrappers that need normalization.
func normalizeCallIDs(execCtx *gent.ExecutionContext, choice *gent.ContentChoice) (
	*gent.ContentChoice, error,
) {
	result := choice
	seen := map[string]bool{}
	for i, part := range choice.Parts {
		var call *gent.ModelToolCallRequest
		switch part := part.(type) {
		case *gent.ModelToolCallRequest:
			call = part
		case gent.ModelToolCallRequest:
			copy := part
			call = &copy
		default:
			continue
		}
		if call.ID == "" || call.Type == "" {
			if result == choice {
				copy := *choice
				copy.Parts = append([]gent.ContentPart(nil), choice.Parts...)
				result = &copy
			}
			copy := *call
			if copy.ID == "" {
				id, err := execCtx.NewToolCallId()
				if err != nil {
					return nil, err
				}
				copy.ID = "native-" + id
			}
			if copy.Type == "" {
				copy.Type = "function"
			}
			call = &copy
			result.Parts[i] = call
		}
		if seen[call.ID] {
			return nil, fmt.Errorf("duplicate model tool call ID %q", call.ID)
		}
		seen[call.ID] = true
	}
	return result, nil
}

func nativeToolEntry(
	choice *gent.ContentChoice, result []*gent.ToolCallRecord,
) *gent.HistoryEntry {
	ai := &gent.MessageContent{Role: gent.ChatMessageTypeAI, Continuation: choice.Continuation}
	ai.Parts = choice.Parts
	entry := &gent.HistoryEntry{Messages: []*gent.MessageContent{ai}}
	for _, record := range result {
		message := &gent.MessageContent{Role: gent.ChatMessageTypeTool,
			Parts: []gent.ContentPart{record.Response()}}
		message.SetMetadata(gent.MMKToolCallRecords, []*gent.ToolCallRecord{record})
		entry.Messages = append(entry.Messages, message)
	}
	return entry
}

func (r *Agent[T]) nextNativeAnswer(
	execCtx *gent.ExecutionContext, choice *gent.ContentChoice,
) (*gent.AgentLoopResult, error) {
	result, err := r.output.Process(execCtx, choice.CandidateContent())
	if err == nil {
		err = result.Validate()
	}
	if err != nil {
		execCtx.PublishOutputValidation(gent.OutputValidationProcess, "", false, nil, err)
		return nil, err
	}
	if !result.Accepted {
		// Save the candidate and steering even if a stage limit has just canceled this run.
		next, err := r.nativeFeedback(execCtx, choice, result.Steering)
		if err != nil {
			return nil, err
		}
		execCtx.PublishOutputValidation(gent.OutputValidationProcess, "", false, result.Steering, nil)
		return next, nil
	}
	execCtx.Stats().ResetGauge(gent.SGRepetitionLoopConsecutive)
	if err := execCtx.AppendRecord(r.answerEntry(choice, nil), true); err != nil {
		return nil, err
	}
	accepted, err := gent.AcceptOutput(execCtx, result, choice.CandidateSource)
	execCtx.PublishOutputValidation(gent.OutputValidationProcess, "", err == nil, nil, err)
	return accepted, err
}

func (r *Agent[T]) nativeFeedback(
	execCtx *gent.ExecutionContext, choice *gent.ContentChoice, feedback []gent.ContentPart,
) (*gent.AgentLoopResult, error) {
	if err := execCtx.AppendRequiredRecord(r.answerEntry(choice, feedback)); err != nil {
		return nil, err
	}
	return &gent.AgentLoopResult{Action: gent.LAContinue}, nil
}

// Answer feedback can cause another request. Keep the provider items for that turn too.
func (r *Agent[T]) answerEntry(
	choice *gent.ContentChoice, feedback []gent.ContentPart,
) *gent.HistoryEntry {
	message := &gent.MessageContent{Role: gent.ChatMessageTypeAI,
		Parts: choice.Parts, Continuation: choice.Continuation}
	entry := &gent.HistoryEntry{Messages: []*gent.MessageContent{message}}
	if len(feedback) > 0 {
		steering := &gent.MessageContent{Role: gent.ChatMessageTypeHuman, Parts: feedback}
		steering.SetMetadata("gent:output_steering", true)
		entry.Messages = append(entry.Messages, steering)
	}
	return entry
}

func (r *Agent[T]) nativeRetry(
	execCtx *gent.ExecutionContext, choice *gent.ContentChoice, feedback string,
) (*gent.AgentLoopResult, error) {
	// Keep the rejected response for inspection. Do not send incomplete call groups to the model.
	entry := &gent.HistoryEntry{}
	if choice != nil {
		entry = r.answerEntry(choice, nil)
	}
	entry.ExpireAfterIteration = max(execCtx.Iteration(), 1)
	if err := execCtx.AppendRecord(entry, true); err != nil {
		return nil, err
	}
	reminder := &gent.HistoryEntry{Origin: gent.RecordSystemInjected,
		Messages: []*gent.MessageContent{{Role: gent.ChatMessageTypeHuman,
			Parts: []gent.ContentPart{gent.TextContent{Text: feedback}}}}}
	if err := execCtx.AppendRequiredRecord(reminder); err != nil {
		return nil, err
	}
	return &gent.AgentLoopResult{Action: gent.LAContinue}, nil
}
