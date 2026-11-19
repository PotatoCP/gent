package gent

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Storage can contain a high-water mark even when its old operation records have been pruned.
// Load that public representation, then verify that a new attempt cannot wrap and reuse an ID.
func TestRestoredCallSequenceExhaustion(t *testing.T) {
	type input struct {
		kind string
		call func(*ExecutionContext, *requestOwnershipModel) (string, error)
	}
	type expected struct {
		id      string
		value   string
		result  *ExecutionResult
		request *ModelCallRequest
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{name: "managed model", input: input{kind: "model",
			call: func(execCtx *ExecutionContext, model *requestOwnershipModel) (string, error) {
				stream, err := execCtx.GenerateContentStream(model, ModelCall{},
					NewModelCallRequest([]*ModelMessage{TextParts(ChatMessageTypeHuman, "Work")}))
				assert.Nil(t, stream)
				return "", err
			}}},
		{name: "tool allocation", input: input{kind: "tool",
			call: func(execCtx *ExecutionContext, _ *requestOwnershipModel) (string, error) {
				return execCtx.NewToolCallId()
			}}},
		{name: "model event", input: input{kind: "model",
			call: func(execCtx *ExecutionContext, _ *requestOwnershipModel) (string, error) {
				event := execCtx.PublishBeforeModelCall("fixture", &ModelCallRequest{})
				return event.ModelCallId, execCtx.RecordingError()
			}}},
		{name: "tool event", input: input{kind: "tool",
			call: func(execCtx *ExecutionContext, _ *requestOwnershipModel) (string, error) {
				event := execCtx.PublishBeforeToolCall("write", nil)
				return event.ToolCallId, execCtx.RecordingError()
			}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := NewCodecRegistry()
			runId, saved := recordedStateFixture(t, 1, codecs, nil)
			var batch SaveBatch
			assert.NoError(t, codecs.Unmarshal(saved[0], &batch))
			batch.Runtime.Contexts[0].NextModelCall = math.MaxUint64
			batch.Runtime.Contexts[0].NextToolCall = math.MaxUint64
			batch.Checkpoint.Contexts[0].Runtime = batch.Runtime.Contexts[0]
			var err error
			batch.BatchId, err = BatchDigest(&batch, codecs)
			assert.NoError(t, err)
			state := NewRunState(runId)
			applied, err := state.Apply(&batch, codecs)
			if !assert.NoError(t, err) ||
				!assert.Equal(t, &ApplyStateResult{Status: ApplyApplied}, applied) {
				return
			}
			recorder := &runStateRecorder{codecs: codecs}
			model := &requestOwnershipModel{}
			var id string
			var operationError error
			loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				id, operationError = test.input.call(execCtx, model)
				return nil, operationError
			}}
			runner := NewRunner[string](loop, &Config{Codecs: codecs, Recorder: recorder})
			restored, err := runner.Restore(context.Background(), state)
			if !assert.NoError(t, err) || !assert.Equal(t, RestoreReady, restored.Status) {
				return
			}
			execCtx := restored.Context
			defer execCtx.Release()
			value, result, err := runner.Continue(execCtx)
			assert.Equal(t, test.expected, expected{id, value, result, model.request})
			assert.EqualError(t, operationError, fmt.Sprintf(
				"gent: %s call sequence exhausted: run=%s turn=%s context=%s",
				test.input.kind, runId, execCtx.TurnId(), execCtx.ContextId()))
			assert.ErrorIs(t, err, operationError)
			assert.Len(t, recorder.batches, 1) // Only before-iteration acceptance, no tool or result.
			var accepted SaveBatch
			assert.NoError(t, codecs.Unmarshal(recorder.batches[0], &accepted))
			assert.Equal(t, PhaseIterationReady, accepted.Checkpoint.Phase)
			assert.Equal(t, uint64(math.MaxUint64), accepted.Runtime.Contexts[0].NextModelCall)
			assert.Equal(t, uint64(math.MaxUint64), accepted.Runtime.Contexts[0].NextToolCall)
		})
	}
}
