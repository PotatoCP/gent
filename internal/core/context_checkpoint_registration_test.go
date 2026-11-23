package gent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func registrationRecoveryCodecs(t *testing.T, custom bool,
	compression CodecCompression,
) *CodecRegistry {
	t.Helper()
	codecs := NewCodecRegistry(WithCodecCompression(compression))
	for _, id := range []string{"nil", "any", "[]/string"} {
		assert.EqualError(t, registerTestRecord(codecs, id, custom),
			fmt.Sprintf("gent: codec name %q is reserved", id))
	}
	for _, id := range []string{"app.\xff", "app.\xe2\x82"} {
		assert.EqualError(t, registerTestRecord(codecs, id, custom),
			fmt.Sprintf("gent: codec name %q is not valid UTF-8", id))
	}
	if !assert.NoError(t, registerTestRecord(codecs, "app.値", custom)) {
		t.FailNow()
	}
	return codecs
}

func TestCheckpointRecoveryAfterRejectedRegistration(t *testing.T) {
	type input struct {
		custom      bool
		compression CodecCompression
	}
	type expected struct{ value registrationRecord }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	value := registrationRecord{Value: strings.Repeat("accepted custom state ", 1024)}
	cases := []testCase{
		{"reflective plain", input{false, CodecCompressionNone}, expected{value}},
		{"reflective compressed", input{false, CodecCompressionSnappy}, expected{value}},
		{"custom plain", input{true, CodecCompressionNone}, expected{value}},
		{"custom compressed", input{true, CodecCompressionSnappy}, expected{value}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := registrationRecoveryCodecs(t, test.input.custom, test.input.compression)
			recorder := &runStateRecorder{codecs: codecs, capture: true}
			loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				if err := execCtx.SetCheckpointState("buffers", test.expected.value); err != nil {
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
			output, result, err := runner.NewTurn(execCtx, NewTask(TextPart("Save the custom state.")))
			if !assert.NoError(t, err) {
				return
			}
			assert.Equal(t, "", output)
			assert.Equal(t, &ExecutionResult{Status: ExecutionPaused, TurnId: execCtx.TurnId(),
				TerminationReason: TerminationStopped}, result)
			execCtx.Release()
			if !assert.Greater(t, len(recorder.batches), 1) {
				return
			}
			for _, path := range []string{"full batches", "RunStateSnapshot plus tail",
				"latest RunStateSnapshot"} {
				t.Run(path, func(t *testing.T) {
					// A fresh registry models a new worker. Rejection cannot leave bindings that
					// change the meaning of either a saved component or surrounding framework data.
					fresh := registrationRecoveryCodecs(t, test.input.custom, test.input.compression)
					verifyRegistrationRecovery(t, fresh, recorder, execCtx.RunId(), execCtx.TurnId(),
						path, test.expected.value)
				})
			}
		})
	}
}

func verifyRegistrationRecovery(t *testing.T, fresh *CodecRegistry, recorder *runStateRecorder,
	runId, turnId, path string, want registrationRecord,
) {
	t.Helper()
	state := NewRunState(runId)
	start := 0
	if path != "full batches" {
		start = len(recorder.states)
		if path == "RunStateSnapshot plus tail" {
			start--
		}
		loaded, err := fresh.UnmarshalRunState(
			encodeRunState(t, recorder.codecs, recorder.states[start-1]))
		if !assert.NoError(t, err) || !assert.Equal(t, RunStateReady, loaded.Status) {
			return
		}
		state = loaded.State
	}
	for _, batch := range recorder.batches[start:] {
		applyRecordedState(t, state, fresh, batch)
	}
	assert.Equal(t, encodeRunState(t, recorder.codecs, recorder.states[len(recorder.states)-1]),
		encodeRunState(t, fresh, state))
	continued := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
		saved, found, err := execCtx.CheckpointState("buffers")
		if err != nil {
			return nil, err
		}
		assert.True(t, found)
		assert.Equal(t, want, saved)
		return acceptTurnText(execCtx, "Restored custom state.")
	}}
	tail := &runStateRecorder{codecs: fresh, capture: true}
	runner := NewRunner[string](continued, &Config{Codecs: fresh, Recorder: tail})
	restored, err := runner.Restore(context.Background(), state)
	if !assert.NoError(t, err) || !assert.Equal(t, RestoreReady, restored.Status) {
		return
	}
	defer restored.Context.Release()
	assert.Equal(t, &RestoreResult{Status: RestoreReady, Context: restored.Context}, restored)
	output, result, err := runner.Continue(restored.Context)
	assert.NoError(t, err)
	assert.Equal(t, "Restored custom state.", output)
	assert.Equal(t, &ExecutionResult{Status: ExecutionFinished, TurnId: turnId,
		TerminationReason: TerminationSuccess, Output: []ContentPart{TextPart(output)}}, result)
	if !assert.NotEmpty(t, tail.batches) {
		return
	}
	completed := NewRunState(runId)
	for _, batches := range [][][]byte{recorder.batches, tail.batches} {
		for _, batch := range batches {
			applyRecordedState(t, completed, fresh, batch)
		}
	}
	assert.Equal(t, encodeRunState(t, fresh, tail.states[len(tail.states)-1]),
		encodeRunState(t, fresh, completed))
}
