package gent_test

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

func TestRecoveryAllowsChangedToolConfiguration(t *testing.T) {
	type input struct{ search, program bool }
	type expected struct {
		status        gent.ExecutionStatus
		reason        gent.TerminationReason
		toolCalls     int
		modelRequests int
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{

		{
			"registry",
			input{},
			expected{
				status:        gent.ExecutionFinished,
				reason:        gent.TerminationSuccess,
				modelRequests: 1,
			},
		},
		{
			"search",
			input{
				search: true,
			},
			expected{
				status:        gent.ExecutionFinished,
				reason:        gent.TerminationSuccess,
				modelRequests: 1,
			},
		},
		{
			"program",
			input{
				program: true,
			},
			expected{
				status:        gent.ExecutionFinished,
				reason:        gent.TerminationSuccess,
				modelRequests: 1,
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			toolCalls := 0
			build := func(model *guidanceModel, description string) (*react.Agent[string], *gent.Config) {
				tool := &recoverySearchTool{gent.DefineTool("book",
					func(context.Context, *bookingInput) (*gent.ToolExecutionResponse, error) {
						toolCalls++
						return capturedToolResponse("booked", nil)
					}).Description(description).ParameterSchema(nil).
					Recovery(func(context.Context, *gent.ToolRecoveryRequest[*bookingInput]) (
						*gent.ToolRecoveryResponse, error,
					) {
						return &gent.ToolRecoveryResponse{Status: gent.ToolRecoveryUnresolved,
							Instructions: "Check the booking service."}, nil
					}).MustBuild()}
				var chain gent.ToolChain = toolchain.New().RegisterTool(tool)
				if test.input.search {
					search := toolchain.NewSearch(toolchain.SearchHintSimpleList).
						RegisterEngine(toolchain.NewRegexToolSearcher()).Pin("book")
					search.RegisterTool(tool)
					if !assert.NoError(t, search.Initialize()) {
						t.FailNow()
					}
					chain = search
				}
				if test.input.program {
					chain = toolchain.NewJsToolChainWrapper(chain)
				}
				return react.NewAgent(model), &gent.Config{ToolChain: chain}
			}
			worker, cancel := context.WithCancel(context.Background())
			defer cancel()
			store := &recoveryStore{codecs: gent.NewCodecRegistry(), cancel: cancel,
				phase: gent.PhaseInitial}
			model := &guidanceModel{finish: true}
			agent, config := build(model, "Original booking description")
			config.Codecs, config.Recorder = store.codecs, store
			runner, execCtx := newTestExecution(t, worker, agent, config)
			_, _, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Book a flight.")))
			if !assert.ErrorIs(t, err, context.Canceled) {
				return
			}
			assert.Equal(t, []*gent.ModelCallRequest(nil), model.requests)
			freshModel := &guidanceModel{finish: true}
			freshAgent, freshConfig := build(freshModel, "Updated booking description")
			_, result, err := resumeRecovery(t, freshAgent, store.runStateSnapshot, freshConfig)
			if !assert.NoError(t, err) {
				return
			}
			assert.Equal(t, test.expected, expected{result.Status, result.TerminationReason,
				toolCalls, len(freshModel.requests)})

		})
	}
}
