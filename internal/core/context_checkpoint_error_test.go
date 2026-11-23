package gent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCheckpointErrorRepresentation(t *testing.T) {
	type input struct {
		value       any
		replacement any
		custom      bool
	}
	type expected struct{ errorText string }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	problem := codecConcreteError{"saved problem"}
	pointer := &codecPointerError{"saved pointer problem"}
	valueType := reflect.TypeFor[codecConcreteError]()
	pointerType := reflect.TypeFor[*codecPointerError]()
	valueError := errorDestinationMessage(valueType, valueType)
	pointerError := errorDestinationMessage(pointerType, pointerType)
	accepted := []error{&SavedError{Message: "accepted problem", Codes: []string{"app:accepted"}}}
	first, second := identityTimes()
	nilKeys := map[error]string{nil: "first", (*SavedError)(nil): "second"}
	arrayKeys := map[[1]error]string{{nil}: "first", {(*SavedError)(nil)}: "second"}
	combinedKeys := map[codecNilErrorKey]string{{When: first}: "first",
		{When: second, Problem: (*SavedError)(nil)}: "second"}
	cases := []testCase{
		{"unique nil key", input{value: map[error]string{nil: "first"}}, expected{}},
		{"independent error keys", input{value: map[error]string{
			errors.New("same"): "first", errors.New("same"): "second"}}, expected{}},
		{"custom nil key", input{value: map[error]string{
			nil: "first", (*codecPointerError)(nil): "second"}, custom: true}, expected{}},
		{"reject nil keys", input{value: accepted, replacement: nilKeys},
			expected{nilKeyCollision(nilKeys)}},
		{"reject nil array keys", input{value: accepted, replacement: arrayKeys},
			expected{nilKeyCollision(arrayKeys)}},
		{"reject combined key conversion", input{value: accepted, replacement: combinedKeys},
			expected{nilKeyCollision(combinedKeys)}},
		{"reject value slice", input{value: accepted, replacement: []codecConcreteError{problem}},
			expected{valueError}},
		{"reject pointer array", input{value: accepted, replacement: [1]*codecPointerError{pointer}},
			expected{pointerError}},
		{"reject map key", input{value: accepted,
			replacement: map[codecConcreteError]string{problem: "entry"}}, expected{valueError}},
		{"reject map value", input{value: accepted,
			replacement: map[string]codecConcreteError{"entry": problem}}, expected{valueError}},
		{"reject nested field", input{value: accepted,
			replacement: codecNestedErrorRecord{codecErrorRecord{problem}}}, expected{
			"gent.codecNestedErrorRecord.Record: gent.codecErrorRecord.Problem: " + valueError}},
		{"custom value slice", input{value: []codecConcreteError{problem}, custom: true}, expected{}},
		{"custom pointer array", input{value: [1]*codecPointerError{pointer}, custom: true}, expected{}},
		{"custom nested field", input{value: codecNestedErrorRecord{codecErrorRecord{problem}},
			custom: true}, expected{}},
		{"custom map key", input{value: map[codecConcreteError]string{problem: "entry"}, custom: true},
			expected{}},
	}
	for _, test := range cases {
		for _, compression := range []CodecCompression{CodecCompressionNone, CodecCompressionSnappy} {
			t.Run(test.name+"/"+string(compression), func(t *testing.T) {
				codecs := errorValueCodecs(t, compression, test.input.custom)
				recorder := &runStateRecorder{codecs: codecs, capture: true}
				nextValue := test.input.value
				loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
					if err := execCtx.SetCheckpointState("buffers", nextValue); err != nil {
						return nil, err
					}
					entry := &HistoryEntry{Messages: []*MessageContent{{Role: ChatMessageTypeHuman,
						Parts: []ContentPart{TextPart("Keep the accepted error data.")}}}}
					entry.SetMetadata("app:map", nextValue)
					if err := execCtx.AppendRecord(entry, true); err != nil {
						return nil, err
					}
					execCtx.PublishCommonEvent("app:errors", "Keep the accepted error data.", nextValue)
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
				runStateSnapshots := make([][]byte, len(recorder.states))
				for i, state := range recorder.states {
					runStateSnapshots[i] = encodeRunState(t, codecs, state)
				}
				// Keep separate bytes so an accidental mutation cannot change the comparison value.
				acceptedBatches := make([][]byte, len(recorder.batches))
				for i, batch := range recorder.batches {
					acceptedBatches[i] = bytes.Clone(batch)
				}
				if test.expected.errorText != "" {
					nextValue = test.input.replacement
					output, result, err = runner.Continue(execCtx)
					assert.EqualError(t, err, fmt.Sprintf(
						"gent: Loop.Next *gent.turnTestLoop failed in run %q turn %q iteration 1: %s",
						execCtx.RunId(), execCtx.TurnId(), test.expected.errorText))
					assert.Equal(t, "", output)
					assert.Nil(t, result)
					assert.Equal(t, acceptedBatches, recorder.batches)
					assert.Len(t, recorder.states, len(runStateSnapshots))
					for i, state := range recorder.states {
						assert.Equal(t, runStateSnapshots[i], encodeRunState(t, codecs, state))
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
						fresh := errorValueCodecs(t, compression, test.input.custom)
						verifyIdentityContinuation(t, fresh, recorder, runStateSnapshots,
							execCtx.RunId(), execCtx.TurnId(), path, test.input.value)
					})
				}
			})
		}
	}
}
