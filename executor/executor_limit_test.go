package executor_test

// These tests check Runner limit mechanisms through real turn admission and saved events.
// ReAct checks each agent statistic separately in agents/react/agent_executor_limits_test.go.
// Add agent-specific statistics there; add new limit matching or measurement behavior here.

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLimitsThresholdsAndSavedEvents(t *testing.T) {
	type input struct {
		limits   []gent.Limit
		step     func(*gent.ExecutionContext)
		finishAt int
	}
	type expected struct {
		calls      int
		limitIndex int
		key        gent.StatKey
		value      float64
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	iteration := func(max float64) []gent.Limit {
		return []gent.Limit{{Type: gent.LimitExactKey, Key: gent.SCIterations, MaxValue: max}}
	}
	tokens := func(execCtx *gent.ExecutionContext) {
		execCtx.PublishAfterModelCall("fixture", &gent.ModelCallRequest{}, &gent.ModelCallResponse{
			Info: &gent.GenerationInfo{InputTokens: 10, OutputTokens: 5}}, 0, nil)
	}
	counter := func(execCtx *gent.ExecutionContext) { execCtx.Stats().IncrCounter("work", 10) }
	prefix := func(execCtx *gent.ExecutionContext) {
		execCtx.Stats().IncrCounter("work:fast", 30)
		execCtx.Stats().IncrCounter("work:slow", 20)
	}
	gauge := func(execCtx *gent.ExecutionContext) { execCtx.Stats().IncrGauge("fraction", 0.25) }
	gaugePrefix := func(execCtx *gent.ExecutionContext) {
		execCtx.Stats().IncrGauge("fraction:fast", 0.5)
		execCtx.Stats().IncrGauge("fraction:slow", 0.25)
	}
	parseError := func(execCtx *gent.ExecutionContext) {
		execCtx.PublishParseError(gent.ParseErrorTypeToolchain, "bad input", nil)
	}
	resetErrors := func(execCtx *gent.ExecutionContext) {
		if execCtx.Iteration() == 3 {
			execCtx.Stats().ResetGauge(gent.SGToolchainParseErrorConsecutive)
			return
		}
		parseError(execCtx)
	}
	cases := []testCase{

		{"zero iteration allowance", input{limits: iteration(0)}, expected{0, 0, gent.SCIterations, 0}},
		{"first iteration", input{limits: iteration(1)}, expected{1, 0, gent.SCIterations, 1}},
		{"fifth iteration", input{limits: iteration(5)}, expected{5, 0, gent.SCIterations, 5}},
		{"tenth iteration", input{limits: iteration(10)}, expected{10, 0, gent.SCIterations, 10}},
		{"answer at hard cap pauses", input{limits: iteration(2), finishAt: 2},
			expected{2, 0, gent.SCIterations, 2}},
		{
			"answer below cap",
			input{
				limits: iteration(
					5,
				),
				finishAt: 2,
			},
			expected{
				calls:      2,
				limitIndex: -1,
			},
		},
		{"input tokens first call", input{limits: []gent.Limit{{Type: gent.LimitExactKey,
			Key: gent.SCInputTokens, MaxValue: 9}}, step: tokens}, expected{1, 0, gent.SCInputTokens, 10}},
		{
			"output tokens later call",
			input{
				limits: []gent.Limit{
					{
						Type:     gent.LimitExactKey,
						Key:      gent.SCOutputTokens,
						MaxValue: 10,
					},
				},
				step: tokens,
			},
			expected{
				3,
				0,
				gent.SCOutputTokens,
				15,
			},
		},
		{"total tokens later call", input{limits: []gent.Limit{{Type: gent.LimitExactKey,
			Key: gent.SCTotalTokens, MaxValue: 30}}, step: tokens}, expected{3, 0, gent.SCTotalTokens, 45}},
		{"counter zero threshold", input{limits: []gent.Limit{{Type: gent.LimitExactKey,
			Key: "work", MaxValue: 0}}, step: counter}, expected{1, 0, "work", 10}},
		{"counter equality allowed", input{limits: []gent.Limit{{Type: gent.LimitExactKey,
			Key: "work", MaxValue: 10}}, step: counter, finishAt: 1}, expected{calls: 1, limitIndex: -1}},
		{"counter exceeds later", input{limits: []gent.Limit{{Type: gent.LimitExactKey,
			Key: "work", MaxValue: 10}}, step: counter}, expected{2, 0, "work", 20}},
		{"prefix first call", input{limits: []gent.Limit{{Type: gent.LimitKeyPrefix,
			Key: "work:", MaxValue: 25}}, step: prefix}, expected{1, 0, "work:fast", 30}},
		{"prefix later call", input{limits: []gent.Limit{{Type: gent.LimitKeyPrefix,
			Key: "work:", MaxValue: 45}}, step: prefix}, expected{2, 0, "work:fast", 60}},
		{"two matching rules choose first exceeded", input{limits: []gent.Limit{
			{Type: gent.LimitExactKey, Key: "work:fast", MaxValue: 100},
			{Type: gent.LimitKeyPrefix, Key: "work:", MaxValue: 45}}, step: prefix},
			expected{2, 1, "work:fast", 60}},
		{"gauge first call", input{limits: []gent.Limit{{Type: gent.LimitExactKey,
			Key: "fraction", MaxValue: 0.12}}, step: gauge}, expected{1, 0, "fraction", 0.25}},
		{"gauge equality allowed", input{limits: []gent.Limit{{Type: gent.LimitExactKey,
			Key: "fraction", MaxValue: 0.5}}, step: gauge, finishAt: 2}, expected{calls: 2, limitIndex: -1}},
		{"gauge later call", input{limits: []gent.Limit{{Type: gent.LimitExactKey,
			Key: "fraction", MaxValue: 0.5}}, step: gauge}, expected{3, 0, "fraction", 0.75}},
		{"gauge prefix first call", input{limits: []gent.Limit{{Type: gent.LimitKeyPrefix,
			Key: "fraction:", MaxValue: 0.25}}, step: gaugePrefix}, expected{1, 0, "fraction:fast", 0.5}},
		{"gauge prefix later call", input{limits: []gent.Limit{{Type: gent.LimitKeyPrefix,
			Key: "fraction:", MaxValue: 0.75}}, step: gaugePrefix}, expected{2, 0, "fraction:fast", 1}},
		{"consecutive parse errors", input{limits: []gent.Limit{{Type: gent.LimitExactKey,
			Key: gent.SGToolchainParseErrorConsecutive, MaxValue: 2}}, step: parseError},
			expected{3, 0, gent.SGToolchainParseErrorConsecutive, 3}},
		{"success resets consecutive errors", input{limits: []gent.Limit{{Type: gent.LimitExactKey,
			Key: gent.SGToolchainParseErrorConsecutive, MaxValue: 2}}, step: resetErrors, finishAt: 5},
			expected{calls: 5, limitIndex: -1}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			loop := &functionLoop{next: func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
				calls++
				if test.input.step != nil {
					test.input.step(execCtx)
				}
				if calls == test.input.finishAt {
					return acceptTestOutput(execCtx, "Done")
				}
				return &gent.AgentLoopResult{Action: gent.LAContinue}, nil
			}}
			store := &executionRecorder{codecs: gent.NewCodecRegistry()}
			runner := executor.NewRunner[string](loop, &gent.Config{Limits: test.input.limits,
				Codecs: store.codecs, Recorder: store})
			execCtx, err := runner.NewExecutionContext(context.Background(), "limits")
			require.NoError(t, err)
			defer execCtx.Release()
			value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Work.")))
			require.NoError(t, err)
			want := &gent.ExecutionResult{Status: gent.ExecutionFinished, TurnId: execCtx.TurnId(),
				TerminationReason: gent.TerminationSuccess, Output: []gent.ContentPart{gent.TextPart("Done")}}
			wantValue := "Done"
			var expectedEvents []*gent.LimitExceededEvent
			if test.expected.limitIndex >= 0 {
				limit := test.input.limits[test.expected.limitIndex]
				wantValue = ""
				want = &gent.ExecutionResult{Status: gent.ExecutionPaused, TurnId: execCtx.TurnId(),
					TerminationReason: gent.TerminationLimitExceeded, ExceededLimit: &gent.LimitExceeded{
						Limit: &limit, MatchedKey: test.expected.key, CurrentValue: test.expected.value}}
				expectedEvents = []*gent.LimitExceededEvent{{Limit: limit, MatchedKey: test.expected.key,
					CurrentValue: test.expected.value}}
			}
			assert.Equal(t, wantValue, value)
			assert.Equal(t, want, result)
			assert.Equal(t, test.expected.calls, calls)
			var events []*gent.LimitExceededEvent
			for _, event := range store.view.Activity {
				if exceeded, ok := event.Payload.(*gent.LimitExceededEvent); ok {
					copy := *exceeded
					copy.BaseEvent = gent.BaseEvent{}
					events = append(events, &copy)
				}
			}
			assert.Equal(t, expectedEvents, events)
		})
	}
}

