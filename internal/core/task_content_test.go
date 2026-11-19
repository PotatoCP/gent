package gent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTaskPreservesParts(t *testing.T) {
	type input struct{ parts []ContentPart }
	type expected struct{ task *Task }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"empty", input{}, expected{&Task{Messages: []*ModelMessage{{Role: ChatMessageTypeHuman}}}}},
		{"ordered media", input{[]ContentPart{TextContent{Text: "Read both."},
			&BinaryContent{MIMEType: "application/pdf", Data: []byte{0, 1, 255}},
			ImageURLContent{URL: "https://example.org/image.png"}}},
			expected{&Task{Messages: []*ModelMessage{{Role: ChatMessageTypeHuman,
				Parts: []ContentPart{TextContent{Text: "Read both."},
					&BinaryContent{MIMEType: "application/pdf", Data: []byte{0, 1, 255}},
					ImageURLContent{URL: "https://example.org/image.png"}}}}}}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected.task, NewTask(tt.input.parts...))
		})
	}
}

func TestTaskPointersAndMediaRemainOwnedAcrossRequests(t *testing.T) {
	makeTask := func() *Task {
		return &Task{Instructions: []*ModelMessage{
			TextParts(ChatMessageTypeSystem, "Use the attached evidence."),
		}, Messages: []*ModelMessage{{Role: ChatMessageTypeHuman, Parts: []ContentPart{
			&TextContent{Text: "Read the PDF."},
			&BinaryContent{MIMEType: "application/pdf", Data: []byte{0, 7, 255}},
			BinaryContent{MIMEType: "image/png", Data: []byte{1, 2, 3}},
		}}}}
	}
	task := makeTask()
	native := &ProviderContinuation{Provider: "fixture",
		Items: []json.RawMessage{json.RawMessage(`{"signature":"original"}`)}}
	codecs := NewCodecRegistry()
	recorder := &runStateRecorder{codecs: codecs, capture: true}
	loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
		// Admission owns an independent saved task. The caller can reuse its input buffers.
		task.Instructions[0].Role = ChatMessageTypeHuman
		task.Messages[0].Parts[1].(*BinaryContent).Data[0] = 99
		owned, err := execCtx.Task()
		if err != nil {
			return nil, err
		}
		assert.Equal(t, makeTask(), owned)
		entry, err := execCtx.NewHistoryEntry(&HistoryEntry{Messages: []*MessageContent{{
			Role: ChatMessageTypeAI, Continuation: native}}})
		if err != nil {
			return nil, err
		}
		draft := execCtx.ContextDraft()
		if err := execCtx.UpdateHistory(HistoryUpdate{ExpectedDraftRevision: draft.Revision,
			AppendedEntries: []*HistoryEntry{entry}, Active: append(draft.Active, entry.Id),
			Reason: ContextAppend}); err != nil {
			return nil, err
		}
		messages := append([]*ModelMessage{}, owned.Instructions...)
		messages = append(messages, owned.Messages...)
		messages = append(messages, &ModelMessage{Role: ChatMessageTypeAI, Continuation: native})
		model := &requestOwnershipModel{}
		stream, err := execCtx.GenerateContentStream(model, ModelCall{}, NewModelCallRequest(messages))
		if err != nil {
			return nil, err
		}
		defer stream.Close()
		for range stream.Chunks() {
		}
		if _, err := stream.Response(); err != nil {
			return nil, err
		}
		assert.Equal(t, makeTask(), owned)
		assert.Equal(t, json.RawMessage(`{"signature":"original"}`), native.Items[0])
		assert.Equal(t, "Request only.", ContentText(model.request.Messages[0].Parts))
		assert.Equal(t, byte(88), model.request.Messages[1].Parts[1].(*BinaryContent).Data[0])
		assert.Equal(t, byte(77), model.request.Messages[1].Parts[2].(BinaryContent).Data[0])
		assert.Equal(t, json.RawMessage(`{"signature":"changed"}`),
			model.request.Messages[2].Continuation.Items[0])
		return acceptTurnText(execCtx, "Done")
	}}
	runner := NewRunner[string](loop, &Config{Codecs: codecs, Recorder: recorder,
		Hooks: requestOwnershipHooks(func(request *ModelCallRequest) {
			request.Messages[0].Parts[0] = TextPart("Request only.")
			request.Messages[1].Parts[1].(*BinaryContent).Data[0] = 88
			request.Messages[1].Parts[2].(BinaryContent).Data[0] = 77
			request.Messages[2].Continuation.Items[0] = json.RawMessage(`{"signature":"changed"}`)
		})})
	execCtx, err := runner.NewExecutionContext(context.Background(), "task-media")
	require.NoError(t, err)
	defer execCtx.Release()
	_, _, err = runner.NewTurn(execCtx, task)
	require.NoError(t, err)
	restored, err := runner.Restore(context.Background(), recorder.states[len(recorder.states)-1])
	require.NoError(t, err)
	require.Equal(t, RestoreReady, restored.Status)
	defer restored.Context.Release()
	actual, err := restored.Context.Task()
	assert.NoError(t, err)
	assert.Equal(t, makeTask(), actual)
	assert.Equal(t, execCtx.History(), restored.Context.History())
}

