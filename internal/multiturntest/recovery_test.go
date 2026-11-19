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

func TestAcceptedRecoverySurvivesAnotherInterruption(t *testing.T) {
	type input struct {
		status       gent.ToolRecoveryStatus
		fromSnapshot bool
	}
	type expected struct{ calls, checks, modelCalls int }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"completed batches", input{gent.ToolRecoveryCompleted, false}, expected{1, 1, 1}},
		{"completed saved state", input{gent.ToolRecoveryCompleted, true}, expected{1, 1, 1}},
		{"unresolved batches", input{gent.ToolRecoveryUnresolved, false}, expected{1, 1, 1}},
		{"unresolved saved state", input{gent.ToolRecoveryUnresolved, true}, expected{1, 1, 1}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			worker, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls, checks := 0, 0
			chain := toolchain.New()
			chain.RegisterTool(gent.DefineTool("write", func(ctx context.Context, input struct{}) (
				*gent.ToolExecutionResponse, error,
			) {
				calls++
				cancel()
				return nil, ctx.Err()
			}).Recovery(func(context.Context, *gent.ToolRecoveryRequest[struct{}]) (
				*gent.ToolRecoveryResponse, error,
			) {
				checks++
				if test.input.status == gent.ToolRecoveryCompleted {
					return &gent.ToolRecoveryResponse{Status: gent.ToolRecoveryCompleted,
						Execution: &gent.ToolExecutionResponse{Status: gent.ToolExecuted,
							Parts: []gent.ContentPart{gent.TextPart("Saved once")}}}, nil
				}
				return &gent.ToolRecoveryResponse{Status: gent.ToolRecoveryUnresolved,
					Instructions: "Inspect the external record before taking another action."}, nil
			}).MustBuild())
			firstModel := tt.NewMockModel().AddRawResponse(&gent.ModelCallResponse{
				Choices: []*gent.ContentChoice{{Completion: gent.ModelCompletion{
					Status: gent.ModelStatusToolCalls}, Parts: []gent.ContentPart{&gent.ModelToolCallRequest{
					ID: "write-1", Type: "function", FunctionCall: &gent.FunctionCall{
						Name: "write", Arguments: `{}`}}}}},
			})
			codecs := gent.NewCodecRegistry()
			recorder := &memoryRecorder{codecs: codecs}
			config := &gent.Config{Codecs: codecs, Recorder: recorder, ToolChain: chain}
			first := executor.NewRunner(react.NewAgent(firstModel), config)
			execCtx, err := first.NewExecutionContext(worker, "recovery")
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			_, result, err := first.NewTurn(execCtx, gent.NewTask(gent.TextPart("Save a record")))
			assert.ErrorIs(t, err, context.Canceled)
			assert.Nil(t, result)
			assert.Equal(t, gent.PhaseToolReady, recorder.phases[len(recorder.phases)-1])
			restoreState := func() *gent.RunState {
				state := gent.NewRunState(execCtx.RunId())
				start := 0
				if test.input.fromSnapshot {
					loaded, err := codecs.UnmarshalRunState(recorder.states[1])
					if !assert.NoError(t, err) || !assert.Equal(t, gent.RunStateReady, loaded.Status) {
						t.FailNow()
					}
					state, start = loaded.State, 2
				}
				for _, data := range recorder.batches[start:] {
					var batch gent.SaveBatch
					if !assert.NoError(t, codecs.Unmarshal(data, &batch)) {
						t.FailNow()
					}
					applied, err := state.Apply(&batch, codecs)
					if !assert.NoError(t, err) || !assert.Equal(t,
						&gent.ApplyStateResult{Status: gent.ApplyApplied}, applied) {
						t.FailNow()
					}
				}
				return state
			}
			nextModel := tt.NewMockModel().AddResponse("Complete", 10, 1)
			next := executor.NewRunner(react.NewAgent(nextModel), config)
			worker2, cancel2 := context.WithCancel(context.Background())
			defer cancel2()
			restored, err := next.Restore(worker2, restoreState())
			if !assert.NoError(t, err) || !assert.Equal(t, gent.RestoreReady, restored.Status) {
				return
			}
			defer restored.Context.Release()
			recorder.afterSave = func(batch *gent.SaveBatch) {
				if batch.Checkpoint.Phase == gent.PhaseRecoveryCompleted {
					cancel2()
				}
			}
			_, result, err = next.Continue(restored.Context)
			assert.ErrorIs(t, err, context.Canceled)
			assert.Nil(t, result)
			assert.Equal(t, gent.PhaseRecoveryCompleted, recorder.phases[len(recorder.phases)-1])
			assert.Equal(t, 0, nextModel.CallCount())
			prepared, err := restored.Context.Scratchpad()
			if !assert.NoError(t, err) {
				return
			}
			recorder.afterSave = nil
			third, err := next.Restore(context.Background(), restoreState())
			if !assert.NoError(t, err) || !assert.Equal(t, gent.RestoreReady, third.Status) {
				return
			}
			defer third.Context.Release()
			before, err := third.Context.Scratchpad()
			assert.NoError(t, err)
			assert.Equal(t, prepared, before)
			value, result, err := next.Continue(third.Context)
			if !assert.NoError(t, err) {
				return
			}
			assert.Equal(t, "Complete", value)
			assert.Equal(t, gent.ExecutionFinished, result.Status)
			assert.Equal(t, test.expected.calls, calls)
			assert.Equal(t, test.expected.checks, checks)
			assert.Equal(t, test.expected.modelCalls, nextModel.CallCount())
			recoverySaves := 0
			for _, phase := range recorder.phases {
				if phase == gent.PhaseRecoveryCompleted {
					recoverySaves++
				}
			}
			assert.Equal(t, 1, recoverySaves)
			var expectedMessages []*gent.ModelMessage
			for _, entry := range prepared {
				for _, message := range entry.Messages {
					expectedMessages = append(expectedMessages, &gent.ModelMessage{
						Role: message.Role, Parts: message.Parts, Continuation: message.Continuation})
				}
			}
			assert.Equal(t, [][]*gent.ModelMessage{expectedMessages}, nextModel.CapturedMessages)
		})
	}
}
