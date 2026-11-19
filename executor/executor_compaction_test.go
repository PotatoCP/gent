package executor_test

import (
	"context"
	"errors"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/internal/tt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompactionScheduleAcceptanceAndFailures(t *testing.T) {
	type input struct {
		schedule []bool
		failAt   int
	}
	type expected struct {
		calls, compactions, retained int
		events                       []string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	before, after := gent.EventNameIterationBefore, gent.EventNameIterationAfter
	start, end := gent.EventNameExecutionBefore, gent.EventNameExecutionAfter
	compact := gent.EventNameCompaction
	cases := []testCase{

		{
			"no configuration",
			input{},
			expected{
				2,
				0,
				3,
				[]string{
					start,
					before,
					after,
					before,
					after,
					end,
				},
			},
		},
		{"trigger false", input{[]bool{false, false}, 0}, expected{2, 0, 3,
			[]string{start, before, after, before, after, end}}},
		{"before first iteration", input{[]bool{true, false}, 0}, expected{2, 1, 3,
			[]string{start, compact, before, after, before, after, end}}},
		{"before second iteration", input{[]bool{false, true}, 0}, expected{2, 1, 2,
			[]string{start, before, after, compact, before, after, end}}},
		{"both iterations", input{[]bool{true, true}, 0}, expected{2, 2, 2,
			[]string{start, compact, before, after, compact, before, after, end}}},
		{"first compaction failure", input{[]bool{true}, 1}, expected{0, 0, 1, []string{start}}},
		{"second compaction failure", input{[]bool{true, true}, 2}, expected{1, 1, 2,
			[]string{start, compact, before, after}}},
	}
	failure := errors.New("summary operation failed")
	for _, test := range cases {
		t.Run(
			test.name,
			func(t *testing.T) {
				calls := 0
				loop := &functionLoop{
					next: func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
						calls++
						if err := execCtx.AppendRecord(
							&gent.HistoryEntry{
								Messages: []*gent.MessageContent{
									{

										Role: gent.ChatMessageTypeAI,
										Parts: []gent.ContentPart{
											gent.TextPart(
												"Progress.",
											),
										},
									},
								},
							},
							true,
						); err != nil {
							return nil, err
						}
						if execCtx.Iteration() == 2 {
							return acceptTestOutput(execCtx, "Done")
						}
						return &gent.AgentLoopResult{Action: gent.LAContinue}, nil
					},
				}
				store := &executionRecorder{codecs: gent.NewCodecRegistry()}
				config := &gent.Config{Codecs: store.codecs, Recorder: store}
				var trigger *tt.MockCompactionTrigger
				var strategy *tt.MockCompactionStrategy
				if test.input.schedule != nil {
					trigger = tt.NewMockCompactionTrigger().WithShouldCompact(test.input.schedule...)
					strategy = tt.NewMockCompactionStrategy().WithCompactFunc(func(
						_ *gent.ExecutionContext, input *gent.CompactionInput,
					) (*gent.HistoryUpdate, error) {
						if strategy.CallCount() == test.input.failAt {
							return nil, failure
						}
						active := input.EntryIds()
						if len(active) > 1 {
							active = active[len(active)-1:]
						}
						return &gent.HistoryUpdate{ExpectedDraftRevision: input.Revision, Active: active}, nil
					})
					config.CompactionTrigger, config.CompactionStrategy = trigger, strategy
				}
				runner := executor.NewRunner[string](loop, config)
				execCtx, err := runner.NewExecutionContext(context.Background(), "compaction")
				require.NoError(t, err)
				defer execCtx.Release()
				value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Work.")))
				if test.input.failAt > 0 {
					assert.ErrorIs(t, err, failure)
					assert.Equal(t, "", value)
					assert.Nil(t, result)
				} else {
					require.NoError(t, err)
					assert.Equal(t, "Done", value)
					assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionFinished, TurnId: execCtx.TurnId(),
						TerminationReason: gent.TerminationSuccess,
						Output:            []gent.ContentPart{gent.TextPart("Done")}}, result)
				}
				var events []string
				for _, event := range store.view.Activity {
					switch value := event.Payload.(type) {
					case *gent.BeforeExecutionEvent:
						events = append(events, value.EventName)
					case *gent.AfterExecutionEvent:
						events = append(events, value.EventName)
					case *gent.BeforeIterationEvent:
						events = append(events, value.EventName)
					case *gent.AfterIterationEvent:
						events = append(events, value.EventName)
					case *gent.CompactionEvent:
						events = append(events, value.EventName)
					}
				}
				retained, err := execCtx.Scratchpad()
				require.NoError(t, err)
				assert.Equal(t, test.expected, expected{calls,
					int(execCtx.Stats().GetCounter(gent.SCCompactions)), len(retained), events})
				if trigger != nil {
					assert.Equal(t, test.expected.compactions, trigger.NotifiedCount())
				}
				// A failed summary has no terminal save. Restore the accepted head with corrected
				// configuration and continue the same task without losing earlier compaction work.
				if test.input.failAt > 0 {
					codecs := gent.NewCodecRegistry()
					fixed := executor.NewRunner[string](loop, &gent.Config{Codecs: codecs})
					restored, err := fixed.Restore(context.Background(), store.latest(t, codecs))
					require.NoError(t, err)
					require.Equal(t, gent.RestoreReady, restored.Status)
					defer restored.Context.Release()
					value, result, err = fixed.Continue(restored.Context)
					require.NoError(t, err)
					assert.Equal(t, "Done", value)
					assert.Equal(t, gent.TerminationSuccess, result.TerminationReason)
				}
			},
		)
	}
}

