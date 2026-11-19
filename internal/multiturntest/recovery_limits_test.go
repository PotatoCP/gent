package multiturntest

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/internal/tt"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

func TestSavedLimitsBlockRecoveryBeforeTaskReplacement(t *testing.T) {
	type input struct {
		replace bool
		scope   gent.LimitScope
	}
	type expected struct {
		calls, checks, modelCalls int
		value                     string
		err                       error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"Continue turn limit", input{false, gent.LimitTurn}, expected{calls: 1}},
		{"Continue run limit", input{false, gent.LimitRun}, expected{calls: 1}},
		{"NewTurn turn limit", input{true, gent.LimitTurn}, expected{calls: 1}},
		{"NewTurn run limit", input{true, gent.LimitRun}, expected{calls: 1}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls, checks := 0, 0
			chain := toolchain.New().RegisterTool(gent.DefineTool("write",
				func(ctx context.Context, _ struct{}) (*gent.ToolExecutionResponse, error) {
					calls++
					cancel()
					return nil, ctx.Err()
				}).Recovery(func(context.Context, *gent.ToolRecoveryRequest[struct{}]) (
				*gent.ToolRecoveryResponse, error,
			) {
				checks++
				return &gent.ToolRecoveryResponse{Status: gent.ToolRecoveryUnresolved,
					Instructions: "Check the saved effect."}, nil
			}).MustBuild())
			firstModel := tt.NewMockModel().AddRawResponse(&gent.ModelCallResponse{
				Choices: []*gent.ContentChoice{{Completion: gent.ModelCompletion{
					Status: gent.ModelStatusToolCalls}, Parts: []gent.ContentPart{
					&gent.ModelToolCallRequest{ID: "write-1", Type: "function",
						FunctionCall: &gent.FunctionCall{Name: "write", Arguments: `{}`}},
				}}},
			})
			codecs := gent.NewCodecRegistry()
			recorder := &memoryRecorder{codecs: codecs}
			first := executor.NewRunner(react.NewAgent(firstModel), &gent.Config{
				Codecs: codecs, Recorder: recorder, ToolChain: chain})
			execCtx, err := first.NewExecutionContext(ctx, test.name)
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			task := gent.NewTask(gent.TextPart("Save the first record."))
			_, result, err := first.NewTurn(execCtx, task)
			if !assert.ErrorIs(t, err, context.Canceled) {
				return
			}
			assert.Nil(t, result)
			turnId := execCtx.TurnId()
			rule := gent.Limit{Type: gent.LimitExactKey, Key: gent.SCToolCalls,
				MaxValue: 0, Scope: test.input.scope}
			model := tt.NewMockModel().AddResponse("Done", 0, 0)
			next := executor.NewRunner(react.NewAgent(model), &gent.Config{
				Codecs: codecs, Recorder: recorder, ToolChain: chain, Limits: []gent.Limit{rule}})
			loaded, err := codecs.UnmarshalRunState(recorder.states[len(recorder.states)-1])
			if !assert.NoError(t, err) || !assert.Equal(t, gent.RunStateReady, loaded.Status) {
				return
			}
			restored, err := next.Restore(context.Background(), loaded.State)
			if !assert.NoError(t, err) || !assert.Equal(t, gent.RestoreReady, restored.Status) {
				return
			}
			defer restored.Context.Release()
			var value string
			if test.input.replace {
				value, result, err = next.NewTurn(restored.Context,
					gent.NewTask(gent.TextPart("Start a different task.")))
			} else {
				value, result, err = next.Continue(restored.Context)
			}
			assert.Equal(t, test.expected, expected{calls, checks, model.CallCount(), value, err})
			assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionPaused, TurnId: turnId,
				TerminationReason: gent.TerminationLimitExceeded, ExceededLimit: &gent.LimitExceeded{
					Limit: &rule, MatchedKey: gent.SCToolCalls, CurrentValue: 1}}, result)
			assert.Equal(t, turnId, restored.Context.TurnId())
			savedTask, err := restored.Context.Task()
			assert.NoError(t, err)
			assert.Equal(t, task, savedTask)
			lastValue, lastResult, err := next.GetLastTurnResult(restored.Context)
			assert.NoError(t, err)
			assert.Equal(t, "", lastValue)
			assert.Nil(t, lastResult, "the blocked replacement must not close the previous task")
			assert.Equal(t, []gent.ExecutionPhase{gent.PhaseInitial, gent.PhaseIterationReady,
				gent.PhaseToolReady, gent.PhasePaused}, recorder.phases)
		})
	}
}
