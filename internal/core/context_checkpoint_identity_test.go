package gent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func assertIdentityValue(t *testing.T, codecs *CodecRegistry, want, actual any) {
	t.Helper()
	assert.Equal(t, reflect.TypeOf(want), reflect.TypeOf(actual))
	if reflect.ValueOf(want).Kind() == reflect.Map {
		// The entry values and concrete types are checked independently of the wire encoder.
		// Pointer addresses need not match those of the original input or another decoded copy.
		assert.Equal(t, identityMapValues(want), identityMapValues(actual))
	} else {
		assert.Equal(t, want, actual)
	}
	wantEncoded, err := codecs.Encode(want)
	assert.NoError(t, err)
	encoded, err := codecs.Encode(actual)
	assert.NoError(t, err)
	assert.Equal(t, wantEncoded, encoded)
}

func TestCheckpointValueIdentityAndRejectedReplacement(t *testing.T) {
	type input struct {
		value       any
		replacement any
	}
	type expected struct{ errorText string }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	first, second := identityTimes()
	message := "gent: invalid UTF-8 in string value of type string"
	cases := []testCase{
		{"zero pointers", input{value: identityZeroMap()}, expected{}},
		{"distinct time keys", input{value: map[time.Time]string{
			first: "first", first.Add(time.Second): "second"}}, expected{}},
		{"short string rejection", input{"accepted state", string([]byte{255})}, expected{message}},
		{"long string rejection", input{"accepted state", strings.Repeat("a", 300) + string([]byte{255})},
			expected{message}},
		{"string key collision", input{"accepted state",
			map[string]int{string([]byte{255}): 1, string([]byte{254}): 2}}, expected{message}},
		{"error message rejection", input{"accepted state", errors.New(string([]byte{255}))},
			expected{"gent: invalid UTF-8 in saved error message"}},
		{"component name rejection", input{"accepted state", EncodedValue{
			Type: string([]byte{255}), Version: 1}}, expected{
			"gent: invalid UTF-8 in encoded value type"}},
		{"stream IDs rejection", input{"accepted state", StreamChunk{
			SourceMessageIds: []string{string([]byte{255})}}}, expected{
			"encode github.com/rickchristie/gent/StreamChunk: " +
				"gent: invalid UTF-8 in stream source message ID"}},
		{"time key collision", input{"accepted state",
			map[time.Time]string{first: "first", second: "second"}}, expected{
			"gent: map map[time.Time]string has keys that collide after time normalization"}},
		{"nested time key collision", input{"accepted state", map[identityTimeKey]string{
			{Time: first}: "first", {Time: second}: "second"}}, expected{
			"gent: map map[gent.identityTimeKey]string has keys that collide after time normalization"}},
	}
	for _, test := range cases {
		for _, compression := range []CodecCompression{CodecCompressionNone, CodecCompressionSnappy} {
			t.Run(test.name+"/"+string(compression), func(t *testing.T) {
				codecs := identityCodecs(t, compression)
				recorder := &runStateRecorder{codecs: codecs, capture: true}
				nextValue := test.input.value
				loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
					if err := execCtx.SetCheckpointState("buffers", nextValue); err != nil {
						return nil, err
					}
					entry := &HistoryEntry{Messages: []*MessageContent{{Role: ChatMessageTypeHuman,
						Parts: []ContentPart{TextPart("Keep the accepted state.")}}}}
					entry.SetMetadata("app:map", nextValue)
					if err := execCtx.AppendRecord(entry, true); err != nil {
						return nil, err
					}
					execCtx.PublishCommonEvent("app:values", "Keep accepted values.", nextValue)
					execCtx.Stop()
					return &AgentLoopResult{Action: LAContinue}, nil
				}}
				runner := NewRunner[string](loop, &Config{Codecs: codecs, Recorder: recorder})
				execCtx, err := runner.NewExecutionContext(context.Background(), t.Name())
				if !assert.NoError(t, err) {
					return
				}
				t.Cleanup(execCtx.Release)
				task := NewTask(TextPart(strings.Repeat("Keep the original accepted task. ", 512)))
				output, result, err := runner.NewTurn(execCtx, task)
				if !assert.NoError(t, err) {
					return
				}
				assert.Equal(t, "", output)
				assert.Equal(t, &ExecutionResult{Status: ExecutionPaused, TurnId: execCtx.TurnId(),
					TerminationReason: TerminationStopped}, result)
				snapshots := make([][]byte, len(recorder.states))
				for i, state := range recorder.states {
					snapshots[i] = encodeRunState(t, codecs, state)
				}
				acceptedBatches := recorder.batches
				if test.expected.errorText != "" {
					nextValue = test.input.replacement
					output, result, err = runner.Continue(execCtx)
					assert.EqualError(t, err, fmt.Sprintf(
						"gent: Loop.Next *gent.turnTestLoop failed in run %q turn %q iteration 1: %s",
						execCtx.RunId(), execCtx.TurnId(), test.expected.errorText))
					assert.Equal(t, "", output)
					assert.Nil(t, result)
					assert.Equal(t, acceptedBatches, recorder.batches)
					for i, state := range recorder.states {
						assert.Equal(t, snapshots[i], encodeRunState(t, codecs, state))
					}
				}
				saved, found, err := execCtx.CheckpointState("buffers")
				assert.NoError(t, err)
				assert.True(t, found)
				assertIdentityValue(t, codecs, test.input.value, saved)
				execCtx.Release()
				compressed := false
				for _, data := range recorder.batches {
					var format struct{ FormatVersion int }
					assert.NoError(t, json.Unmarshal(data, &format))
					compressed = compressed || format.FormatVersion == 2
				}
				assert.Equal(t, compression == CodecCompressionSnappy, compressed)
				for _, path := range []string{"full batches", "RunStateSnapshot plus tail",
					"latest RunStateSnapshot"} {
					t.Run(path, func(t *testing.T) {
						fresh := identityCodecs(t, compression)
						verifyIdentityContinuation(t, fresh, recorder, snapshots, execCtx.RunId(),
							execCtx.TurnId(), path, test.input.value)
					})
				}
			})
		}
	}
}

