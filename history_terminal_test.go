package gent_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

func TestHistoryRecovery_UnexpectedFailureKeepsAcceptedTurn(t *testing.T) {
	lookupErr := fmt.Errorf("lookup failed: %w", gent.ErrHistoryConflict)
	stopErr := errors.New("worker lease ended")
	type input struct{ loopErr, ownerCause error }
	type expected struct{ cause error }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"loop failure", input{loopErr: lookupErr}, expected{gent.ErrHistoryConflict}},
		{"owner cancellation", input{ownerCause: context.Canceled}, expected{context.Canceled}},
		{"owner cause", input{ownerCause: stopErr}, expected{stopErr}},
	} {
		t.Run(
			test.name,
			func(t *testing.T) {
				owner, cancel := context.WithCancelCause(context.Background())
				defer cancel(nil)
				calls := 0
				loop := contentLoop(func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
					calls++
					if calls == 1 {
						return acceptRootOutput(execCtx, "Earlier answer.")
					}
					if test.input.ownerCause != nil {
						cancel(test.input.ownerCause)
						return nil, execCtx.Context().Err()
					}
					return nil, test.input.loopErr
				})
				store := newCheckpointStore()
				runner, execCtx := newTestExecution(
					t,
					owner,
					loop,
					&gent.Config{
						Codecs:   store.codecs,
						Recorder: store,
					},
				)
				_, prior, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("First task.")))
				if !assert.NoError(t, err) {
					return
				}
				value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Second task.")))
				assert.ErrorIs(t, err, test.expected.cause)
				assert.Empty(t, value)
				assert.Nil(t, result)
				assert.Equal(t, gent.PhaseIterationReady, store.view.Checkpoint.Phase)
				assert.Nil(t, store.view.Checkpoint.Outcome)
				assert.Equal(t, prior, store.view.Checkpoint.LastOutcome.Result)
				savedResult, saved, err := runner.GetLastTurnResult(execCtx)
				assert.NoError(t, err)
				assert.Equal(t, "Earlier answer.", savedResult)
				assert.Equal(t, prior, saved)
				fixed := contentLoop(func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
					return acceptRootOutput(execCtx, "Fixed answer.")
				})
				freshRunner, fresh := restoreTestExecution(t, fixed, nil, store.runStateSnapshot)
				assert.Equal(t, execCtx.TurnId(), fresh.TurnId())
				value, result, err = freshRunner.Continue(fresh)
				assert.NoError(t, err)
				assert.Equal(t, "Fixed answer.", value)
				assert.Equal(t, gent.ExecutionFinished, result.Status)
				assert.Equal(t, 2, calls)
			},
		)
	}
}

func TestHistoryRecovery_PanicRequiresRestoration(t *testing.T) {
	loop := contentLoop(func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
		execCtx.Stats().IncrCounter("app:provisional", 1)
		panic("unexpected application panic")
	})
	store := newCheckpointStore()
	runner, execCtx := newTestExecution(t, context.Background(), loop,
		&gent.Config{Codecs: store.codecs, Recorder: store})
	assert.PanicsWithValue(t, "unexpected application panic", func() {
		_, _, _ = runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Run a task.")))
	})
	_, result, err := runner.Continue(execCtx)
	assert.Error(t, err)
	assert.Nil(t, result)
	fixed := contentLoop(func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
		assert.Equal(t, int64(0), execCtx.Stats().GetCounter("app:provisional"))
		return acceptRootOutput(execCtx, "Recovered.")
	})
	freshRunner, fresh := restoreTestExecution(t, fixed, nil, store.runStateSnapshot)
	value, result, err := freshRunner.Continue(fresh)
	assert.NoError(t, err)
	assert.Equal(t, "Recovered.", value)
	assert.Equal(t, gent.ExecutionFinished, result.Status)
}
