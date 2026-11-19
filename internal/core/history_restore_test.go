package gent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type savedCounter struct{ count int }

func (c *savedCounter) Add(n int)              { c.count += n }
func (c *savedCounter) Value() int             { return c.count }
func (c *savedCounter) CheckpointType() string { return "fixture/counter" }
func (c *savedCounter) CheckpointVersion() int { return 1 }
func (c *savedCounter) SaveCheckpointState() (*EncodedValue, error) {
	value, err := NewCodecRegistry().Encode(c.count)
	return &value, err
}
func (c *savedCounter) RestoreCheckpointState(value *EncodedValue) error {
	decoded, err := decodeAs[int](NewCodecRegistry(), *value)
	if err != nil {
		return err
	}
	c.count = decoded
	return nil
}

type counterLoop struct {
	turnTestLoop
	counter *savedCounter
}

func (loop *counterLoop) ConfigureExecution(execCtx *ExecutionContext) error {
	return execCtx.BindCheckpointState("counter", loop.counter)
}

func TestCheckpointExtensions(t *testing.T) {
	codecs := NewCodecRegistry()
	recorder := &runStateRecorder{codecs: codecs, capture: true}
	counter := &savedCounter{}
	loop := &counterLoop{counter: counter, turnTestLoop: turnTestLoop{
		next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
			counter.Add(17)
			execCtx.Stop()
			return &AgentLoopResult{Action: LAContinue}, nil
		},
	}}
	config := &Config{Codecs: codecs, Recorder: recorder}
	runner := NewRunner[string](loop, config)
	execCtx, err := runner.NewExecutionContext(context.Background(), "counter")
	require.NoError(t, err)
	defer execCtx.Release()
	_, result, err := runner.NewTurn(execCtx, NewTask(TextPart("Count.")))
	require.NoError(t, err)
	assert.Equal(t, ExecutionPaused, result.Status)
	counter.Add(100)
	data := encodeRunState(t, codecs, recorder.states[len(recorder.states)-1])
	loaded, err := codecs.UnmarshalRunState(data)
	require.NoError(t, err)
	missing := NewRunner[string](&turnTestLoop{}, config)
	rejected, err := missing.Restore(context.Background(), loaded.State)
	assert.NoError(t, err)
	assert.Equal(t, &RestoreResult{Status: RestoreIncompatible,
		Detail: `gent: missing or incompatible saved component "counter"`}, rejected)
	assert.Equal(t, data, encodeRunState(t, codecs, loaded.State))

	freshCounter := &savedCounter{}
	freshLoop := &counterLoop{counter: freshCounter, turnTestLoop: turnTestLoop{
		next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
			freshCounter.Add(3)
			return acceptTurnText(execCtx, "Counted.")
		},
	}}
	replacement := NewRunner[string](freshLoop, config)
	fresh, err := replacement.Restore(context.Background(), loaded.State)
	require.NoError(t, err)
	require.Equal(t, RestoreReady, fresh.Status)
	defer fresh.Context.Release()
	assert.Equal(t, 17, freshCounter.Value())
	_, _, err = replacement.Continue(fresh.Context)
	require.NoError(t, err)
	var last SaveBatch
	require.NoError(t, codecs.Unmarshal(recorder.batches[len(recorder.batches)-1], &last))
	value, err := codecs.Decode(last.Checkpoint.Contexts[0].Extensions["counter"].Value)
	assert.NoError(t, err)
	assert.Equal(t, 20, value)
}

func TestRestoreUsesFreshOwnerContext(t *testing.T) {
	codecs := NewCodecRegistry()
	recorder := &runStateRecorder{codecs: codecs, capture: true}
	loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
		return acceptTurnText(execCtx, "Done")
	}}
	runner := NewRunner[string](loop, &Config{Codecs: codecs, Recorder: recorder})
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Hour))
	defer cancel()
	execCtx, err := runner.NewExecutionContext(ctx, "deadline")
	require.NoError(t, err)
	defer execCtx.Release()
	_, _, err = runner.NewTurn(execCtx, NewTask(TextPart("Start.")))
	require.NoError(t, err)
	cancel()
	loaded, err := codecs.UnmarshalRunState(encodeRunState(t, codecs,
		recorder.states[len(recorder.states)-1]))
	require.NoError(t, err)
	restored, err := runner.Restore(context.Background(), loaded.State)
	require.NoError(t, err)
	require.Equal(t, RestoreReady, restored.Status)
	defer restored.Context.Release()
	_, hasDeadline := restored.Context.Context().Deadline()
	assert.False(t, hasDeadline)
	assert.NoError(t, restored.Context.Context().Err())
	value, result, err := runner.NewTurn(restored.Context, NewTask(TextPart("Next.")))
	assert.NoError(t, err)
	assert.Equal(t, "Done", value)
	assert.Equal(t, ExecutionFinished, result.Status)
}

