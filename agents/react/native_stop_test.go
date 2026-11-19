package react

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

func TestNativeAgent_CompletionStates(t *testing.T) {
	type input struct {
		status gent.ModelStatus
		tools  bool
	}
	type expected struct {
		value  string
		result *gent.ExecutionResult
		calls  int
		err    error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	limit := &gent.Limit{Type: gent.LimitExactKey, Key: gent.SCIterations, MaxValue: 1}
	paused := func(calls int) expected {
		return expected{calls: calls, result: &gent.ExecutionResult{
			Status: gent.ExecutionPaused, TerminationReason: gent.TerminationLimitExceeded,
			ExceededLimit: &gent.LimitExceeded{Limit: limit, MatchedKey: gent.SCIterations, CurrentValue: 1},
		}}
	}
	cases := []testCase{
		{"incomplete tools", input{gent.ModelStatusIncomplete, true}, paused(0)},
		{"incomplete answer", input{gent.ModelStatusIncomplete, false}, paused(0)},
		{"completed tools", input{gent.ModelStatusToolCalls, true}, paused(1)},
		{"completed answer", input{gent.ModelStatusAnswer, false}, expected{value: "Done.",
			result: &gent.ExecutionResult{Status: gent.ExecutionFinished,
				TerminationReason: gent.TerminationSuccess,
				Output:            []gent.ContentPart{gent.TextPart("Done.")}}}},
		{"refused with calls", input{gent.ModelStatusRefused, true}, expected{
			result: &gent.ExecutionResult{Status: gent.ExecutionFinished,
				TerminationReason: gent.TerminationRefused,
				Refusal:           &gent.ModelRefusal{Code: "blocked"}}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			choice := answerChoice("Done.")
			choice.Completion.Status = test.input.status
			if test.input.status == gent.ModelStatusRefused {
				choice.Completion.Refusal = &gent.ModelRefusal{Code: "blocked"}
			}
			if test.input.tools {
				choice.Parts = []gent.ContentPart{nativeTestCall("call-a", "lookup", "{}")}
				choice.CandidateSource = nil
			}
			model := newMockModel(nativeTestResponse(choice))
			calls := 0
			chain := toolchain.New().RegisterTool(gent.DefineTool("lookup",
				func(context.Context, map[string]any) (*gent.ToolExecutionResponse, error) {
					calls++
					return capturedToolResponse("record", nil)
				}).Description("Read").ParameterSchema(nil).MustBuild())
			var limits []gent.Limit
			if test.expected.result.Status == gent.ExecutionPaused {
				limits = []gent.Limit{*limit}
			}
			runner, execCtx := newTestExecution(t, NewAgent(model), &gent.Config{
				ToolChain: chain, Limits: limits,
			})
			value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read.")))
			test.expected.result.TurnId = execCtx.TurnId()
			assert.Equal(t, test.expected, expected{value, result, calls, err})
			assert.Equal(t, int64(test.expected.calls), execCtx.Stats().GetCounter(gent.SCToolCalls))
		})
	}
}
