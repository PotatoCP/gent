package executor_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type resumeLoop struct {
	configured int
	calls      int
	step       int
}

func (loop *resumeLoop) ConfigureExecution(*gent.ExecutionContext) error {
	loop.configured++
	loop.step = 0
	return nil
}

func (loop *resumeLoop) Next(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
	loop.calls++
	loop.step++
	if loop.step == 1 {
		return &gent.AgentLoopResult{Action: gent.LAContinue}, nil
	}
	task, err := execCtx.Task()
	if err != nil {
		return nil, err
	}
	return acceptTestOutput(execCtx, fmt.Sprintf("%s:%d", gent.ContentText(task.Messages[0].Parts),
		loop.step))
}

func (*resumeLoop) Output() gent.Output[string] { return output.NewTextOutput() }
func (*resumeLoop) CheckpointType() string      { return "test/resume-loop" }
func (*resumeLoop) CheckpointVersion() int      { return 1 }
func (loop *resumeLoop) SaveCheckpointState() (*gent.EncodedValue, error) {
	value, err := gent.NewCodecRegistry().Encode(loop.step)
	return &value, err
}
func (loop *resumeLoop) RestoreCheckpointState(saved *gent.EncodedValue) error {
	value, err := gent.NewCodecRegistry().Decode(*saved)
	if err != nil {
		return err
	}
	step, ok := value.(int)
	if !ok {
		return fmt.Errorf("invalid loop step %T", value)
	}
	loop.step = step
	return nil
}

func TestRestoreConfiguresBeforeRestoringState(t *testing.T) {
	type input struct {
		phase     gent.ExecutionPhase
		limits    []gent.Limit
		batchOnly bool
	}
	type expected struct{ restoredStep, calls int }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"initial", input{phase: gent.PhaseInitial}, expected{0, 2}},
		{"before iteration", input{phase: gent.PhaseIterationReady}, expected{0, 2}},
		{"completed iteration", input{phase: gent.PhaseIterationCompleted}, expected{1, 1}},
		{"new limits", input{phase: gent.PhaseIterationCompleted, limits: []gent.Limit{
			{Type: gent.LimitExactKey, Key: gent.SCIterations, MaxValue: 20}}}, expected{1, 1}},
		{"batch only", input{phase: gent.PhaseIterationCompleted, batchOnly: true}, expected{1, 1}},
		{"terminal custom state", input{phase: gent.PhaseTerminal}, expected{2, 0}},
	}
	for _, test := range cases {
		t.Run(
			test.name,
			func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				store := &executionRecorder{
					codecs: gent.NewCodecRegistry(),
					afterSave: func(
						batch *gent.SaveBatch,
					) {
						if batch.Checkpoint.Phase == test.input.phase {
							cancel()
						}
					},
				}
				runner := executor.NewRunner[string](&resumeLoop{}, &gent.Config{Codecs: store.codecs,
					Recorder: store})
				original, err := runner.NewExecutionContext(ctx, "checkpoint")
				require.NoError(t, err)
				defer original.Release()
				_, _, err = runner.NewTurn(original, gent.NewTask(gent.TextPart("original task")))
				if test.input.phase == gent.PhaseTerminal {
					assert.NoError(t, err)
				} else {
					assert.ErrorIs(t, err, context.Canceled)
				}
				codecs := gent.NewCodecRegistry()
				state := store.latest(t, codecs)
				if test.input.batchOnly {
					state = gent.NewRunState(original.RunId())
					for _, data := range store.batches {
						var batch gent.SaveBatch
						require.NoError(t, codecs.Unmarshal(data, &batch))
						applied, err := state.Apply(&batch, codecs)
						require.NoError(t, err)
						require.Equal(t, gent.ApplyApplied, applied.Status)
					}
				}
				loop := &resumeLoop{}
				fresh := executor.NewRunner[string](
					loop,
					&gent.Config{
						Codecs: codecs,
						Limits: test.input.limits,
					},
				)
				restored, err := fresh.Restore(context.Background(), state)
				require.NoError(t, err)
				require.Equal(t, gent.RestoreReady, restored.Status)
				defer restored.Context.Release()
				assert.Equal(t, &resumeLoop{configured: 1, step: test.expected.restoredStep}, loop)
				var value string
				var result *gent.ExecutionResult
				if test.input.phase == gent.PhaseTerminal {
					value, result, err = fresh.GetLastTurnResult(restored.Context)
				} else {
					value, result, err = fresh.Continue(restored.Context)
				}
				assert.NoError(t, err)
				assert.Equal(t, "original task:2", value)
				assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionFinished,
					TurnId: original.TurnId(), TerminationReason: gent.TerminationSuccess,
					Output: []gent.ContentPart{gent.TextPart("original task:2")}}, result)
				assert.Equal(t, &resumeLoop{configured: 1, calls: test.expected.calls, step: 2}, loop)
			},
		)
	}
}