func TestRestorePreservesRemainingLimits(t *testing.T) {
	type input struct {
		limit     Limit
		key       StatKey
		initial   int64
		competing StatKey
		gauge     bool
	}
	type expected struct{ value float64 }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"counter", input{limit: Limit{Type: LimitExactKey, Key: SCInputTokens, MaxValue: 10},
			key: SCInputTokens, initial: 9}, expected{11}},
		{"competing prefix", input{limit: Limit{Type: LimitKeyPrefix, Key: SCToolCallsFor, MaxValue: 3},
			key: SCToolCallsFor + "tool2", initial: 2, competing: SCToolCallsFor + "tool1"}, expected{4}},
		{"consecutive errors", input{limit: Limit{Type: LimitExactKey,
			Key: SGToolCallsErrorConsecutive, MaxValue: 3},
			key: SGToolCallsErrorConsecutive, initial: 2, gauge: true}, expected{4}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := NewCodecRegistry()
			recorder := &runStateRecorder{codecs: codecs, capture: true}
			add := func(execCtx *ExecutionContext, value int64) {
				if test.input.gauge {
					execCtx.Stats().IncrGauge(test.input.key, float64(value))
				} else {
					execCtx.Stats().IncrCounter(test.input.key, value)
				}
			}
			first := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				add(execCtx, test.input.initial)
				if test.input.competing != "" {
					execCtx.Stats().IncrCounter(test.input.competing, 3)
				}
				execCtx.Stop()
				return &AgentLoopResult{Action: LAContinue}, nil
			}}
			config := &Config{Limits: []Limit{test.input.limit}, Codecs: codecs, Recorder: recorder}
			runner := NewRunner[string](first, config)
			execCtx, err := runner.NewExecutionContext(context.Background(), "usage")
			require.NoError(t, err)
			defer execCtx.Release()
			_, result, err := runner.NewTurn(execCtx, NewTask(TextPart("Start.")))
			require.NoError(t, err)
			assert.Equal(t, TerminationStopped, result.TerminationReason)
			loaded, err := codecs.UnmarshalRunState(encodeRunState(t, codecs,
				recorder.states[len(recorder.states)-1]))
			require.NoError(t, err)
			steps := 0
			next := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				steps++
				add(execCtx, 1)
				if steps == 1 {
					assert.NoError(t, execCtx.Context().Err())
				}
				return &AgentLoopResult{Action: LAContinue}, nil
			}}
			replacement := NewRunner[string](next, config)
			restored, err := replacement.Restore(context.Background(), loaded.State)
			require.NoError(t, err)
			require.Equal(t, RestoreReady, restored.Status)
			defer restored.Context.Release()
			assert.Equal(t, execCtx.Stats().Counters(), restored.Context.Stats().Counters())
			assert.Equal(t, execCtx.Stats().Gauges(), restored.Context.Stats().Gauges())
			_, result, err = replacement.Continue(restored.Context)
			require.NoError(t, err)
			assert.Equal(t, 2, steps)
			assert.Equal(t, &ExecutionResult{Status: ExecutionPaused, TurnId: execCtx.TurnId(),
				TerminationReason: TerminationLimitExceeded,
				ExceededLimit: &LimitExceeded{Limit: &test.input.limit, MatchedKey: test.input.key,
					CurrentValue: test.expected.value}}, result)
		})
	}
}

func TestRestoreRejectsInvalidCheckpointData(t *testing.T) {
	type input struct{ change func(*SaveBatch) }
	type expected struct {
		status RestoreStatus
		detail string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"unknown task codec", input{func(batch *SaveBatch) {
			batch.Checkpoint.Contexts[0].Task.Type = "missing/task"
		}}, expected{RestoreIncompatible, `gent: restore Task: gent: unknown codec "missing/task"`}},
		{"unsupported task codec version", input{func(batch *SaveBatch) {
			batch.Checkpoint.Contexts[0].Task.Version = 2
		}}, expected{RestoreIncompatible, "gent: restore Task: gent: unsupported pointer version 2"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := NewCodecRegistry()
			runId, batches := recordedStateFixture(t, 1, codecs, nil)
			state := NewRunState(runId)
			for index, data := range batches {
				var batch SaveBatch
				require.NoError(t, codecs.Unmarshal(data, &batch))
				if index == len(batches)-1 {
					test.input.change(&batch)
					digest, err := BatchDigest(&batch, codecs)
					require.NoError(t, err)
					batch.BatchId = digest
				}
				applied, err := state.Apply(&batch, codecs)
				require.NoError(t, err)
				require.Equal(t, ApplyApplied, applied.Status)
			}
			runner := NewRunner[string](&turnTestLoop{}, &Config{})
			restored, err := runner.Restore(context.Background(), state)
			assert.NoError(t, err)
			assert.Equal(t, &RestoreResult{Status: test.expected.status,
				Detail: test.expected.detail}, restored)
		})
	}
}