func TestLimitsExplicitPresetsAndNoHiddenDefaults(t *testing.T) {
	type input struct {
		limits   []gent.Limit
		finishAt int
	}
	type expected struct {
		calls  int
		reason gent.TerminationReason
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	custom := gent.DefaultLimits()
	custom[0].MaxValue = 2
	cases := []testCase{

		{"nil means unlimited", input{nil, 101}, expected{101, gent.TerminationSuccess}},
		{"empty means unlimited", input{[]gent.Limit{}, 101}, expected{101, gent.TerminationSuccess}},
		{
			"explicit preset",
			input{
				gent.DefaultLimits(),
				101,
			},
			expected{
				100,
				gent.TerminationLimitExceeded,
			},
		},
		{"edited preset", input{custom, 101}, expected{2, gent.TerminationLimitExceeded}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			loop := &functionLoop{next: func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
				calls++
				if calls == test.input.finishAt {
					return acceptTestOutput(execCtx, "Done")
				}
				return &gent.AgentLoopResult{Action: gent.LAContinue}, nil
			}}
			runner := executor.NewRunner[string](loop, &gent.Config{Limits: test.input.limits})
			execCtx, err := runner.NewExecutionContext(context.Background(), "preset")
			require.NoError(t, err)
			defer execCtx.Release()
			_, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Work.")))
			require.NoError(t, err)
			assert.Equal(t, test.expected, expected{calls, result.TerminationReason})
		})
	}
	assert.Equal(t, float64(100), gent.DefaultLimits()[0].MaxValue)
}

func TestCanceledOwnerDoesNotStartTurn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	loop := &functionLoop{next: func(*gent.ExecutionContext) (*gent.AgentLoopResult, error) {
		t.Fatal("a canceled owner cannot execute a turn")
		return nil, nil
	}}
	runner := executor.NewRunner[string](loop, &gent.Config{})
	execCtx, err := runner.NewExecutionContext(ctx, "canceled")
	require.NoError(t, err)
	defer execCtx.Release()
	value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Work.")))
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, "", value)
	assert.Nil(t, result)
	_, last, err := runner.GetLastTurnResult(execCtx)
	assert.NoError(t, err)
	assert.Nil(t, last)
}
