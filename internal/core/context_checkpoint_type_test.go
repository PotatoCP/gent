package gent

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDerivedCheckpointStateRestores(t *testing.T) {
	type input struct{ value any }
	type expected struct{ value any }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	deep := reflect.Zero(reflect.SliceOf(pointerType(100))).Interface()
	cases := []testCase{
		{"one MiB element", input{[][1048576]byte(nil)}, expected{[][1048576]byte(nil)}},
		{"zero size element", input{[][1048576][0]byte(nil)}, expected{[][1048576][0]byte(nil)}},
		{"type depth boundary", input{deep}, expected{deep}},
		{"named slice value boundary", input{sliceAtDepth(100)}, expected{sliceAtDepth(100)}},
		{"named map 60 child levels", input{mapAtDepth(60)}, expected{mapAtDepth(60)}},
		{"named pointer value boundary", input{pointerAtDepth(100)}, expected{pointerAtDepth(100)}},
		{"named array value boundary", input{arrayAtDepth(100)}, expected{arrayAtDepth(100)}},
		{"mixed value boundary", input{mixedAtDepth(100)}, expected{mixedAtDepth(100)}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := depthCodecs(t, CodecCompressionSnappy)
			recorder := &runStateRecorder{codecs: codecs, capture: true}
			loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				if err := execCtx.SetCheckpointState("buffers", test.input.value); err != nil {
					return nil, err
				}
				execCtx.Stop()
				return &AgentLoopResult{Action: LAContinue}, nil
			}}
			runner := NewRunner[string](loop, &Config{Codecs: codecs, Recorder: recorder})
			execCtx, err := runner.NewExecutionContext(context.Background(), t.Name())
			if !assert.NoError(t, err) {
				return
			}
			t.Cleanup(execCtx.Release)
			value, result, err := runner.NewTurn(execCtx, NewTask(TextPart("Save the buffer state.")))
			if !assert.NoError(t, err) {
				return
			}
			assert.Equal(t, "", value)
			assert.Equal(t, &ExecutionResult{Status: ExecutionPaused, TurnId: execCtx.TurnId(),
				TerminationReason: TerminationStopped}, result)
			execCtx.Release()
			for _, path := range []string{"full batches", "RunStateSnapshot plus tail",
				"latest RunStateSnapshot"} {
				t.Run(path, func(t *testing.T) {
					verifyCheckpointTypeRecovery(t, recorder, execCtx.RunId(), execCtx.TurnId(),
						path, test.expected.value)
				})
			}
		})
	}
}

