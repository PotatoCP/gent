package react

import (
	"context"
	"fmt"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

// The reply exposes the values received by the adapter without shared test state.
type samplingEchoModel struct{}

func (*samplingEchoModel) GenerateContentStream(_ context.Context, request *gent.ModelCallRequest) (
	gent.Stream, error,
) {
	content := fmt.Sprintf("%.1f %.1f", *request.Options.Temperature, *request.Options.TopP)
	return gent.NewCompletedStream(nativeTestResponse(answerChoice(content)), nil), nil
}

func TestAgentSamplingHooksOwnOptions(t *testing.T) {
	type input struct{ change func(*gent.CallOptions) }
	type callResult struct {
		value  string
		result *gent.ExecutionResult
		err    error
	}
	type expected struct{ calls []callResult }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	answer := func(content string) callResult {
		return callResult{value: content, result: &gent.ExecutionResult{
			Status: gent.ExecutionFinished, TerminationReason: gent.TerminationSuccess,
			Output: []gent.ContentPart{gent.TextPart(content)},
		}}
	}
	cases := []testCase{
		{"temperature", input{func(o *gent.CallOptions) { *o.Temperature = 0.1 }},
			expected{[]callResult{answer("0.1 0.9"), answer("0.8 0.9")}}},
		{"top p", input{func(o *gent.CallOptions) { *o.TopP = 0.1 }},
			expected{[]callResult{answer("0.8 0.1"), answer("0.8 0.9")}}},
		{"explicit zero", input{func(o *gent.CallOptions) { *o.Temperature, *o.TopP = 0, 0 }},
			expected{[]callResult{answer("0.0 0.0"), answer("0.8 0.9")}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			loop := NewAgent(&samplingEchoModel{}).
				WithCallOptions(gent.WithTemperature(0.8), gent.WithTopP(0.9))
			var actual expected
			for call := range 2 {
				config := &gent.Config{}
				if call == 0 {
					config.Hooks = &gent.Hooks{BeforeModelCall: func(_ context.Context,
						event *gent.BeforeModelCallEvent,
					) error {
						test.input.change(&event.Request.Options)
						return nil
					}}
				}
				runner, execCtx := newTestExecution(t, loop, config)
				value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Answer.")))
				actual.calls = append(actual.calls, callResult{value, result, err})
				test.expected.calls[call].result.TurnId = execCtx.TurnId()
			}
			assert.Equal(t, test.expected, actual)
		})
	}
}

func TestAgentSamplingHooksConcurrent(t *testing.T) {
	type expected struct {
		value  string
		result *gent.ExecutionResult
		err    error
	}
	loop := NewAgent(&samplingEchoModel{}).
		WithCallOptions(gent.WithTemperature(0.8), gent.WithTopP(0.9))
	ready := make(chan struct{}, 2)
	changed := make(chan struct{}, 2)
	start := make(chan struct{})
	finish := make(chan struct{})
	results := make([]chan expected, 2)
	contexts := make([]*gent.ExecutionContext, 2)
	for worker := range results {
		config := &gent.Config{Hooks: &gent.Hooks{BeforeModelCall: func(_ context.Context,
			event *gent.BeforeModelCallEvent,
		) error {
			ready <- struct{}{}
			<-start
			*event.Request.Options.Temperature = float64(worker) / 10
			*event.Request.Options.TopP = float64(worker+1) / 10
			changed <- struct{}{}
			<-finish
			return nil
		}}}
		runner, execCtx := newTestExecution(t, loop, config)
		contexts[worker] = execCtx
		results[worker] = make(chan expected, 1)
		go func() {
			value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Answer.")))
			results[worker] <- expected{value, result, err}
		}()
	}
	for range results {
		<-ready
	}
	close(start)
	for range results {
		<-changed
	}
	close(finish)
	// Both hooks have changed their own values before either adapter reads them.
	for worker, content := range []string{"0.0 0.1", "0.1 0.2"} {
		actual := <-results[worker]
		want := expected{value: content, result: &gent.ExecutionResult{
			Status: gent.ExecutionFinished, TurnId: contexts[worker].TurnId(),
			TerminationReason: gent.TerminationSuccess,
			Output:            []gent.ContentPart{gent.TextPart(content)},
		}}
		assert.Equal(t, want, actual)
	}
}

func (m *samplingEchoModel) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}
