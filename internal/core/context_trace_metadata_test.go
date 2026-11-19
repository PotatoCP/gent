package gent

import (
	"context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// Identity belongs to the frozen event envelope, not its StreamChunk payload. The same saved
// evidence must retain run, turn, context, and sequence information without a UI-only wrapper.
func TestExecutionContextRecordedChunkIdentity(t *testing.T) {
	codecs := NewCodecRegistry()
	recorder := &runStateRecorder{codecs: codecs}
	chunk := StreamChunk{Content: []ContentPart{&AssistantContent{
		Type: AssistantContentIntermediary, MessageId: "progress",
		Content: []ContentPart{TextPart("hello")},
	}}}
	loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
		if err := execCtx.EmitChunk(chunk); err != nil {
			return nil, err
		}
		return acceptTurnText(execCtx, "Done")
	}}
	runner := NewRunner[string](loop, &Config{Recorder: recorder, Codecs: codecs})
	execCtx, err := runner.NewExecutionContext(context.Background(), "main")
	require.NoError(t, err)
	defer execCtx.Release()
	earliest := time.Now()
	_, _, err = runner.NewTurn(execCtx, NewTask(TextPart("Read.")))
	require.NoError(t, err)
	var chunks []*RecordedEvent
	var previous uint64
	for _, data := range recorder.batches {
		var batch SaveBatch
		require.NoError(t, codecs.Unmarshal(data, &batch))
		for _, event := range batch.Events {
			assert.Equal(t, previous+1, event.EventSequenceNumber)
			previous = event.EventSequenceNumber
			if event.Type == "model_stream_chunk" {
				chunks = append(chunks, event)
			}
		}
	}
	require.Len(t, chunks, 1)
	recorded := chunks[0]
	assert.GreaterOrEqual(t, recorded.Ts.UnixNano(), earliest.UnixNano())
	assert.Equal(t, &RecordedEvent{EventSequenceNumber: recorded.EventSequenceNumber,
		RunId: execCtx.RunId(), TurnId: execCtx.TurnId(), ContextId: execCtx.ContextId(),
		AttemptId: execCtx.AttemptId(), Ts: recorded.Ts, Iteration: 1,
		Type: "model_stream_chunk", Payload: &chunk}, recorded)
	assert.Equal(t, "main/1", execCtx.BuildSourcePath())
}

func TestExecutionContext_TraceMetadata_ModelAndToolCorrelation(t *testing.T) {
	type expected struct {
		modelCallId      string
		provider         string
		streamId         string
		streamTopicId    string
		afterModelSource string
		toolCallId       string
		afterToolSource  string
	}

	execCtx := newComponentContext(t, &Config{})
	execCtx.IncrementIteration()
	beforeModel := execCtx.PublishBeforeModelCall(
		"gpt", &ModelCallRequest{},
		WithModelStream("stream", "topic"),
		WithModelProvider("openai"),
	)
	afterModel := execCtx.PublishAfterModelCallForIteration(
		beforeModel.Iteration,
		"gpt", &ModelCallRequest{}, nil, time.Millisecond, nil,
		WithModelCallId(beforeModel.ModelCallId),
		WithModelStream(beforeModel.StreamId, beforeModel.StreamTopicId),
		WithModelCallSource(beforeModel.Source),
		WithModelProvider(beforeModel.Provider),
	)
	beforeTool := execCtx.PublishBeforeToolCall("search", map[string]any{"query": "x"})
	afterTool := execCtx.PublishAfterToolCall(
		"search", beforeTool.Args, "ok", time.Millisecond, nil,
		WithToolCallId(beforeTool.ToolCallId),
		WithToolCallSource(beforeTool.Source),
	)

	require.NotEmpty(t, beforeModel.ModelCallId)
	require.NotEmpty(t, beforeTool.ToolCallId)
	assert.Equal(t, expected{
		modelCallId:      beforeModel.ModelCallId,
		provider:         "openai",
		streamId:         "stream",
		streamTopicId:    "topic",
		afterModelSource: beforeModel.Source,
		toolCallId:       beforeTool.ToolCallId,
		afterToolSource:  beforeTool.Source,
	}, expected{
		modelCallId:      afterModel.ModelCallId,
		provider:         afterModel.Provider,
		streamId:         afterModel.StreamId,
		streamTopicId:    afterModel.StreamTopicId,
		afterModelSource: afterModel.Source,
		toolCallId:       afterTool.ToolCallId,
		afterToolSource:  afterTool.Source,
	})
}
