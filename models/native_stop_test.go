package models_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/models"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

type nativeStopTransport func(*http.Request) (*http.Response, error)

func (transport nativeStopTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

// Real stream decoding must preserve the stop reason through the Anthropic block merge.
func TestNativeHTTP_AnthropicStopReasons(t *testing.T) {
	type input struct{ fixture, originalReason, reason string }
	type expected struct {
		requests int
		calls    int
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"tool token limit", input{"native-anthropic-1.sse", "tool_use", "max_tokens"},
			expected{2, 0}},
		{"answer token limit", input{"native-anthropic-2.sse", "end_turn", "max_tokens"},
			expected{2, 0}},
		{"tool context limit", input{"native-anthropic-1.sse", "tool_use",
			"model_context_window_exceeded"}, expected{2, 0}},
		{"answer context limit", input{"native-anthropic-2.sse", "end_turn",
			"model_context_window_exceeded"}, expected{2, 0}},
		{"complete tools", input{"native-anthropic-1.sse", "tool_use", "tool_use"},
			expected{2, 2}},
		{"complete answer", input{"native-anthropic-2.sse", "end_turn", "end_turn"},
			expected{1, 0}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture, err := os.ReadFile(filepath.Join("testdata", test.input.fixture))
			if !assert.NoError(t, err) {
				return
			}
			original := `"stop_reason": "` + test.input.originalReason + `"`
			if !assert.Equal(t, 1, strings.Count(string(fixture), original)) {
				return
			}
			body := strings.Replace(string(fixture), original, `"stop_reason": "`+test.input.reason+`"`, 1)
			requests := 0
			final, err := os.ReadFile(filepath.Join("testdata", "native-anthropic-2.sse"))
			if !assert.NoError(t, err) {
				return
			}
			client := &http.Client{Transport: nativeStopTransport(func(*http.Request) (
				*http.Response, error,
			) {
				requests++
				data := body
				if requests > 1 {
					data = string(final)
				}
				return &http.Response{StatusCode: http.StatusOK,
					Header: http.Header{"Content-Type": {"text/event-stream"}},
					Body:   io.NopCloser(strings.NewReader(data))}, nil
			})}
			provider := models.NewAnthropic(&models.Config{APIKey: "test-key",
				Model: "wire-test", HTTPClient: client})
			calls := 0
			agent := react.NewAgent(provider)
			chain := toolchain.New().RegisterTool(
				gent.DefineTool("lookup", func(context.Context, map[string]any) (
					*gent.ToolExecutionResponse, error,
				) {
					calls++
					return capturedToolResponse("record", nil)
				}).Description("Read").ParameterSchema(nil).MustBuild())
			runner := executor.NewRunner(agent, &gent.Config{ToolChain: chain})
			execCtx, err := runner.NewExecutionContext(context.Background(), t.Name())
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read two records.")))
			assert.NoError(t, err)
			assert.Equal(t, "Done.", value)
			assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionFinished,
				TurnId: execCtx.TurnId(), TerminationReason: gent.TerminationSuccess,
				Output: []gent.ContentPart{gent.TextPart("Done.")}}, result)
			assert.Equal(t, test.expected, expected{requests, calls})
		})
	}
}
