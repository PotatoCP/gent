package gent

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

type unreadChunksModel struct{}

func (unreadChunksModel) PrepareRequest(_ *ExecutionContext, request *ModelCallRequest,
	_ ToolChain,
) (*ModelCallRequest, error) {
	return request, nil
}

func TestManagedStreamsReleaseCompletedDelivery(t *testing.T) {
	type input struct{ readChunks bool }
	type expected struct{ ownedCounts []int }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"read all chunks", input{true}, expected{make([]int, 20)}},
		{"close after response", input{}, expected{make([]int, 20)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual := expected{}
			loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				for range 20 {
					stream, err := execCtx.GenerateContentStream(unreadChunksModel{}, ModelCall{},
						NewModelCallRequest(nil))
					if err != nil {
						return nil, err
					}
					if test.input.readChunks {
						for range stream.Chunks() {
						}
					}
					if _, err := stream.Response(); err != nil {
						return nil, err
					}
					if test.input.readChunks {
						<-stream.(*streamBuffer).drainDone
					} else {
						stream.Close()
					}
					// Observe resource ownership without changing the execution under test.
					execCtx.mu.RLock()
					actual.ownedCounts = append(actual.ownedCounts, len(execCtx.attempt.streams))
					execCtx.mu.RUnlock()
				}
				return acceptTurnText(execCtx, "Done")
			}}
			runner := NewRunner[string](loop, &Config{})
			execCtx, err := runner.NewExecutionContext(context.Background(), test.name)
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			_, _, err = runner.NewTurn(execCtx, NewTask(TextPart("Read progress.")))
			assert.NoError(t, err)
			assert.Equal(t, test.expected, actual)
		})
	}
}

type countedUnreadModel struct {
	unreadChunksModel
	calls int
}

func (model *countedUnreadModel) GenerateContentStream(ctx context.Context,
	request *ModelCallRequest,
) (Stream, error) {
	model.calls++
	return model.unreadChunksModel.GenerateContentStream(ctx, request)
}

func TestAttemptJoinsUnreadReplayedStream(t *testing.T) {
	codecs := NewCodecRegistry()
	store := &runStateRecorder{codecs: codecs, capture: true}
	model := &countedUnreadModel{}
	initial := NewRunner[string](&turnTestLoop{next: func(execCtx *ExecutionContext) (
		*AgentLoopResult, error,
	) {
		stream, err := execCtx.GenerateContentStream(model, ModelCall{Id: "saved-call"},
			NewModelCallRequest(nil))
		if err != nil {
			return nil, err
		}
		for range stream.Chunks() {
		}
		if _, err := stream.Response(); err != nil {
			return nil, err
		}
		execCtx.Stop()
		return nil, execCtx.Context().Err()
	}}, &Config{Codecs: codecs, Recorder: store})
	execCtx, err := initial.NewExecutionContext(context.Background(), "save stream")
	if !assert.NoError(t, err) {
		return
	}
	defer execCtx.Release()
	_, _, err = initial.NewTurn(execCtx, NewTask(TextPart("Read progress.")))
	if !assert.NoError(t, err) {
		return
	}
	failure := errors.New("loop failed after reading a saved chunk")
	var replayed *streamBuffer
	continued := NewRunner[string](&turnTestLoop{next: func(execCtx *ExecutionContext) (
		*AgentLoopResult, error,
	) {
		stream, err := execCtx.GenerateContentStream(model, ModelCall{Id: "saved-call"},
			NewModelCallRequest(nil))
		if err != nil {
			return nil, err
		}
		replayed = stream.(*streamBuffer)
		<-stream.Chunks()
		return nil, failure
	}}, &Config{Codecs: codecs})
	restored, err := continued.Restore(context.Background(), store.states[len(store.states)-1])
	if !assert.NoError(t, err) || !assert.Equal(t, RestoreReady, restored.Status) {
		return
	}
	defer restored.Context.Release()
	value, result, err := continued.Continue(restored.Context)
	assert.ErrorIs(t, err, failure)
	assert.Equal(t, "", value)
	assert.Nil(t, result)
	assert.Equal(t, 1, model.calls, "saved replay must not call the provider")
	if !assert.NotNil(t, replayed) {
		return
	}
	defer replayed.Close()
	select {
	case <-replayed.drainDone:
	default:
		t.Error("Continue returned before the saved stream drain worker stopped")
	}
}

func (unreadChunksModel) GenerateContentStream(context.Context, *ModelCallRequest) (Stream, error) {
	stream := NewStreamWithDuration()
	for range 8 {
		stream.Send(StreamChunk{Content: []ContentPart{&AssistantContent{
			Type: AssistantContentIntermediary, MessageId: "preview",
			Content: []ContentPart{TextPart("Progress")}}}})
	}
	stream.Complete(&ModelCallResponse{}, nil)
	return stream, nil
}

func TestAttemptJoinsUnreadManagedStream(t *testing.T) {
	type input struct{ fail bool }
	type expected struct{ failure error }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	failure := errors.New("loop failed after its first chunk")
	for _, test := range []testCase{
		{"failed loop", input{true}, expected{failure}},
		{"completed loop", input{}, expected{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var streams []*streamBuffer
			defer func() {
				// Also clean the deliberately failing regression on an unfixed implementation.
				for _, stream := range streams {
					stream.Close()
					<-stream.drainDone
				}
			}()
			loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				stream, err := execCtx.GenerateContentStream(unreadChunksModel{}, ModelCall{},
					NewModelCallRequest(NewTask(TextPart("Read progress.")).Messages))
				if err != nil {
					return nil, err
				}
				streams = append(streams, stream.(*streamBuffer))
				<-stream.Chunks()
				if test.input.fail {
					return nil, failure
				}
				return acceptTurnText(execCtx, "Done")
			}}
			runner := NewRunner[string](loop, &Config{})
			for range 3 {
				execCtx, err := runner.NewExecutionContext(context.Background(), test.name)
				if !assert.NoError(t, err) {
					return
				}
				_, _, err = runner.NewTurn(execCtx, NewTask(TextPart("Read progress.")))
				assert.ErrorIs(t, err, test.expected.failure)
				execCtx.Release()
				stream := streams[len(streams)-1]
				select {
				case <-stream.drainDone:
				default:
					t.Error("Release returned before the managed stream drain worker stopped")
				}
			}
		})
	}
}
