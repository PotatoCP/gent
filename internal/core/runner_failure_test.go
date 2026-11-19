package gent

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

type uncertainTurnRecorder struct {
	*runStateRecorder
	failAt   int
	accepted bool
	failure  error
}

func (recorder *uncertainTurnRecorder) Save(ctx context.Context, batch *SaveBatch,
	capture func() (*RunState, error),
) error {
	fail := len(recorder.batches)+1 == recorder.failAt
	if !fail || recorder.accepted {
		if err := recorder.runStateRecorder.Save(ctx, batch, capture); err != nil {
			return err
		}
	}
	if fail {
		return recorder.failure
	}
	return nil
}

func TestRunnerReconcilesUncertainNewTurnAdmission(t *testing.T) {
	type input struct{ accepted bool }
	type expected struct {
		resetAfterRestore  int
		activeAfterRestore bool
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"storage rejected", input{}, expected{1, false}},
		{"acknowledgment lost", input{true}, expected{2, true}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := NewCodecRegistry()
			failure := errors.New("storage connection closed")
			recorder := &uncertainTurnRecorder{runStateRecorder: &runStateRecorder{
				codecs: codecs, capture: true}, accepted: test.input.accepted, failure: failure}
			loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				task, err := execCtx.Task()
				if err != nil {
					return nil, err
				}
				return acceptTurnText(execCtx, ContentText(task.Messages[0].Parts))
			}}
			runner := NewRunner[string](loop, &Config{Codecs: codecs, Recorder: recorder,
				NewLoopData: func() LoopData { return &turnTestData{} }})
			execCtx, err := runner.NewExecutionContext(context.Background(), "uncertain admission")
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			firstValue, firstResult, err := runner.NewTurn(execCtx, NewTask(TextPart("First")))
			if !assert.NoError(t, err) {
				return
			}
			recorder.failAt = len(recorder.batches) + 1
			value, result, err := runner.NewTurn(execCtx, NewTask(TextPart("Second")))
			assert.ErrorIs(t, err, failure)
			assert.Equal(t, "", value)
			assert.Nil(t, result)
			lastValue, lastResult, err := runner.GetLastTurnResult(execCtx)
			assert.NoError(t, err)
			assert.Equal(t, firstValue, lastValue)
			assert.Equal(t, firstResult, lastResult)
			_, result, err = runner.Continue(execCtx)
			assert.Error(t, err)
			assert.Nil(t, result)
			recorder.failAt = 0
			state := NewRunState(execCtx.RunId())
			for _, batch := range recorder.batches {
				applyRecordedState(t, state, codecs, batch)
			}
			restored, err := runner.Restore(context.Background(), state)
			if !assert.NoError(t, err) || !assert.Equal(t, RestoreReady, restored.Status) {
				return
			}
			defer restored.Context.Release()
			assert.Equal(t, &turnTestData{Resets: test.expected.resetAfterRestore}, restored.Context.Data())
			assert.Equal(t, test.expected.activeAfterRestore, restored.Context.turnOpen)
			if test.input.accepted {
				value, result, err = runner.Continue(restored.Context)
			} else {
				value, result, err = runner.NewTurn(restored.Context, NewTask(TextPart("Second")))
			}
			assert.NoError(t, err)
			assert.Equal(t, "Second", value)
			assert.Equal(t, &ExecutionResult{Status: ExecutionFinished,
				TurnId: restored.Context.TurnId(), TerminationReason: TerminationSuccess,
				Output: []ContentPart{TextPart("Second")}}, result)
			assert.Equal(t, &turnTestData{Resets: 2}, restored.Context.Data())
		})
	}
}

func TestRunnerUnexpectedFailurePreservesAcceptedTask(t *testing.T) {
	failure := errors.New("application invariant failed")
	codecs := NewCodecRegistry()
	recorder := &runStateRecorder{codecs: codecs, capture: true}
	broken := &turnTestLoop{next: func(*ExecutionContext) (*AgentLoopResult, error) {
		return nil, failure
	}}
	first := NewRunner[string](broken, &Config{Codecs: codecs, Recorder: recorder})
	execCtx, err := first.NewExecutionContext(context.Background(), "fix and continue")
	if !assert.NoError(t, err) {
		return
	}
	defer execCtx.Release()
	value, result, err := first.NewTurn(execCtx, NewTask(TextPart("Keep this task")))
	assert.ErrorIs(t, err, failure)
	assert.Equal(t, "", value)
	assert.Nil(t, result)
	assert.Equal(t, 2, len(recorder.batches))
	state := NewRunState(execCtx.RunId())
	for _, batch := range recorder.batches {
		applyRecordedState(t, state, codecs, batch)
	}
	fixed := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
		task, err := execCtx.Task()
		if err != nil {
			return nil, err
		}
		return acceptTurnText(execCtx, ContentText(task.Messages[0].Parts))
	}}
	next := NewRunner[string](fixed, &Config{Codecs: codecs, Recorder: recorder})
	restored, err := next.Restore(context.Background(), state)
	if !assert.NoError(t, err) || !assert.Equal(t, RestoreReady, restored.Status) {
		return
	}
	defer restored.Context.Release()
	value, result, err = next.Continue(restored.Context)
	assert.NoError(t, err)
	assert.Equal(t, "Keep this task", value)
	assert.Equal(t, execCtx.TurnId(), result.TurnId)
	assert.Equal(t, int64(1), restored.Context.Stats().GetCounter(SCIterations))
}
