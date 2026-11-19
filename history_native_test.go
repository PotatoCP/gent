package gent_test

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

type nativeRecoveryModel struct {
	requests []*gent.ModelCallRequest
	answer   bool
	code     bool
}

func (model *nativeRecoveryModel) GenerateContentStream(_ context.Context,
	request *gent.ModelCallRequest) (gent.Stream, error) {
	model.requests = append(model.requests, request)
	choice := recoveryAnswerChoice("Policy P10 permits it.")
	if !model.answer && len(model.requests) == 1 {
		choice = recoveryChoice()
		for i, id := range []string{"provider-a", "provider-b"} {
			choice.Parts[i+1].(*gent.ModelToolCallRequest).ID = id
		}
		if model.code {
			choice.Parts = []gent.ContentPart{&gent.ModelToolCallRequest{ID: "provider-code",
				Type: "function", FunctionCall: &gent.FunctionCall{Name: "run_code",
					Arguments: `{"code":"tool.call({tool:'policy',args:{id:'P10'}});"}`}}}
		}
	}
	return gent.NewCompletedStream(&gent.ModelCallResponse{Choices: []*gent.ContentChoice{choice},
		Info: &gent.GenerationInfo{InputTokens: 12, OutputTokens: 8, TotalTokens: 20}}, nil), nil
}

func newNativeRecoveryAgent(
	model *nativeRecoveryModel, calls *[]gent.ToolIdentity,
) (*react.Agent[string], *gent.Config) {
	var chain gent.ToolChain = toolchain.New().RegisterTool(gent.DefineTool("policy",
		func(ctx context.Context, input recoveryInput) (*gent.ToolExecutionResponse, error) {
			identity, _ := gent.ToolIdentityFromContext(ctx)
			*calls = append(*calls, identity)
			return capturedToolResponse(input.Id+": exact original text", nil)
		}).Description("Read the policy").ParameterSchema(nil).MustBuild())
	if model.code {
		chain = toolchain.NewJsToolChainWrapper(chain)
	}
	return react.NewAgent(model), &gent.Config{ToolChain: chain}
}

func TestNativeHistory_Recovery(t *testing.T) {
	type input struct {
		phase gent.ExecutionPhase
		event string
	}
	type expected struct {
		beforeModels, afterModels, beforeTools, afterTools int
		failure                                            error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"initial", input{phase: gent.PhaseInitial}, expected{0, 2, 0, 2, nil}},
		{"saved response", input{event: "model_call_finished"}, expected{1, 1, 0, 0, nil}},
		{"uncertain call", input{phase: gent.PhaseToolReady},
			expected{1, 1, 0, 0, nil}},
		{"first result", input{phase: gent.PhaseToolCompleted}, expected{1, 1, 1, 0, nil}},
		{"saved iteration", input{phase: gent.PhaseIterationCompleted}, expected{1, 1, 2, 0, nil}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			worker, cancel := context.WithCancel(context.Background())
			defer cancel()
			store := &recoveryStore{codecs: gent.NewCodecRegistry(), cancel: cancel,
				phase: test.input.phase, event: test.input.event}
			model := &nativeRecoveryModel{}
			var before []gent.ToolIdentity
			agent, config := newNativeRecoveryAgent(model, &before)
			config.Codecs, config.Recorder = store.codecs, store
			runner, execCtx := newTestExecution(t, worker, agent, config)
			_, _, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read the policy twice.")))
			assert.ErrorIs(t, err, context.Canceled)
			nextModel := &nativeRecoveryModel{answer: len(model.requests) > 0}
			var after []gent.ToolIdentity
			nextAgent, freshConfig := newNativeRecoveryAgent(nextModel, &after)
			restored, result, err := resumeRecovery(t, nextAgent, store.runStateSnapshot, freshConfig)
			if test.expected.failure != nil {
				assert.ErrorIs(t, err, test.expected.failure)
			} else if assert.NoError(t, err) {
				assert.Equal(t, []gent.ContentPart{gent.TextContent{Text: "Policy P10 permits it."}},
					result.Output)
				assert.Equal(t, int64(40), restored.Stats().GetCounter(gent.SCTotalTokens))
				expectedCalls := int64(test.expected.beforeTools + test.expected.afterTools)
				if test.name == "uncertain call" || test.input.event != "" {
					expectedCalls = 1
				}
				assert.Equal(t, expectedCalls, restored.Stats().GetCounter(gent.SCToolCalls))
				last := nextModel.requests[len(nextModel.requests)-1]
				assert.Equal(t, &gent.ModelMessage{Role: gent.ChatMessageTypeAI,
					Parts: []gent.ContentPart{&gent.AssistantContent{
						MessageId: last.Messages[1].Parts[0].(*gent.AssistantContent).MessageId,
						Type:      gent.AssistantContentCommentary,
						Content:   []gent.ContentPart{gent.TextPart("Read the policy.")}},
						&gent.ModelToolCallRequest{ID: "provider-a", Type: "function",
							FunctionCall: &gent.FunctionCall{
								Name: "policy", Arguments: `{"id":"P10"}`,
							}},
						&gent.ModelToolCallRequest{ID: "provider-b", Type: "function",
							FunctionCall: &gent.FunctionCall{
								Name: "policy", Arguments: `{"id":"P10"}`,
							}}}},
					last.Messages[1])
			}
			assert.Equal(t, test.expected, expected{len(model.requests), len(nextModel.requests),
				len(before), len(after), test.expected.failure})
		})
	}
}

func TestNativeHistory_JavaScriptRecoveryBoundary(t *testing.T) {
	type input struct{ completedCheckpoints int }
	type expected struct {
		failure           error
		resumedModelCalls int
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"inside program", input{1}, expected{nil, 1}},
		{"complete program", input{2}, expected{nil, 1}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			worker, cancel := context.WithCancel(context.Background())
			defer cancel()
			store := &recoveryStore{codecs: gent.NewCodecRegistry(), cancel: cancel,
				phase: gent.PhaseToolCompleted, after: test.input.completedCheckpoints}
			model := &nativeRecoveryModel{code: true}
			var effects []gent.ToolIdentity
			agent, config := newNativeRecoveryAgent(model, &effects)
			config.Codecs, config.Recorder = store.codecs, store
			runner, execCtx := newTestExecution(t, worker, agent, config)
			_, _, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read the policy.")))
			assert.ErrorIs(t, err, context.Canceled)
			nextModel := &nativeRecoveryModel{code: true, answer: true}
			var repeated []gent.ToolIdentity
			nextAgent, freshConfig := newNativeRecoveryAgent(nextModel, &repeated)
			_, result, err := resumeRecovery(t, nextAgent, store.runStateSnapshot, freshConfig)
			if test.expected.failure != nil {
				assert.ErrorIs(t, err, test.expected.failure)
			} else if assert.NoError(t, err) {
				assert.Equal(t, []gent.ContentPart{gent.TextContent{Text: "Policy P10 permits it."}},
					result.Output)
			}
			assert.Len(t, effects, 1)
			assert.Equal(t, []gent.ToolIdentity(nil), repeated)
			assert.Equal(t, test.expected.resumedModelCalls, len(nextModel.requests))
		})
	}
}
func (m *nativeRecoveryModel) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}
