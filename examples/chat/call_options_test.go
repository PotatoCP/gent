package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/models"
	"github.com/stretchr/testify/assert"
)

// Check the request after ReAct applies model defaults, not just the option callback. An unset
// temperature can acquire a default that the provider rejects when thinking is enabled.
func TestChatThinkingUsesAllowedAnthropicSampling(t *testing.T) {
	type input struct{ model string }
	type expected struct{ temperature any }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"Sonnet thinking", input{gent.ModelAnthropicClaude46Sonnet}, expected{float64(1)}},
		{"forbidden sampling", input{gent.ModelAnthropicClaude51Fable}, expected{nil}},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) {
					http.Error(w, "Invalid JSON", 400)
					return
				}
				requests <- body
				writeJSON(w, 200, map[string]any{"id": "response", "model": test.input.model,
					"stop_reason": "end_turn", "content": []any{
						map[string]any{"type": "text", "text": "Hello."},
					}})
			}))
			defer server.Close()
			model := models.NewAnthropic(&models.Config{BaseURL: server.URL,
				Model: test.input.model, APIKey: "fixture"})
			runner := executor.NewRunner(newAgent(model, "anthropic"), &gent.Config{})
			execCtx, err := runner.NewExecutionContext(context.Background(), "thinking options")
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			answer, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Hello")))
			if !assert.NoError(t, err) {
				return
			}
			assert.Equal(t, "Hello.", answer)
			assert.Equal(t, gent.ExecutionFinished, result.Status)
			want := map[string]any{"model": test.input.model, "max_tokens": float64(8192),
				"stream": true, "thinking": map[string]any{"type": "adaptive"},
				"output_config": map[string]any{"effort": "medium"},
				"messages": []any{map[string]any{"role": "user", "content": []any{
					map[string]any{"type": "text", "text": "Hello"},
				}}}}
			if test.expected.temperature != nil {
				want["temperature"] = test.expected.temperature
			}
			assert.Equal(t, want, <-requests)
		})
	}
}
