package models

import genttest "github.com/rickchristie/gent/internal/tt"

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

func TestResponsesAgentMessagePhases(t *testing.T) {
	type input struct{ output string }
	type expected struct {
		result *gent.AgentLoopResult
		calls  int
		err    error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	const commentary = `{"type":"message","phase":"commentary","role":"assistant",
"content":[{"type":"output_text","text":"I will check this."}]}`
	const answer = `{"type":"message","phase":"final_answer","role":"assistant",
"content":[{"type":"output_text","text":"Done."}]}`
	const ordinary = `{"type":"message","role":"assistant",
"content":[{"type":"output_text","text":"Done."}]}`
	const call = `{"type":"function_call","call_id":"call-1","name":"lookup","arguments":"{}"}`
	accepted := &gent.AgentLoopResult{TerminationReason: gent.TerminationSuccess,
		Action: gent.LATerminate,
		Result: []gent.ContentPart{
			gent.TextContent{Text: "Done."},
		},
		Value: genttest.Value(gent.ContentText([]gent.ContentPart{
			gent.TextContent{Text: "Done."},
		}))}
	tools := &gent.AgentLoopResult{Action: gent.LAContinue}
	cases := []testCase{
		{"commentary alone", input{commentary}, expected{result: &gent.AgentLoopResult{
			Action: gent.LAContinue}}},
		{"commentary and answer", input{commentary + "," + answer}, expected{result: accepted}},
		{"answer alone", input{answer}, expected{result: accepted}},
		{"unlabelled answer", input{ordinary}, expected{result: accepted}},
		{"commentary and tool", input{commentary + "," + call}, expected{result: tools, calls: 1}},
		{"unlabelled text and tool", input{ordinary + "," + call}, expected{result: tools, calls: 1}},
		{"answer and tool", input{answer + "," + call}, expected{result: tools, calls: 1}},
	}
	for _, test := range cases {
		t.Run(
			test.name,
			func(t *testing.T) {
				requests := 0
				model := NewResponses(&ResponsesConfig{
					Provider: "openai", BaseURL: "https://fixture.test/v1", Model: "fixture",
					HTTPClient: &http.Client{Transport: responseTransport(func(
						*http.Request,
					) (*http.Response, error) {
						requests++
						item := test.input.output
						if requests > 1 {
							item = answer
						}
						body := `{"status":"completed","output":[` + item + `]}`
						return &http.Response{StatusCode: 200,
							Header: http.Header{"Content-Type": {"application/json"}},
							Body:   io.NopCloser(strings.NewReader(body))}, nil
					})},
				})
				calls := 0
				agent := react.NewAgent(model)
				chain := toolchain.New().RegisterTool(gent.DefineTool("lookup",
					func(context.Context, map[string]any) (*gent.ToolExecutionResponse, error) {
						calls++
						return capturedToolResponse("record", nil)
					}).Description("Read").ParameterSchema(nil).MustBuild())
				runner, execCtx := newModelExecution(
					t,
					context.Background(),
					agent,
					&gent.Config{
						ToolChain: chain,
					},
				)
				value, completed, err := runner.NewTurn(
					execCtx,
					gent.NewTask(
						gent.TextPart(
							"Read the record.",
						),
					),
				)
				if !assert.NoError(t, err) {
					return
				}
				assert.Equal(t, "Done.", value)
				assert.Equal(t, gent.ExecutionFinished, completed.Status)
				var result *gent.AgentLoopResult
				var source *gent.ContentSource
				for _, event := range providerEvents(execCtx) {
					switch event := event.(type) {
					case *gent.AfterModelCallEvent:
						if event.Iteration == 1 {
							source = event.Response.Choices[0].CandidateSource
						}
					case *gent.AfterIterationEvent:
						if event.Iteration == 1 {
							result = event.Result
						}
					}
				}
				want := test.expected
				if want.result.Action == gent.LATerminate {
					copy := *want.result
					copy.ContentSource = source
					want.result = &copy
				}
				assert.Equal(t, want, expected{result, calls, err})
			},
		)
	}
}
