package gent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rickchristie/gent/internal/modelcontext"
)

// GenerateContentStream is the managed model boundary. Hooks finish before the owned request is
// accepted; the adapter receives precisely those options and messages. Completion waits for the
// provider and accepted history, including when a consumer stops reading early.
func (execCtx *ExecutionContext) GenerateContentStream(
	model Model, call ModelCall, request *ModelCallRequest, chains ...ToolChain,
) (Stream, error) {
	if err := execCtx.RecordingError(); err != nil {
		return nil, err
	}
	if model == nil {
		return nil, errors.New("gent: nil model adapter")
	}
	if call.Model == "" {
		if named, ok := model.(interface{ ModelName() string }); ok {
			call.Model = named.ModelName()
		}
	}
	if call.Provider == "" {
		if provider, ok := model.(interface{ Provider() string }); ok {
			call.Provider = provider.Provider()
		}
	}
	if request.Options.Model == "" {
		request.Options.Model = call.Model
	}
	execCtx.run.mu.RLock()
	request.SelectionRevision = execCtx.run.working.Revision
	request.ActiveEntryIds = append([]EntryId(nil), execCtx.active...)
	execCtx.run.mu.RUnlock()
	var err error
	call.Id, err = execCtx.modelCallId(call.Id)
	if err != nil {
		return nil, err
	}
	stream, err := execCtx.replayModelCall(call.Id)
	if err != nil {
		return nil, err
	}
	if stream != nil {
		execCtx.trackStream(stream.(*streamBuffer))
		return stream, nil
	}
	var chain ToolChain
	if len(chains) > 1 {
		return nil, errors.New("gent: model call accepts at most one tool chain")
	}
	if len(chains) == 1 {
		chain = chains[0]
	}
	prepared := *request
	prepared.Options.SteeringMessages = execCtx.SteeringMessages()
	request, err = model.PrepareRequest(execCtx, &prepared, chain)
	if err != nil {
		return nil, err
	}
	if request == nil {
		return nil, errors.New("gent: model preparation returned no request")
	}
	// Only a mutable request hook needs an independent data tree. Without one, adapters read the
	// owned parts directly and the recorder captures the effective request in serialized evidence.
	if execCtx.hasModelRequestHooks() {
		request, err = cloneOwned(execCtx.Codecs(), request)
		if err != nil {
			return nil, execCtx.stopRecording(err)
		}
	}
	expectedOutput, err := captureOutputControls(request.Options)
	if err != nil {
		return nil, err
	}
	before := execCtx.PublishBeforeModelCall(call.Model, request, WithModelCallId(call.Id),
		WithModelStream(call.StreamId, call.StreamTopicId), WithModelCallSource(call.Source),
		WithModelProvider(call.Provider))
	// A physical retry may reuse the logical call ID within the same worker attempt.
	// Its preview must have a new identity; saved replay keeps this identity unchanged.
	messagePrefix := before.ContextId + ":message:" + randomIdentity() + ":"
	if err := execCtx.RecordingError(); err != nil {
		return nil, err
	}
	// The hook transfers its request on return. Recorded evidence is already encoded; a second
	// deep copy would only protect against a prohibited later edit by the hook.
	request = before.Request
	if err := checkOutputControls(expectedOutput, request.Options); err != nil {
		return nil, err
	}
	if err := validateRequestGroups(request); err != nil {
		return nil, err
	}
	if err := execCtx.Context().Err(); err != nil {
		return nil, err
	}
	providerCtx, cancel := context.WithCancel(execCtx.Context())
	providerCtx = modelcontext.WithRecorder(providerCtx, func(evidence map[string]any) error {
		evidence["model_call_id"] = before.ModelCallId
		execCtx.PublishCommonEvent("gent:media_prepared", "Prepared media for model input.", evidence)
		return execCtx.RecordingError()
	})
	producerDone, err := execCtx.trackProducer()
	if err != nil {
		cancel()
		return nil, err
	}
	provider, err := model.GenerateContentStream(providerCtx, request)
	if err != nil {
		producerDone()
		cancel()
		execCtx.PublishAfterModelCallForIteration(before.Iteration, before.Model, request, nil, 0, err,
			WithModelCallId(
				before.ModelCallId,
			), WithModelStream(
				before.StreamId,
				before.StreamTopicId,
			))
		return nil, errors.Join(err, execCtx.RecordingError())
	}
	if provider == nil {
		producerDone()
		cancel()
		err := errors.New("gent: model adapter returned a nil stream")
		execCtx.PublishAfterModelCallForIteration(before.Iteration, before.Model, request, nil, 0, err,
			WithModelCallId(
				before.ModelCallId,
			), WithModelStream(
				before.StreamId,
				before.StreamTopicId,
			))
		return nil, errors.Join(err, execCtx.RecordingError())
	}
	output := newStreamBufferWithClose(func() { cancel(); provider.Close() })
	execCtx.trackStream(output)
	started := time.Now()
	go func() {
		defer producerDone()
		defer cancel()
		var recordingErr error
		for chunk := range provider.Chunks() {
			if recordingErr == nil {
				recordingErr = validateAssistantParts(chunk.Content, true)
			}
			if recordingErr == nil && len(chunk.SourceMessageIds) != 0 {
				recordingErr = errors.New("gent: provider progress cannot publish accepted source links")
			}
			if recordingErr == nil {
				// Scoping needs a separate item wrapper. The producer retains its local identities.
				chunk = cloneStreamChunk(chunk)
				for _, part := range chunk.Content {
					if item, ok := part.(*AssistantContent); ok {
						item.MessageId = scopedMessageId(messagePrefix, item.MessageId)
					}
				}
				recordingErr = execCtx.emitChunk(&chunk, before)
			}
			if recordingErr != nil {
				cancel()
				provider.Close()
				continue
			}
			output.Send(chunk)
		}

		response, providerErr := provider.Response()
		if response != nil && recordingErr == nil {
			response, recordingErr = cloneOwned(execCtx.Codecs(), response)
			if recordingErr == nil {
				recordingErr = scopeResponseMessages(before, messagePrefix, response)
			}
		}
		if recordingErr == nil {
			execCtx.PublishAfterModelCallForIteration(before.Iteration, before.Model, request, response,
				time.Since(started), providerErr, WithModelCallId(before.ModelCallId),
				WithModelStream(before.StreamId, before.StreamTopicId),
				WithModelCallSource(before.Source), WithModelProvider(before.Provider))
			recordingErr = execCtx.RecordingError()
		}
		if recordingErr != nil {
			// Partial provider output can also fail local validation or evidence capture. Keep
			// both causes; the secondary failure must not hide the provider operation failure.
			output.Complete(nil, fmt.Errorf(
				"gent: model stream/response for call %q in run %q turn %q context %q: %w",
				before.ModelCallId, execCtx.RunId(), execCtx.TurnId(), before.ContextId,
				errors.Join(providerErr, recordingErr)))
			return
		}
		output.Complete(response, providerErr)
	}()
	return output, nil
}

func (execCtx *ExecutionContext) hasModelRequestHooks() bool {
	return execCtx.config.Hooks != nil && execCtx.config.Hooks.BeforeModelCall != nil
}

func scopedMessageId(prefix, local string) string {
	if local == "" {
		local = "message:0"
	}
	return prefix + local
}

func scopeResponseMessages(
	call *BeforeModelCallEvent, prefix string, response *ModelCallResponse,
) error {
	for index, choice := range response.Choices {
		if choice == nil {
			return fmt.Errorf("gent: model choice %d is nil", index)
		}
		if err := choice.ValidateCompletion(); err != nil {
			return fmt.Errorf("gent: model call %q choice %d: %w", call.ModelCallId, index, err)
		}
		for _, part := range choice.Parts {
			if item, ok := part.(*AssistantContent); ok {
				item.MessageId = scopedMessageId(prefix, item.MessageId)
			}
		}
		if source := choice.CandidateSource; source != nil {
			source.ModelCallId, source.StreamId = call.ModelCallId, call.StreamId
			source.StreamTopicId = call.StreamTopicId
			for i, id := range source.MessageIds {
				source.MessageIds[i] = scopedMessageId(prefix, id)
			}
		}
	}
	return nil
}
