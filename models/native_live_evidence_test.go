package models

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/stretchr/testify/assert"
)

type liveEvidenceModel struct{ choices []*gent.ContentChoice }

func (m *liveEvidenceModel) GenerateContentStream(context.Context, *gent.ModelCallRequest) (
	gent.Stream, error,
) {
	if len(m.choices) == 0 {
		return nil, fmt.Errorf("fixture has no remaining model response")
	}
	choice := m.choices[0]
	m.choices = m.choices[1:]
	response := &gent.ModelCallResponse{Choices: []*gent.ContentChoice{choice}}
	return gent.NewCompletedStream(response, nil), nil
}

func TestNativeLiveProgramEvidence(t *testing.T) {
	type input struct {
		programs []string
		direct   bool
	}
	type expected struct {
		calls                []string
		programs             [][]liveProgramCall
		output               []gent.ContentPart
		reason               gent.TerminationReason
		codeRuns, codeErrors int64
		accepted             bool
		err                  error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	const program = `(function() {
const first = tool.call({tool:"lookup",args:{id:"start"}});
if (first.kind !== "execution") throw new Error("lookup unavailable");
const data = first.parts.find(part => part.kind === "data").data;
const second = tool.call({tool:"read",args:{id:data.next_id}});
if (second.kind !== "execution") throw new Error("read unavailable");
})();`
	calls := []string{"lookup:start", "read:record-9"}
	output := []gent.ContentPart{gent.TextContent{Text: "record-finished"}}
	lookup := liveProgramCall{"lookup", &responseInput{ID: "start"}, `{"next_id":"record-9"}`, nil}
	read := liveProgramCall{"read", &responseInput{ID: "record-9"}, "record-finished", nil}
	programs := [][]liveProgramCall{{lookup, read}}
	cases := []testCase{
		{"valid program", input{[]string{program}, false},
			expected{calls, programs, output, gent.TerminationSuccess, 1, 0, true, nil}},
		{"correct undefined function", input{[]string{`lookup({id:"start"});`, program}, false},
			expected{calls, programs, output, gent.TerminationSuccess, 2, 1, true, nil}},
		{"correct top-level return", input{[]string{`return;`, program}, false},
			expected{calls, programs, output, gent.TerminationSuccess, 2, 1, true, nil}},
		{"correct Promise after lookup", input{[]string{
			`tool.call({tool:"lookup",args:{id:"start"}}).then(() => {});`, program,
		}, false}, expected{[]string{"lookup:start", "lookup:start", "read:record-9"},
			[][]liveProgramCall{{lookup}, {lookup, read}}, output,
			gent.TerminationSuccess, 2, 1, true, nil}},
		{"reject no-op program then direct calls", input{[]string{`console.log("no tools");`}, true},
			expected{calls, nil, output, gent.TerminationSuccess, 1, 0, false, nil}},
		{"reject direct calls only", input{nil, true},
			expected{calls, nil, output, gent.TerminationSuccess, 0, 0, false, nil}},
		{"reject calls in separate programs", input{[]string{
			`tool.call({tool:"lookup",args:{id:"start"}});`,
			`tool.call({tool:"read",args:{id:"record-9"}});`,
		}, false}, expected{calls, [][]liveProgramCall{{lookup}, {read}}, output,
			gent.TerminationSuccess, 2, 0, false, nil}},
	}
	for _, test := range cases {
		t.Run(
			test.name,
			func(t *testing.T) {
				model := &liveEvidenceModel{}
				addCall := func(name string, args map[string]any) {
					data, err := json.Marshal(args)
					if !assert.NoError(t, err) {
						t.FailNow()
					}
					model.choices = append(
						model.choices,
						&gent.ContentChoice{
							Completion: gent.ModelCompletion{

								Status: gent.ModelStatusToolCalls,
							},
							Parts: []gent.ContentPart{
								&gent.ModelToolCallRequest{
									ID: fmt.Sprintf(
										"call-%d",
										len(
											model.choices,
										),
									),
									Type:         "function",
									FunctionCall: &gent.FunctionCall{Name: name, Arguments: string(data)},
								},
							},
						},
					)
				}
				addCall("tool_registry_search", map[string]any{"query": "lookup|read", "query_type": "regex"})
				for _, code := range test.input.programs {
					addCall("run_code", map[string]any{"code": code})
				}
				if test.input.direct {
					addCall("lookup", map[string]any{"id": "start"})
					addCall("read", map[string]any{"id": "record-9"})
				}
				model.choices = append(
					model.choices,
					&gent.ContentChoice{

						Completion: gent.ModelCompletion{
							Status: gent.ModelStatusAnswer,
						},
						CandidateSource: &gent.ContentSource{
							MessageIds: []string{
								"answer",
							},
						},
						Parts: []gent.ContentPart{
							&gent.AssistantContent{
								Type:      gent.AssistantContentFinalAnswerCandidate,
								MessageId: "answer",
								Content: []gent.ContentPart{
									gent.TextContent{
										Text: "record-finished",
									},
								},
							},
						},
					},
				)
				actual := expected{}
				chain, err := liveToolChain(&actual.calls, true)
				if !assert.NoError(t, err) {
					return
				}
				loop := react.NewAgent(model)
				runner, execCtx := newModelExecution(t, context.Background(), loop, &gent.Config{
					ToolChain: chain, Limits: []gent.Limit{{Type: gent.LimitExactKey,
						Key: gent.SCIterations, MaxValue: 6}}})
				_, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart(
					"Search, then use code to read the record.")))
				if !assert.NoError(t, err) {
					return
				}
				actual.output, actual.reason = result.Output, result.TerminationReason
				actual.programs, actual.err = liveProgramCalls(execCtx)
				actual.codeRuns = execCtx.Stats().GetCounter(gent.SCCodeExecutions)
				actual.codeErrors = execCtx.Stats().GetCounter(gent.SCCodeExecutionsError)
				actual.accepted = validLiveProgramEvidence(actual.calls, actual.programs, programs[0])
				assert.Equal(t, test.expected, actual)
			},
		)
	}
}
func (m *liveEvidenceModel) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}

