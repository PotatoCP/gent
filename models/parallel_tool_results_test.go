package models

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

func TestGenerateContentParallelToolResults(t *testing.T) {
	const calls = `{"role":"model","parts":[
{"functionCall":{"name":"read_policy","args":{"id":"A"}},"thoughtSignature":"c2ln"},
{"functionCall":{"name":"read_policy","id":"call-b","args":{"id":"B"}}}]}`
	const results = `{"role":"user","parts":[
{"functionResponse":{"name":"read_policy","response":{"result":["amber"]}}},
{"functionResponse":{"name":"read_policy","id":"call-b","response":{"result":["blue"]}}}]}`
	type input struct{ provider string }
	type expected struct{ first, second string }
	type testCase struct {
		input    input
		expected expected
	}
	const user = `{"role":"user","parts":[{"text":"Read both policies."}]}`
	want := expected{"[" + user + "]", "[" + user + "," + calls + "," + results + "]"}
	for _, test := range []testCase{{input{"gemini"}, want}, {input{"vertexai"}, want}} {
		t.Run(test.input.provider, func(t *testing.T) {
			var contents []json.RawMessage
			client := &http.Client{Transport: responseTransport(func(r *http.Request) (
				*http.Response, error,
			) {
				var body struct{ Contents json.RawMessage }
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					return nil, err
				}
				contents = append(contents, body.Contents)
				response := `{"candidates":[{"finishReason":"STOP","content":` + calls + `}]}`
				if len(contents) > 1 {
					response = `{"candidates":[{"finishReason":"STOP",
"content":{"role":"model","parts":[{"text":"Done."}]}}]}`
				}
				return fixtureHTTP(response, "application/json"), nil
			})}
			type lookup struct {
				ID string `json:"id"`
			}
			var read []string
			chain := toolchain.New().RegisterTool(gent.DefineTool("read_policy",
				func(_ context.Context, args lookup) (*gent.ToolExecutionResponse, error) {
					read = append(read, args.ID)
					color := map[string]string{"A": "amber", "B": "blue"}[args.ID]
					return &gent.ToolExecutionResponse{Status: gent.ToolExecuted,
						Parts: []gent.ContentPart{gent.TextPart(color)}}, nil
				}).Description("Read a policy.").MustBuild())
			agent := react.NewAgent(fixtureModel(test.input.provider, client))
			runner, execCtx := newModelExecution(t, t.Context(), agent, &gent.Config{ToolChain: chain})
			task := gent.NewTask(gent.TextPart("Read both policies."))
			answer, result, err := runner.NewTurn(execCtx, task)
			if !assert.NoError(t, err) || !assert.Len(t, contents, 2) {
				return
			}
			assert.Equal(t, []string{"A", "B"}, read)
			assert.Equal(t, "Done.", answer)
			assert.Equal(t, gent.ExecutionFinished, result.Status)
			assert.JSONEq(t, test.expected.first, string(contents[0]))
			assert.JSONEq(t, test.expected.second, string(contents[1]))
		})
	}
}

func TestGeminiToolResultsFollowCallOrder(t *testing.T) {
	call := func(id string) gent.ModelToolCallRequest {
		return gent.ModelToolCallRequest{ID: id, Type: "function",
			FunctionCall: &gent.FunctionCall{Name: "read", Arguments: "{}"}}
	}
	firstCall, secondCall := call("A"), call("B")
	first := preparedTestReply("A", "read", "amber")
	data := mustTestDataPart(map[string]any{"color": "amber"})
	first, err := first.WithParts(data, gent.TextPart("Check A."), gent.PNGPart([]byte{1}))
	if !assert.NoError(t, err) {
		return
	}
	second := preparedTestReply("B", "read", "blue")
	type input struct{ results []*gent.ModelMessage }
	type expected struct{ contents string }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	// Gemini 2 sends tool media as a separate user message after all function responses.
	want := expected{`[
{"role":"model","parts":[{"functionCall":{"name":"read","id":"A","args":{}}},
{"functionCall":{"name":"read","id":"B","args":{}}}]},
{"role":"user","parts":[{"functionResponse":{"name":"read","id":"A","response":{"color":"amber"}}},
{"functionResponse":{"name":"read","id":"B","response":{"result":["blue"]}}},{"text":"Check A."}]},
{"role":"user","parts":[{"text":"Media from read (call A):"},
{"inlineData":{"mimeType":"image/png","data":"AQ=="}}]},
{"role":"user","parts":[{"text":"Next task."}]}]`}
	tool := func(parts ...gent.ContentPart) *gent.ModelMessage {
		return &gent.ModelMessage{Role: gent.ChatMessageTypeTool, Parts: parts}
	}
	cases := []testCase{
		{"separate replies", input{[]*gent.ModelMessage{tool(second), tool(first)}}, want},
		{"one tool message", input{[]*gent.ModelMessage{tool(second, first)}}, want},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			messages := []*gent.ModelMessage{{Role: gent.ChatMessageTypeAI,
				Parts: []gent.ContentPart{firstCall, &secondCall}}}
			messages = append(messages, test.input.results...)
			messages = append(messages, gent.TextParts(gent.ChatMessageTypeHuman, "Next task."))
			got, _, _, err := geminiMessages(messages, "gemini", false)
			if !assert.NoError(t, err) {
				return
			}
			raw, err := json.Marshal(got)
			assert.NoError(t, err)
			assert.JSONEq(t, test.expected.contents, string(raw))
			assert.Same(t, second, test.input.results[0].Parts[0])
		})
	}
}
