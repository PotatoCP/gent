package gent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRunnerTurnAndRunLimits(t *testing.T) {
	type input struct {
		scope   LimitScope
		replace bool
	}
	type expected struct {
		firstCalls, finalCalls int
		turnUse, runUse        int64
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"turn Continue", input{LimitTurn, false}, expected{2, 2, 2, 2}},
		{"run Continue", input{LimitRun, false}, expected{2, 2, 2, 2}},
		{"turn NewTurn", input{LimitTurn, true}, expected{2, 4, 2, 4}},
		{"run NewTurn", input{LimitRun, true}, expected{2, 2, 0, 2}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				calls++
				return &AgentLoopResult{Action: LAContinue}, nil
			}}
			rule := Limit{Type: LimitExactKey, Key: SCIterations, MaxValue: 2, Scope: test.input.scope}
			codecs := NewCodecRegistry()
			recorder := &runStateRecorder{codecs: codecs, capture: true}
			runner := NewRunner[string](loop, &Config{Limits: []Limit{rule}, Codecs: codecs,
				Recorder: recorder})
			execCtx, err := runner.NewExecutionContext(context.Background(), "limits")
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			value, result, err := runner.NewTurn(execCtx, NewTask(TextPart("First task")))
			if !assert.NoError(t, err) {
				return
			}
			assert.Equal(t, "", value)
			assert.Equal(t, &ExecutionResult{Status: ExecutionPaused, TurnId: execCtx.TurnId(),
				TerminationReason: TerminationLimitExceeded, ExceededLimit: &LimitExceeded{
					Limit: &rule, MatchedKey: SCIterations, CurrentValue: 2}}, result)
			assert.Equal(t, test.expected.firstCalls, calls)
			loaded, err := codecs.UnmarshalRunState(encodeRunState(t, codecs,
				recorder.states[len(recorder.states)-1]))
			if !assert.NoError(t, err) || !assert.Equal(t, RunStateReady, loaded.Status) {
				return
			}
			restored, err := runner.Restore(context.Background(), loaded.State)
			if !assert.NoError(t, err) || !assert.Equal(t, RestoreReady, restored.Status) {
				return
			}
			defer restored.Context.Release()
			if test.input.replace {
				value, result, err = runner.NewTurn(restored.Context, NewTask(TextPart("Second task")))
			} else {
				value, result, err = runner.Continue(restored.Context)
			}
			if !assert.NoError(t, err) {
				return
			}
			assert.Equal(t, "", value)
			assert.Equal(t, &ExecutionResult{Status: ExecutionPaused, TurnId: restored.Context.TurnId(),
				TerminationReason: TerminationLimitExceeded, ExceededLimit: &LimitExceeded{
					Limit: &rule, MatchedKey: SCIterations, CurrentValue: 2}}, result)
			assert.Equal(t, test.expected.finalCalls, calls)
			assert.Equal(t, test.expected.runUse, restored.Context.Stats().GetCounter(SCIterations))
			assert.Equal(t, test.expected.turnUse, restored.Context.Stats().GetTurnCounter(SCIterations))
		})
	}
}

func TestRunnerContinuesAfterLimitConfigurationChange(t *testing.T) {
	calls := 0
	loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
		calls++
		if calls == 2 {
			return acceptTurnText(execCtx, "Complete")
		}
		return &AgentLoopResult{Action: LAContinue}, nil
	}}
	codecs := NewCodecRegistry()
	recorder := &runStateRecorder{codecs: codecs, capture: true}
	first := NewRunner[string](loop, &Config{Recorder: recorder, Codecs: codecs,
		Limits: []Limit{{Type: LimitExactKey, Key: SCIterations, MaxValue: 1}}})
	execCtx, err := first.NewExecutionContext(context.Background(), "increase limit")
	if !assert.NoError(t, err) {
		return
	}
	defer execCtx.Release()
	_, result, err := first.NewTurn(execCtx, NewTask(TextPart("Complete two steps")))
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, ExecutionPaused, result.Status)
	turnId := execCtx.TurnId()
	loaded, err := codecs.UnmarshalRunState(encodeRunState(t, codecs,
		recorder.states[len(recorder.states)-1]))
	if !assert.NoError(t, err) {
		return
	}
	next := NewRunner[string](loop, &Config{Recorder: recorder, Codecs: codecs,
		Limits: []Limit{{Type: LimitExactKey, Key: SCIterations, MaxValue: 3}}})
	restored, err := next.Restore(context.Background(), loaded.State)
	if !assert.NoError(t, err) || !assert.Equal(t, RestoreReady, restored.Status) {
		return
	}
	defer restored.Context.Release()
	value, result, err := next.Continue(restored.Context)
	assert.NoError(t, err)
	assert.Equal(t, "Complete", value)
	assert.Equal(t, &ExecutionResult{Status: ExecutionFinished, TurnId: turnId,
		TerminationReason: TerminationSuccess, Output: []ContentPart{TextPart("Complete")}}, result)
	assert.Equal(t, 2, calls)
	assert.Equal(t, int64(2), restored.Context.Stats().GetTurnCounter(SCIterations))
}
