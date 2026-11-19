package compaction_test

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/compaction"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/internal/tt"
	"github.com/stretchr/testify/assert"
)

type baselineRecorder struct {
	codecs  *gent.CodecRegistry
	batches [][]byte
	state   []byte
}

func (store *baselineRecorder) Save(_ context.Context, batch *gent.SaveBatch,
	capture func() (*gent.RunState, error),
) error {
	data, err := store.codecs.Marshal(batch)
	if err != nil {
		return err
	}
	if store.state == nil {
		state, err := capture()
		if err != nil {
			return err
		}
		store.state, err = store.codecs.MarshalRunState(state)
		if err != nil {
			return err
		}
	}
	store.batches = append(store.batches, data)
	return nil
}

type countingWindow struct {
	gent.CompactionStrategy
	calls int
}

func (strategy *countingWindow) Compact(execCtx *gent.ExecutionContext,
	input *gent.CompactionInput,
) (*gent.HistoryUpdate, error) {
	strategy.calls++
	return strategy.CompactionStrategy.Compact(execCtx, input)
}

func TestCompactionBaselineSurvivesNewTurns(t *testing.T) {
	type input struct{ restore, snapshot, prefix bool }
	type expected struct {
		compactions []int
		counters    []int64
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	want := expected{[]int{1, 1, 1, 2}, []int64{10, 10, 10, 20}}
	cases := []testCase{
		{"reused context", input{}, want},
		{"full replay", input{restore: true}, want},
		{"RunStateSnapshot and tail", input{restore: true, snapshot: true}, want},
		{"prefix counter", input{restore: true, snapshot: true, prefix: true}, want},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			model := tt.NewMockModel()
			for range 4 {
				model.AddResponse("Done", 0, 0)
			}
			codecs := gent.NewCodecRegistry()
			store := &baselineRecorder{codecs: codecs}
			strategy := &countingWindow{CompactionStrategy: compaction.NewSlidingWindow(2)}
			trigger := compaction.NewStatThresholdTrigger()
			if test.input.prefix {
				trigger.OnCounterPrefix("test:", 10)
			} else {
				trigger.OnCounter("test:work", 10)
			}
			runner := executor.NewRunner(react.NewAgent(model), &gent.Config{
				Codecs: codecs, Recorder: store, CompactionTrigger: trigger, CompactionStrategy: strategy})
			execCtx, err := runner.NewExecutionContext(context.Background(), test.name)
			if !assert.NoError(t, err) {
				return
			}
			t.Cleanup(execCtx.Release)
			execCtx.Stats().IncrCounter("test:work", 10)
			actual := expected{}
			for turn := range 4 {
				if turn == 3 {
					execCtx.Stats().IncrCounter("test:work", 10)
				}
				value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Answer.")))
				if !assert.NoError(t, err) {
					return
				}
				assert.Equal(t, "Done", value)
				assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionFinished,
					TurnId: execCtx.TurnId(), TerminationReason: gent.TerminationSuccess,
					Output: []gent.ContentPart{gent.TextPart("Done")}}, result)
				actual.compactions = append(actual.compactions, strategy.calls)
				actual.counters = append(actual.counters, execCtx.Stats().GetCounter("test:work"))
				if !test.input.restore {
					continue
				}
				state := gent.NewRunState(execCtx.RunId())
				start := 0
				if test.input.snapshot {
					loaded, err := codecs.UnmarshalRunState(store.state)
					if !assert.NoError(t, err) || !assert.Equal(t, gent.RunStateReady, loaded.Status) {
						return
					}
					state, start = loaded.State, 1
				}
				for _, data := range store.batches[start:] {
					var batch gent.SaveBatch
					if !assert.NoError(t, codecs.Unmarshal(data, &batch)) {
						return
					}
					applied, err := state.Apply(&batch, codecs)
					if !assert.NoError(t, err) || !assert.Equal(t, gent.ApplyApplied, applied.Status) {
						return
					}
				}
				execCtx.Release()
				restored, err := runner.Restore(context.Background(), state)
				if !assert.NoError(t, err) || !assert.Equal(t, gent.RestoreReady, restored.Status) {
					return
				}
				execCtx = restored.Context
				t.Cleanup(execCtx.Release)
			}
			assert.Equal(t, test.expected, actual)
		})
	}
}
