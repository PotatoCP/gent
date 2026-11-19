package trace

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/internal/tt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHistoryReplayCursorsAndLostCompletion(t *testing.T) {
	config := Config{MaxRecentEvents: 2, IncludeRunOutput: true,
		IncludeChunkText: true, IncludeStateChanges: true}
	seq := newTestSequencer(t, "", config)
	defer seq.Close()
	loop := &traceTestLoop{next: func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
		for i := range 120 {
			execCtx.PublishCommonEvent("fixture:progress", "accepted progress", i)
		}
		for range 8 {
			if err := execCtx.EmitChunk(gent.StreamChunk{Content: []gent.ContentPart{&gent.AssistantContent{
				Type: gent.AssistantContentIntermediary, MessageId: "partial",
				Content: []gent.ContentPart{gent.TextPart("x")}}}}); err != nil {
				return nil, err
			}
		}
		execCtx.Stop()
		return nil, execCtx.Context().Err()
	}}
	runner, execCtx, store := newTraceRun(t, context.Background(), loop,
		&gent.Config{Subscribers: []*gent.SubscriberRegistration{{Subscriber: seq}}})
	_, paused, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Work.")))
	require.NoError(t, err)
	require.Equal(t, gent.ExecutionPaused, paused.Status)
	assert.Equal(t, RunStatusPaused, seq.Snapshot().Status)
	savedCursor := store.view.LastAppliedEventSequenceNumber
	assert.Greater(t, savedCursor, uint64(128))
	assert.Equal(t, savedCursor, store.view.Checkpoint.EventSequenceNumber)
	archive := gent.NewRunView(execCtx.RunId())
	codecs := gent.NewCodecRegistry()
	for _, data := range store.batches {
		var batch gent.SaveBatch
		require.NoError(t, codecs.Unmarshal(data, &batch))
		applied, err := archive.Apply(&batch, codecs)
		require.NoError(t, err)
		require.Equal(t, gent.ApplyApplied, applied.Status)
	}
	assert.Equal(t, store.view, archive)
	fresh, err := RestoreSequencer(*archive, config)
	require.NoError(t, err)
	defer fresh.Close()
	assert.Equal(t, seq.Snapshot(), fresh.Snapshot())
	loaded, err := codecs.UnmarshalRunState(store.runStateSnapshot)
	require.NoError(t, err)
	require.Equal(t, gent.RunStateReady, loaded.Status)
	loop = &traceTestLoop{next: func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
		for _, text := range []string{"y", "z"} {
			if err := execCtx.EmitChunk(gent.StreamChunk{Content: []gent.ContentPart{&gent.AssistantContent{
				Type: gent.AssistantContentIntermediary, MessageId: "partial",
				Content: []gent.ContentPart{gent.TextPart(text)}}}}); err != nil {
				return nil, err
			}
		}
		return traceTestDone(execCtx)
	}}
	continued := executor.NewRunner[string](loop, &gent.Config{Codecs: codecs, Recorder: store,
		Subscribers: []*gent.SubscriberRegistration{{Subscriber: fresh}}})
	restored, err := continued.Restore(context.Background(), loaded.State)
	require.NoError(t, err)
	require.Equal(t, gent.RestoreReady, restored.Status)
	defer restored.Context.Release()
	assert.Equal(t, savedCursor, fresh.Snapshot().LastEventSequenceNumber)
	_, completed, err := continued.Continue(restored.Context)
	require.NoError(t, err)
	require.Equal(t, gent.ExecutionFinished, completed.Status)
	last := fresh.Snapshot().LastEventSequenceNumber
	events, err := fresh.EventsAfter(last - 1)
	require.NoError(t, err)
	assert.Equal(t, StitchStatusGap, ValidateStitch(savedCursor, events).Status)
	assert.Equal(t, StitchStatusOK, ValidateStitch(last-1, events).Status)
	_, err = fresh.EventsAfter(savedCursor)
	assert.ErrorIs(t, err, ErrReplayUnavailable)
	// A client can miss the last publication without observing another event. A full accepted
	// archive read must still reveal the complete answer and its terminal cursor.
	late, err := RestoreSequencer(*store.view, config)
	require.NoError(t, err)
	defer late.Close()
	assert.Equal(t, fresh.Snapshot(), late.Snapshot())
	assert.Equal(t, gent.PhaseTerminal, store.view.Checkpoint.Phase)
	assert.Equal(t, "Done", gent.ContentText(late.Snapshot().Contexts[0].Content))
	assert.NoError(t, gent.ValidateBundle(*store.view, codecs))
}

type interruptedProvider struct{ started chan struct{} }