func TestCheckpointTypeRejectionKeepsAcceptedState(t *testing.T) {
	type input struct{ replacement any }
	type expected struct{ errorText string }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"element depth", input{reflect.Zero(reflect.SliceOf(pointerType(101))).Interface()},
			expected{"gent: type nesting exceeds 100"}},
		{"map key depth", input{
			reflect.Zero(reflect.MapOf(pointerType(101), reflect.TypeFor[string]())).Interface()},
			expected{"gent: type nesting exceeds 100"}},
		{"named slice value depth", input{sliceAtDepth(101)},
			expected{"gent: value nesting exceeds 100"}},
		{"named map value depth", input{mapAtDepth(101)},
			expected{"gent: value nesting exceeds 100"}},
		{"named array value depth", input{arrayAtDepth(102)},
			expected{"gent: value nesting exceeds 100"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := depthCodecs(t, CodecCompressionSnappy)
			recorder := &runStateRecorder{codecs: codecs, capture: true}
			var nextValue any = "accepted buffer state"
			loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				if err := execCtx.SetCheckpointState("buffers", nextValue); err != nil {
					return nil, err
				}
				execCtx.Stop()
				return &AgentLoopResult{Action: LAContinue}, nil
			}}
			runner := NewRunner[string](loop, &Config{Codecs: codecs, Recorder: recorder})
			execCtx, err := runner.NewExecutionContext(context.Background(), t.Name())
			if !assert.NoError(t, err) {
				return
			}
			t.Cleanup(execCtx.Release)
			_, result, err := runner.NewTurn(execCtx, NewTask(TextPart("Keep the accepted buffer.")))
			assert.NoError(t, err)
			assert.Equal(t, &ExecutionResult{Status: ExecutionPaused, TurnId: execCtx.TurnId(),
				TerminationReason: TerminationStopped}, result)
			acceptedBatches := recorder.batches
			acceptedState := encodeRunState(t, codecs, recorder.states[len(recorder.states)-1])
			nextValue = test.input.replacement
			value, result, err := runner.Continue(execCtx)
			// Stop paused before the first iteration completed. Continue resumes that iteration;
			// the failed replacement must not accept another batch or change its saved component.
			assert.EqualError(t, err, fmt.Sprintf(
				"gent: Loop.Next *gent.turnTestLoop failed in run %q turn %q iteration 1: %s",
				execCtx.RunId(), execCtx.TurnId(), test.expected.errorText))
			assert.Equal(t, "", value)
			assert.Nil(t, result)
			assert.Equal(t, acceptedBatches, recorder.batches)
			assert.Equal(t, acceptedState,
				encodeRunState(t, codecs, recorder.states[len(recorder.states)-1]))
			saved, found, err := execCtx.CheckpointState("buffers")
			assert.NoError(t, err)
			assert.True(t, found)
			assert.Equal(t, "accepted buffer state", saved)
			execCtx.Release()
			for _, path := range []string{"full batches", "RunStateSnapshot plus tail",
				"latest RunStateSnapshot"} {
				t.Run(path, func(t *testing.T) {
					verifyCheckpointTypeRecovery(t, recorder, execCtx.RunId(), execCtx.TurnId(),
						path, "accepted buffer state")
				})
			}
		})
	}
}

// Use the same save/load path for accepted types and for a failed replacement. Checkpoint state
// must remain usable after application code fails, with no need to resubmit its accepted task.
func verifyCheckpointTypeRecovery(t *testing.T, recorder *runStateRecorder, runId, turnId,
	path string, want any,
) {
	t.Helper()
	codecs := depthCodecs(t, CodecCompressionSnappy)
	state := NewRunState(runId)
	start := 0
	if path != "full batches" {
		start = len(recorder.states)
		if path == "RunStateSnapshot plus tail" {
			start--
		}
		loaded, err := codecs.UnmarshalRunState(
			encodeRunState(t, recorder.codecs, recorder.states[start-1]))
		if !assert.NoError(t, err) || !assert.Equal(t, RunStateReady, loaded.Status) {
			return
		}
		state = loaded.State
	}
	for _, batch := range recorder.batches[start:] {
		applyRecordedState(t, state, codecs, batch)
	}
	assert.Equal(t, encodeRunState(t, recorder.codecs, recorder.states[len(recorder.states)-1]),
		encodeRunState(t, codecs, state))
	loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
		saved, found, err := execCtx.CheckpointState("buffers")
		if err != nil {
			return nil, err
		}
		assert.True(t, found)
		assert.Equal(t, want, saved)
		return acceptTurnText(execCtx, "Restored buffer state.")
	}}
	runner := NewRunner[string](loop, &Config{Codecs: codecs})
	restored, err := runner.Restore(context.Background(), state)
	if !assert.NoError(t, err) || !assert.Equal(t, RestoreReady, restored.Status) {
		return
	}
	defer restored.Context.Release()
	assert.Equal(t, &RestoreResult{Status: RestoreReady, Context: restored.Context}, restored)
	value, result, err := runner.Continue(restored.Context)
	assert.NoError(t, err)
	assert.Equal(t, "Restored buffer state.", value)
	assert.Equal(t, &ExecutionResult{Status: ExecutionFinished, TurnId: turnId,
		TerminationReason: TerminationSuccess, Output: []ContentPart{TextPart(value)}}, result)
}
