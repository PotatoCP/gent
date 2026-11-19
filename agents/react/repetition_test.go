package react

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/rickchristie/gent"
	agentutil "github.com/rickchristie/gent/agents"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgent_ResponseGuardRecovery(t *testing.T) {
	type input struct {
		text string
		max  int
		tool bool
	}
	type expected struct {
		loops     int64
		gauges    []float64
		toolCalls int
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cfg := agentutil.DefaultRepetitionConfig()
	cfg.BlockSize = 50
	block := strings.Repeat("I am stuck in a loop and cannot proceed. ", 2)[:cfg.BlockSize]
	looping := strings.Repeat(block, 5)
	// Two repetitions are below the streaming threshold (3), but above CheckAccumulated's
	// relaxed threshold (2). The size guard triggers first, then detects the repetition.
	shortBlock := strings.Repeat("I am stuck in this loop. ", 3)[:cfg.BlockSize]
	twoStep := strings.Repeat(shortBlock, 2) + strings.Repeat("padding text ", 10)

	// Use unique paragraphs to avoid SimHash near-duplicate matches in overlapping windows.
	words := []string{
		"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel",
		"india", "juliet", "kilo", "lima", "mike", "november", "oscar", "papa",
		"quebec", "romeo", "sierra", "tango", "uniform", "victor", "whiskey", "xray",
	}
	var text strings.Builder
	for i := range 200 {
		fmt.Fprintf(&text, "%s%d_%d_%d_%d ", words[i%len(words)], i, i*7, i*13, i*31)
	}
	cases := []testCase{
		{"repetition then answer", input{looping, 0, false}, expected{1, []float64{1}, 0}},
		{"tool resets repetition gauge", input{looping, 0, true}, expected{1, []float64{1, 0}, 1}},
		{"long unique answer", input{text.String(), 500, false}, expected{0, []float64{0}, 0}},
		{"size guard detects repetition", input{twoStep, len(twoStep) - 10, false},
			expected{1, []float64{1}, 0}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			responses := []*gent.ModelCallResponse{nativeTestResponse(answerChoice(test.input.text))}
			if test.input.tool {
				responses = append(responses, nativeTestResponse(&gent.ContentChoice{
					Completion: gent.ModelCompletion{Status: gent.ModelStatusToolCalls},
					Parts:      []gent.ContentPart{nativeTestCall("work", "test_tool", "{}")},
				}))
			}
			responses = append(responses, nativeTestResponse(answerChoice("Done.")))
			model := newMockModel(responses...)
			calls := 0
			chain := toolchain.New().RegisterTool(gent.DefineTool("test_tool",
				func(context.Context, map[string]any) (*gent.ToolExecutionResponse, error) {
					calls++
					return capturedToolResponse("tool result", nil)
				}).Description("Read a result").ParameterSchema(nil).MustBuild())
			loop := NewAgent(model).WithRepetitionConfig(cfg).WithMaxResponseChars(test.input.max)
			recorder := &testRecorder{}
			runner, execCtx := newTestExecution(t, loop, &gent.Config{
				ToolChain: chain, Recorder: recorder,
			})
			value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("test task")))
			require.NoError(t, err)
			assert.Equal(t, "Done.", value)
			assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionFinished,
				TurnId: execCtx.TurnId(), TerminationReason: gent.TerminationSuccess,
				Output: []gent.ContentPart{gent.TextPart("Done.")}}, result)
			var gauges []float64
			for _, batch := range recorder.batches {
				if batch.Checkpoint.Phase == gent.PhaseIterationCompleted {
					gauges = append(gauges,
						batch.Runtime.Contexts[0].Gauges[string(gent.SGRepetitionLoopConsecutive)])
				}
			}
			assert.Equal(t, test.expected, expected{
				execCtx.Stats().GetCounter(gent.SCRepetitionLoopTotal), gauges, calls,
			})
			assert.Equal(t, float64(0), execCtx.Stats().GetGauge(gent.SGRepetitionLoopConsecutive))
			assert.Equal(t, len(responses), model.callCount)
		})
	}
}

func TestAgent_RepetitionDetection_TerminateImmediately(t *testing.T) {
	cfg := agentutil.DefaultRepetitionConfig()
	cfg.BlockSize = 50
	cfg.Action = agentutil.RepetitionTerminate
	model := newMockModel(nativeTestResponse(answerChoice(strings.Repeat("x", cfg.BlockSize*5))))
	loop := NewAgent(model).WithRepetitionConfig(cfg)
	runner, execCtx := newTestExecution(t, loop, &gent.Config{})
	value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("test")))
	require.ErrorIs(t, err, agentutil.ErrRepetitionDetected)
	assert.EqualError(t, err, fmt.Sprintf(
		"gent: Loop.Next %T failed in run %q turn %q iteration 1: model call failed: %s",
		loop, execCtx.RunId(), execCtx.TurnId(), agentutil.ErrRepetitionDetected))
	assert.Equal(t, "", value)
	assert.Nil(t, result)
}