func (model *interruptedProvider) GenerateContentStream(
	ctx context.Context,
	_ *gent.ModelCallRequest,
) (
	gent.Stream,
	error,
) {
	stream := gent.NewStreamWithDuration()
	go func() {
		parts := []gent.ContentPart{&gent.AssistantContent{Type: gent.AssistantContentIntermediary,
			MessageId: "partial", Content: []gent.ContentPart{gent.TextPart("partial policy evidence")}}}
		stream.Send(gent.StreamChunk{Content: parts})
		close(model.started)
		<-ctx.Done()
		stream.Complete(&gent.ModelCallResponse{Choices: []*gent.ContentChoice{{Parts: parts,
			Completion: gent.ModelCompletion{Status: gent.ModelStatusIncomplete}}},
			Info: &gent.GenerationInfo{InputTokens: 12, OutputTokens: 8, TotalTokens: 20}}, ctx.Err())
	}()
	return stream, nil
}
func (*interruptedProvider) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}

func TestHistoryReplayModelAttemptsAndKnownUsage(t *testing.T) {
	config := Config{IncludeChunkText: true, IncludeModelRequests: true, IncludeModelResponses: true}
	seq := newTestSequencer(t, "", config)
	defer seq.Close()
	model := &interruptedProvider{started: make(chan struct{})}
	buildLoop := func(model gent.Model) *traceTestLoop {
		return &traceTestLoop{next: func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
			stream, err := execCtx.GenerateContentStream(model,
				gent.ModelCall{Id: execCtx.ContextId() + ":policy", StreamId: "answer"},
				gent.NewModelCallRequest([]*gent.ModelMessage{
					gent.TextParts(gent.ChatMessageTypeHuman, "policy question")}))
			if err != nil {
				return nil, err
			}
			defer stream.Close()
			for range stream.Chunks() {
			}
			response, err := stream.Response()
			if err != nil {
				return nil, err
			}
			choice := response.Choices[0]
			return gent.AcceptOutput(execCtx, &gent.OutputResult[string]{Accepted: true,
				Value: gent.ContentText(choice.CandidateContent()), Content: choice.CandidateContent()},
				choice.CandidateSource)
		}}
	}
	runner, execCtx, store := newTraceRun(t, context.Background(), buildLoop(model),
		&gent.Config{Subscribers: []*gent.SubscriberRegistration{{Subscriber: seq}}})
	type result struct {
		execution *gent.ExecutionResult
		err       error
	}
	done := make(chan result, 1)
	go func() {
		_, execution, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read policy.")))
		done <- result{execution, err}
	}()
	<-model.started
	execCtx.Stop()
	paused := <-done
	require.NoError(t, paused.err)
	require.Equal(t, gent.ExecutionPaused, paused.execution.Status)
	assert.Equal(t, int64(20), execCtx.Stats().GetCounter(gent.SCTotalTokens))
	freshSeq, err := RestoreSequencer(*store.view, config)
	require.NoError(t, err)
	defer freshSeq.Close()
	assert.Equal(t, seq.Snapshot(), freshSeq.Snapshot())
	codecs := gent.NewCodecRegistry()
	loaded, err := codecs.UnmarshalRunState(store.runStateSnapshot)
	require.NoError(t, err)
	require.Equal(t, gent.RunStateReady, loaded.Status)
	freshModel := tt.NewMockModel().AddResponse("complete answer", 12, 8)
	fresh := executor.NewRunner[string](buildLoop(freshModel), &gent.Config{Codecs: codecs,
		Recorder: store, Subscribers: []*gent.SubscriberRegistration{{Subscriber: freshSeq}}})
	restored, err := fresh.Restore(context.Background(), loaded.State)
	require.NoError(t, err)
	require.Equal(t, gent.RestoreReady, restored.Status)
	defer restored.Context.Release()
	assert.Equal(t, int64(20), restored.Context.Stats().GetCounter(gent.SCTotalTokens))
	value, completed, err := fresh.Continue(restored.Context)
	require.NoError(t, err)
	assert.Equal(t, gent.ExecutionFinished, completed.Status)
	assert.Equal(t, "complete answer", value)
	assert.Len(t, freshModel.CapturedRequests, 1)
	assert.Equal(t, int64(40), restored.Context.Stats().GetCounter(gent.SCTotalTokens))
	snapshot := freshSeq.Snapshot()
	require.Len(t, snapshot.ModelCalls, 2)
	first, second := snapshot.ModelCalls[0], snapshot.ModelCalls[1]
	assert.Equal(t, first.Id, second.Id)
	assert.NotEqual(t, first.AttemptId, second.AttemptId)
	assert.NotEqual(t, StepStatusSucceeded, first.Status)
	assert.Equal(t, StepStatusSucceeded, second.Status)
	assert.Equal(t, "partial policy evidence", gent.ContentText(first.Stream.Content))
	assert.Equal(t, "complete answer", gent.ContentText(second.Stream.Content))
	assert.Equal(t, 40, snapshot.Stats.TotalTokens)
	assert.Equal(t, RunStatusSucceeded, snapshot.Status)
	assert.ErrorIs(t, execCtx.Context().Err(), context.Canceled)
}