func TestBinaryStreamAndTerminalContentRestore(t *testing.T) {
	parts := []ContentPart{TextPart("Generated files."),
		&BinaryContent{MIMEType: "image/png", Data: []byte{0, 1, 255}},
		BinaryContent{MIMEType: "application/pdf", Data: []byte{3, 4, 5}}, TextPart("Done.")}
	want := CloneContentParts(parts)
	codecs := NewCodecRegistry()
	recorder := &runStateRecorder{codecs: codecs, capture: true}
	subscriber := &ownershipSubscriber{}
	loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
		if err := execCtx.EmitChunk(StreamChunk{Content: []ContentPart{&AssistantContent{
			Type: AssistantContentIntermediary, MessageId: "preview", Content: parts}}}); err != nil {
			return nil, err
		}
		parts[1].(*BinaryContent).Data[0] = 99
		return AcceptOutput(execCtx, &OutputResult[string]{Accepted: true,
			Value: "files", Content: want}, &ContentSource{MessageIds: []string{"preview"}})
	}}
	runner := NewRunner[string](loop, &Config{Codecs: codecs, Recorder: recorder,
		Subscribers: []*SubscriberRegistration{{Subscriber: subscriber}}})
	execCtx, err := runner.NewExecutionContext(context.Background(), "binary-output")
	require.NoError(t, err)
	defer execCtx.Release()
	value, result, err := runner.NewTurn(execCtx, NewTask(TextPart("Generate files.")))
	require.NoError(t, err)
	assert.Equal(t, "files", value)
	assert.Equal(t, &ExecutionResult{Status: ExecutionFinished, TurnId: execCtx.TurnId(),
		TerminationReason: TerminationSuccess, Output: want}, result)
	saved := savedTestEvents(t, recorder)
	assert.Equal(t, saved, subscriber.events)
	var savedPreview, savedAccepted []ContentPart
	for _, event := range subscriber.events {
		chunk, ok := event.Payload.(*StreamChunk)
		if !ok {
			continue
		}
		group := chunk.Content[0].(*AssistantContent)
		switch event.Type {
		case "accepted_answer":
			savedAccepted = group.Content
			assert.Equal(t, AssistantContentFinalAnswer, group.Type)
			assert.Equal(t, []string{"preview"}, chunk.SourceMessageIds)
		case "model_stream_chunk":
			savedPreview = group.Content
		}
		// A UI editor owns its own mutable content; delivered evidence stays read-only.
		display := CloneContentParts(group.Content)
		display[1].(*BinaryContent).Data[0] = 88
	}
	assert.Equal(t, want, savedPreview)
	assert.Equal(t, want, savedAccepted)
	assert.Equal(t, saved, subscriber.events)
	restored, err := runner.Restore(context.Background(), recorder.states[len(recorder.states)-1])
	require.NoError(t, err)
	require.Equal(t, RestoreReady, restored.Status)
	defer restored.Context.Release()
	actual, completed, err := runner.GetLastTurnResult(restored.Context)
	assert.NoError(t, err)
	assert.Equal(t, value, actual)
	assert.Equal(t, result, completed)
}
