package gent_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/format"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

func TestHistoryRecovery_UncertainTool(t *testing.T) {
	type input struct{ decision gent.ToolRecoveryStatus }
	type expected struct {
		err        error
		toolCalls  int
		modelCalls int
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"default tool recovery", input{}, expected{nil, 0, 1}},
		{"unresolved", input{gent.ToolRecoveryUnresolved}, expected{nil, 0, 1}},
		{"retry authorized", input{gent.ToolRecoveryNotStarted}, expected{nil, 0, 1}},
		{"completed externally", input{gent.ToolRecoveryCompleted}, expected{nil, 0, 1}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			bundle, _, priorCalls := saveInterruptedRun(t, gent.PhaseToolReady, "")
			assert.Empty(t, priorCalls)
			model := &recoveryModel{answer: true}
			var calls []gent.ToolIdentity
			var recovered *gent.ToolRecoveryRequest[recoveryInput]
			var handler gent.ToolRecoveryFunc[recoveryInput]
			if test.input.decision != "" {
				handler = func(_ context.Context, request *gent.ToolRecoveryRequest[recoveryInput]) (
					*gent.ToolRecoveryResponse, error,
				) {
					recovered = request
					assert.Equal(t, recoveryInput{Id: "P10"}, request.Input())
					response := &gent.ToolRecoveryResponse{Status: test.input.decision}
					if response.Status == gent.ToolRecoveryCompleted {
						response.Execution = &gent.ToolExecutionResponse{Status: gent.ToolExecuted,
							Parts: []gent.ContentPart{gent.TextPart("P10: exact original text")}}
					}
					return response, nil
				}
			}
			agent, config := newRecoveryAgent(model, &calls, handler)
			_, result, err := resumeRecovery(t, agent, bundle, config)
			assert.ErrorIs(t, err, test.expected.err)
			assert.Len(t, calls, test.expected.toolCalls)
			assert.Len(t, model.requests, test.expected.modelCalls)
			assert.Equal(t, gent.ExecutionFinished, result.Status)
			if recovered != nil {
				assert.Equal(t, "policy", recovered.Name())
			}
		})
	}
}

type rejectRecorder struct {
	event string
	phase gent.ExecutionPhase
	err   error
}

func (r rejectRecorder) Save(
	_ context.Context, batch *gent.SaveBatch,
	captureRunStateSnapshot func() (*gent.RunState, error),
) error {
	if batch.Checkpoint != nil && batch.Checkpoint.Phase == r.phase {
		return r.err
	}
	for _, event := range batch.Events {
		if event.Type == r.event {
			return r.err
		}
	}
	return nil
}

func TestHistoryRecorderFailure_PreventsSideEffects(t *testing.T) {
	type input struct {
		event string
		phase gent.ExecutionPhase
	}
	type expected struct{ models, tools int }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"initial checkpoint", input{phase: gent.PhaseInitial}, expected{0, 0}},
		{"model request grouped before tool", input{event: "model_call_started"}, expected{1, 0}},
		{"iteration checkpoint", input{phase: gent.PhaseIterationReady}, expected{0, 0}},
		{"tool readiness", input{phase: gent.PhaseToolReady}, expected{1, 0}},
		{"first tool result", input{phase: gent.PhaseToolCompleted}, expected{1, 1}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			saveErr := errors.New("writer lease expired")
			recorder := rejectRecorder{test.input.event, test.input.phase, saveErr}
			model := &recoveryModel{}
			var calls []gent.ToolIdentity
			agent, config := newRecoveryAgent(model, &calls)
			config.Recorder = recorder
			runner, execCtx := newTestExecution(t, context.Background(), agent, config)
			_, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("policy question")))
			assert.Nil(t, result)
			assert.ErrorIs(t, err, saveErr)
			assert.Equal(t, test.expected, expected{len(model.requests), len(calls)})
		})
	}
}

type toolInputHook struct {
	value string
	calls int
}

func (h *toolInputHook) BeforeToolCall(
	_ context.Context,
	event *gent.BeforeToolCallEvent,
) error {
	h.calls++
	event.Args = recoveryInput{Id: h.value}
	return nil
}

func TestToolRecovery_ReusesEffectiveInput(t *testing.T) {
	worker, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &recoveryStore{
		codecs: gent.NewCodecRegistry(),
		cancel: cancel,
		phase:  gent.PhaseToolReady,
	}
	var got []recoveryInput
	newChain := func() *toolchain.Registry {
		chain := toolchain.New()
		chain.RegisterTool(gent.DefineTool("policy",
			func(_ context.Context, input recoveryInput) (*gent.ToolExecutionResponse, error) {
				got = append(got, input)
				return capturedToolResponse(fmt.Sprintf("policy %s", input.Id), nil)
			}).Description("lookup").ParameterSchema(nil).Recovery(
			func(_ context.Context, request *gent.ToolRecoveryRequest[recoveryInput]) (
				*gent.ToolRecoveryResponse, error,
			) {
				got = append(got, request.Input())
				return &gent.ToolRecoveryResponse{Status: gent.ToolRecoveryNotStarted}, nil
			}).MustBuild())
		return chain
	}
	chain := newChain()
	loop := &contentLoopValue{
		next: func(
			execCtx *gent.ExecutionContext,
		) (
			*gent.AgentLoopResult,
			error,
		) {
			plan, exists, err := execCtx.SavedToolPlan()
			if err != nil {
				return nil, err
			}
			if !exists {
				plan, err = execCtx.Config().ToolChain.PrepareCalls(execCtx,
					[]*gent.ModelToolCallRequest{{ID: "policy", Type: "function",
						FunctionCall: &gent.FunctionCall{Name: "policy", Arguments: `{"id":"original"}`}}})
				if err != nil {
					return nil, err
				}
			}
			if plan.NextCall < len(plan.Calls) {
				_, err = execCtx.Config().ToolChain.ExecuteNext(execCtx, plan, format.NewXML())
			}
			if err != nil {
				return nil, err
			}
			return acceptRootOutput(execCtx, "Done")
		},
	}
	hook := &toolInputHook{value: "effective"}
	runner, execCtx := newTestExecution(t, worker, loop, &gent.Config{Codecs: store.codecs,
		Recorder: store, ToolChain: chain, Hooks: &gent.Hooks{BeforeToolCall: hook.BeforeToolCall}})
	_, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read a policy.")))
	assert.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, result)
	assert.Empty(t, got)
	freshHook := &toolInputHook{value: "would change the saved input"}
	freshRunner, fresh := restoreTestExecution(t, loop, &gent.Config{ToolChain: newChain(),
		Hooks: &gent.Hooks{BeforeToolCall: freshHook.BeforeToolCall}}, store.runStateSnapshot)
	_, result, err = freshRunner.Continue(fresh)
	assert.NoError(t, err)
	assert.Equal(t, gent.ExecutionFinished, result.Status)
	assert.Equal(t, []recoveryInput{{Id: "effective"}}, got)
	assert.Equal(t, 1, hook.calls)
	assert.Equal(t, 0, freshHook.calls)
}
