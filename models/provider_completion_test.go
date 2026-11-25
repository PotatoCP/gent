package models

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/output"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

func incompleteFixture(t *testing.T, provider string, tools, unnamed bool) string {
	t.Helper()
	turn := 3
	if tools {
		turn = 0
	}
	var value map[string]any
	assert.NoError(t, json.Unmarshal([]byte(providerResponse(provider, turn)), &value))
	switch provider {
	case "openai", "xai":
		value["status"] = "incomplete"
		value["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
		if unnamed {
			delete(value["output"].([]any)[1].(map[string]any), "name")
		}
	case "anthropic":
		value["stop_reason"] = "max_tokens"
		if unnamed {
			delete(value["content"].([]any)[1].(map[string]any), "name")
		}
	case "gemini", "vertexai":
		candidate := value["candidates"].([]any)[0].(map[string]any)
		candidate["finishReason"] = "MAX_TOKENS"
		if unnamed {
			part := candidate["content"].(map[string]any)["parts"].([]any)[1].(map[string]any)
			delete(part["functionCall"].(map[string]any), "name")
		}
	case "ollama":
		value["done_reason"] = "length"
		if unnamed {
			call := value["message"].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
			delete(call["function"].(map[string]any), "name")
		}
	default:
		choice := value["choices"].([]any)[0].(map[string]any)
		choice["finish_reason"] = "length"
		if unnamed {
			call := choice["message"].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
			delete(call["function"].(map[string]any), "name")
		}
	}
	data, err := json.Marshal(value)
	assert.NoError(t, err)
	return string(data)
}

func TestProvidersIncompleteRetriesWithoutValidationOrTools(t *testing.T) {
	type input struct {
		provider string
		tools    bool
		unnamed  bool
	}
	type expected struct {
		requests, validations, tools int
		iterations                   int64
		value                        string
		reason                       gent.TerminationReason
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	var cases []testCase
	want := expected{requests: 2, validations: 1, iterations: 2, value: `{"value":3}`,
		reason: gent.TerminationSuccess}
	for _, provider := range testProviders {
		cases = append(cases,
			testCase{provider + "/complete-looking JSON", input{provider: provider}, want},
			testCase{provider + "/complete-looking tool", input{provider: provider, tools: true}, want},
			testCase{provider + "/partial tool", input{provider, true, true}, want})
	}
	for _, test := range cases {
		t.Run(
			test.name,
			func(t *testing.T) {
				first := incompleteFixture(t, test.input.provider, test.input.tools, test.input.unnamed)
				requests, validations, calls := 0, 0, 0
				client := &http.Client{Transport: responseTransport(func(request *http.Request) (
					*http.Response, error,
				) {
					if request.Method == http.MethodGet {
						return fixtureHTTP(routerFixture, "application/json"), nil
					}
					requests++
					if requests == 1 {
						return fixtureHTTP(first, "application/json"), nil
					}
					if requests > 2 {
						return nil, errors.New("unexpected incomplete retry")
					}
					return fixtureHTTP(providerResponse(test.input.provider, 3), "application/json"), nil
				})}
				out := output.NewTextOutput().SetCustomValidator("accept", func(_ *gent.ExecutionContext,
					_ string,
				) (*gent.OutputValidationResult, error) {
					validations++
					return &gent.OutputValidationResult{Accepted: true}, nil
				})
				agent := react.NewWithOutput(fixtureModel(test.input.provider, client), out)
				chain := toolchain.New().RegisterTool(
					gent.DefineTool("lookup", func(context.Context, providerLookupInput) (
						*gent.ToolExecutionResponse, error,
					) {
						calls++
						return capturedToolResponse("record-seven", nil)
					}).Description("Read").MustBuild())
				task := gent.NewTask(gent.TextContent{Text: "Read the record."})
				runner, execCtx := newModelExecution(
					t,
					context.Background(),
					agent,
					&gent.Config{
						ToolChain: chain,
					},
				)
				value, result, err := runner.NewTurn(execCtx, task)
				if !assert.NoError(t, err) {
					return
				}
				assert.Equal(t, test.expected, expected{requests, validations, calls,
					execCtx.Stats().GetCounter(gent.SCIterations), value, result.TerminationReason})
				savedTask, err := execCtx.Task()
				assert.NoError(t, err)
				assert.Equal(t, task, savedTask)
				assert.Equal(t, int64(0), execCtx.Stats().GetCounter(gent.SCOutputRejectedTotal))
				var completions []gent.ModelStatus
				for _, event := range providerEvents(execCtx) {
					if after, ok := event.(*gent.AfterModelCallEvent); ok {
						completions = append(completions, after.Response.Choices[0].Completion.Status)
					}
				}
				assert.Equal(t, []gent.ModelStatus{
					gent.ModelStatusIncomplete,
					gent.ModelStatusAnswer,
				}, completions)
			},
		)
	}
}

func TestProvidersNativeRefusal(t *testing.T) {
	type input struct{ provider, response string }
	type expected struct{ refusal *gent.ModelRefusal }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	blocked := []gent.ContentPart{gent.TextContent{Text: "blocked"}}
	var cases []testCase
	for _, provider := range testProviders {
		payload := `{"choices":[{"message":{"role":"assistant","refusal":"blocked"},
"finish_reason":"stop"}]}`
		refusal := &gent.ModelRefusal{Code: "refusal", Content: blocked}
		switch provider {
		case "openai", "xai":
			payload = `{"status":"completed","output":[{"type":"message","role":"assistant",
"content":[{"type":"refusal","refusal":"blocked"}]}]}`
			refusal = &gent.ModelRefusal{Content: blocked}
		case "anthropic":
			payload = `{"stop_reason":"refusal","content":[{"type":"text","text":"blocked"}]}`
		case "gemini", "vertexai":
			payload = `{"promptFeedback":{"blockReason":"SAFETY"}}`
			refusal = &gent.ModelRefusal{Code: "SAFETY"}
			cases = append(cases, testCase{
				provider + "/image recitation",
				input{provider, `{"candidates":[{"finishReason":"IMAGE_RECITATION"}]}`},
				expected{&gent.ModelRefusal{Code: "IMAGE_RECITATION"}},
			})
		case "ollama":
			// /api/chat has no native refusal field. Text that says no remains ordinary text.
			continue
		}
		cases = append(cases, testCase{provider, input{provider, payload}, expected{refusal}})
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			requests, validations := 0, 0
			client := &http.Client{Transport: responseTransport(func(request *http.Request) (
				*http.Response, error,
			) {
				if request.Method == http.MethodGet {
					return fixtureHTTP(routerFixture, "application/json"), nil
				}
				requests++
				return fixtureHTTP(test.input.response, "application/json"), nil
			})}
			out := output.NewTextOutput().SetCustomValidator("accept", func(_ *gent.ExecutionContext,
				_ string,
			) (*gent.OutputValidationResult, error) {
				validations++
				return &gent.OutputValidationResult{Accepted: true}, nil
			})
			agent := react.NewWithOutput(fixtureModel(test.input.provider, client), out)
			runner, execCtx := newModelExecution(t, context.Background(), agent, nil)
			value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read.")))
			assert.NoError(t, err)
			assert.Equal(t, "", value)
			want := &gent.ExecutionResult{Status: gent.ExecutionFinished, TurnId: execCtx.TurnId(),
				TerminationReason: gent.TerminationRefused,
				Refusal:           test.expected.refusal}
			assert.Equal(t, want, result)
			assert.Equal(t, 0, validations)
			saved := execCtx.Config().Recorder.(*modelTestStore).saved
			runner, restored := restoreModelExecution(t, context.Background(), agent, nil, saved)
			value, result, err = runner.GetLastTurnResult(restored)
			assert.NoError(t, err)
			assert.Equal(t, "", value)
			assert.Equal(t, want, result)
			assert.Equal(t, 0, validations)
			assert.Equal(t, 1, requests)
		})
	}
}
