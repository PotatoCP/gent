package models

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/internal/tt"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

func TestPrepareRequestChecksEffectiveStrictTools(t *testing.T) {
	execCtx := tt.NewContext(t, &gent.Config{})
	defer execCtx.Release()
	newChain := func(strict bool) gent.ToolChain {
		return toolchain.New().RegisterTool(gent.DefineTool("lookup", func(context.Context,
			map[string]any,
		) (*gent.ToolExecutionResponse, error) {
			return nil, errors.New("unexpected tool call")
		}).Description("Read a record.").ParameterSchema(nil).Strict(strict).MustBuild())
	}
	strict, plain := newChain(true), newChain(false)
	strictTools, err := strict.NativeTools(execCtx)
	if !assert.NoError(t, err) {
		return
	}
	plainTools, err := plain.NativeTools(execCtx)
	if !assert.NoError(t, err) {
		return
	}
	type input struct {
		tools []*gent.ToolDefinition
		chain gent.ToolChain
	}
	type expected struct {
		tools  []*gent.ToolDefinition
		strict bool
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"no tools", input{}, expected{}},
		{"explicit strict", input{tools: strictTools}, expected{strictTools, true}},
		{"catalog requires strict", input{plainTools, strict}, expected{strictTools, true}},
		{"catalog replaces strict", input{strictTools, plain}, expected{tools: plainTools}},
	}
	for _, provider := range []string{"openai", "anthropic", "gemini", "vertexai", "ollama"} {
		for _, tc := range cases {
			t.Run(provider+"/"+tc.name, func(t *testing.T) {
				calls := 0
				client := &http.Client{Transport: responseTransport(func(*http.Request) (
					*http.Response, error,
				) {
					calls++
					return nil, errors.New("unexpected HTTP request")
				})}
				model := fixtureModel(provider, client)
				request := gent.NewModelCallRequest([]*gent.ModelMessage{
					gent.TextParts(gent.ChatMessageTypeHuman, "Read the record."),
				})
				request.Options.Tools = tc.input.tools
				prepared, err := model.PrepareRequest(execCtx, request, tc.input.chain)
				strictError := map[string]string{
					"gemini":   "Gemini does not support per-function strict settings",
					"vertexai": "Gemini does not support per-function strict settings",
					"ollama":   "Ollama requires non-strict function tools",
				}[provider]
				if tc.expected.strict && strictError != "" {
					assert.EqualError(t, err, strictError)
					assert.Nil(t, prepared)
					// Direct callers and Hooks can supply different tools after preparation.
					effective := *request
					effective.Options.Tools = tc.expected.tools
					stream, err := model.GenerateContentStream(t.Context(), &effective)
					assert.EqualError(t, err, strictError)
					assert.Nil(t, stream)
				} else if assert.NoError(t, err) && assert.NotNil(t, prepared) {
					assert.Equal(t, tc.expected.tools, prepared.Options.Tools)
				}
				assert.Equal(t, tc.input.tools, request.Options.Tools)
				assert.True(t, strictTools[0].Function.Strict)
				assert.Equal(t, 0, calls)
			})
		}
	}
}