func TestNativeLiveProgramValidation(t *testing.T) {
	type input struct {
		calls    []string
		programs [][]liveProgramCall
	}
	type expected struct{ accepted bool }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	lookup := liveProgramCall{"lookup", &responseInput{ID: "start"}, `{"next_id":"record-9"}`, nil}
	read := liveProgramCall{"read", &responseInput{ID: "record-9"}, "record-finished", nil}
	want := []liveProgramCall{lookup, read}
	calls := []string{"lookup:start", "read:record-9"}
	wrong := liveProgramCall{"lookup", &responseInput{ID: "wrong"}, `{"next_id":"record-9"}`, nil}
	changed := liveProgramCall{"lookup", &responseInput{ID: "start"}, `{"next_id":"other"}`, nil}
	cases := []testCase{
		{"complete program", input{calls, [][]liveProgramCall{want}}, expected{true}},
		{"partial correction kept", input{[]string{"lookup:start", "lookup:start", "read:record-9"},
			[][]liveProgramCall{{lookup}, want}}, expected{true}},
		{"lost earlier operation", input{[]string{"lookup:start", "lookup:start", "read:record-9"},
			[][]liveProgramCall{want}}, expected{false}},
		{"no complete program", input{calls, [][]liveProgramCall{{lookup}, {read}}}, expected{false}},
		{"direct calls", input{calls, nil}, expected{false}},
		{"wrong input before correction", input{[]string{"lookup:wrong", "lookup:start", "read:record-9"},
			[][]liveProgramCall{{wrong}, want}}, expected{false}},
		{"changed result before correction",
			input{[]string{"lookup:start", "lookup:start", "read:record-9"},
				[][]liveProgramCall{{changed}, want}}, expected{false}},
		{"wrong operation order", input{[]string{"read:record-9", "lookup:start"},
			[][]liveProgramCall{want}}, expected{false}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, expected{
				validLiveProgramEvidence(test.input.calls, test.input.programs, want),
			})
		})
	}
}