type measuredTokens struct{ input, output, total float64 }
type gaugeCapturingTrigger struct{ measurements []measuredTokens }

func (trigger *gaugeCapturingTrigger) ShouldCompact(execCtx *gent.ExecutionContext) (bool, error) {
	stats := execCtx.Stats()
	trigger.measurements = append(trigger.measurements, measuredTokens{
		stats.GetGauge(gent.SGInputTokensLastIteration), stats.GetGauge(gent.SGOutputTokensLastIteration),
		stats.GetGauge(gent.SGTotalTokensLastIteration)})
	return false, nil
}
func (*gaugeCapturingTrigger) NotifyCompacted(*gent.ExecutionContext) error { return nil }

func TestCompactionReadsPreviousIterationGauges(t *testing.T) {
	type input struct{ inputTokens, outputTokens int }
	type expected struct{ measurements []measuredTokens }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"small", input{10, 5}, expected{[]measuredTokens{{}, {10, 5, 15}}}},
		{"large", input{500, 100}, expected{[]measuredTokens{{}, {500, 100, 600}}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			trigger := &gaugeCapturingTrigger{}
			loop := &functionLoop{next: func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
				execCtx.PublishAfterModelCall("fixture", &gent.ModelCallRequest{}, &gent.ModelCallResponse{
					Info: &gent.GenerationInfo{InputTokens: test.input.inputTokens,
						OutputTokens: test.input.outputTokens}}, 0, nil)
				if execCtx.Iteration() == 2 {
					return acceptTestOutput(execCtx, "Done")
				}
				return &gent.AgentLoopResult{Action: gent.LAContinue}, nil
			}}
			runner := executor.NewRunner[string](loop, &gent.Config{CompactionTrigger: trigger,
				CompactionStrategy: tt.NewMockCompactionStrategy()})
			execCtx, err := runner.NewExecutionContext(context.Background(), "gauge timing")
			require.NoError(t, err)
			defer execCtx.Release()
			_, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Work.")))
			require.NoError(t, err)
			assert.Equal(t, gent.ExecutionFinished, result.Status)
			assert.Equal(t, test.expected, expected{trigger.measurements})
		})
	}
}