func verifyIdentityContinuation(t *testing.T, codecs *CodecRegistry, recorder *runStateRecorder,
	snapshots [][]byte, runId, turnId, path string, want any,
) {
	t.Helper()
	state, start := NewRunState(runId), 0
	if path != "full batches" {
		start = len(snapshots)
		if path == "RunStateSnapshot plus tail" {
			start--
		}
		loaded, err := codecs.UnmarshalRunState(snapshots[start-1])
		if !assert.NoError(t, err) || !assert.Equal(t, RunStateReady, loaded.Status) {
			t.FailNow()
		}
		assert.Equal(t, &RunStateLoadResult{Status: RunStateReady, State: loaded.State}, loaded)
		state = loaded.State
	}
	for _, data := range recorder.batches[start:] {
		applyRecordedState(t, state, codecs, data)
	}
	assert.Equal(t, snapshots[len(snapshots)-1], encodeRunState(t, codecs, state))
	view := NewRunView(runId)
	applyMapArchive(t, view, codecs, recorder.batches)
	data := marshalMapArchive(t, codecs, view)
	view = loadMapArchive(t, codecs, data)
	assert.Equal(t, data, marshalMapArchive(t, codecs, view))
	metadata, found := mapHistoryEntry(t, view.History).GetMetadata("app:map")
	assert.True(t, found)
	assertIdentityValue(t, codecs, want, metadata)
	verifyMapReceipts(t, codecs, recorder.batches[len(recorder.batches)-1], state, view)
	loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
		saved, found, err := execCtx.CheckpointState("buffers")
		if err != nil {
			return nil, err
		}
		assert.True(t, found)
		assertIdentityValue(t, codecs, want, saved)
		return acceptTurnText(execCtx, "Restored accepted state.")
	}}
	tail := &runStateRecorder{codecs: codecs, capture: true}
	runner := NewRunner[string](loop, &Config{Codecs: codecs, Recorder: tail})
	restored, err := runner.Restore(context.Background(), state)
	if !assert.NoError(t, err) || !assert.Equal(t, RestoreReady, restored.Status) {
		t.FailNow()
	}
	defer restored.Context.Release()
	assert.Equal(t, &RestoreResult{Status: RestoreReady, Context: restored.Context}, restored)
	output, result, err := runner.Continue(restored.Context)
	assert.NoError(t, err)
	assert.Equal(t, "Restored accepted state.", output)
	assert.Equal(t, &ExecutionResult{Status: ExecutionFinished, TurnId: turnId,
		TerminationReason: TerminationSuccess, Output: []ContentPart{TextPart(output)}}, result)
	applyMapArchive(t, view, codecs, tail.batches)
	full, complete := NewRunView(runId), NewRunState(runId)
	for _, batches := range [][][]byte{recorder.batches, tail.batches} {
		applyMapArchive(t, full, codecs, batches)
		for _, batch := range batches {
			applyRecordedState(t, complete, codecs, batch)
		}
	}
	assert.Equal(t, marshalMapArchive(t, codecs, full), marshalMapArchive(t, codecs, view))
	assert.Equal(t, encodeRunState(t, codecs, tail.states[len(tail.states)-1]),
		encodeRunState(t, codecs, complete))
}
