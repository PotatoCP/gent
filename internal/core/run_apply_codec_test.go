package gent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

type runStateEncodeCodec struct{ failure error }

func (runStateEncodeCodec) Type() string { return "test/run-state-value" }
func (runStateEncodeCodec) Version() int { return 1 }
func (codec runStateEncodeCodec) Encode(value any) (json.RawMessage, error) {
	if codec.failure != nil {
		return nil, codec.failure
	}
	return json.Marshal(value)
}
func (runStateEncodeCodec) Decode(data json.RawMessage) (any, error) {
	var value runStateCustomValue
	err := json.Unmarshal(data, &value)
	return value, err
}

func TestReplaySeparatesMissingEncoderAndOperationFailure(t *testing.T) {
	type input struct {
		archive  bool
		register bool
		failure  error
	}
	type expected struct {
		result  *ApplyStateResult
		cause   error
		message string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	codecs := NewCodecRegistry()
	assert.NoError(t, codecs.Register(runStateCustomValue{}, runStateEncodeCodec{}))
	runId, batches := recordedStateFixture(t, 1, codecs, runStateCustomValue{Label: "saved"})
	index := -1
	for i, encoded := range batches {
		var batch SaveBatch
		assert.NoError(t, codecs.Unmarshal(encoded, &batch))
		for _, event := range batch.Events {
			if change := recordedHistoryChange(event); change != nil {
				for _, entry := range change.AppendedEntries {
					if len(entry.Metadata) > 0 {
						index = i
					}
				}
			}
		}
		if index >= 0 {
			break
		}
	}
	if !assert.GreaterOrEqual(t, index, 0) {
		return
	}
	path := strings.Join([]string{"gent.SaveBatch.Events", "gent.RecordedEvent.Payload",
		"gent.HistoryChange.AppendedEntries",
		"gent.HistoryEntry.Metadata"}, ": ")
	failure := errors.New("encode service failed")
	failureMessage := fmt.Sprintf("gent: validate batch for run %q: %s: %s", runId, path,
		"encode test/run-state-value: encode service failed")
	missing := &ApplyStateResult{Status: ApplyIncompatible,
		Detail: path + ": gent: no codec for gent.runStateCustomValue"}
	tests := []testCase{
		{"state missing codec", input{}, expected{result: missing}},
		{"archive missing codec", input{archive: true}, expected{result: missing}},
		{"state codec error", input{register: true, failure: failure},
			expected{cause: failure, message: failureMessage}},
		{"archive codec error", input{archive: true, register: true, failure: failure},
			expected{cause: failure, message: failureMessage}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := NewCodecRegistry()
			if test.input.register {
				assert.NoError(t, registry.Register(runStateCustomValue{},
					runStateEncodeCodec{failure: test.input.failure}))
			}
			state, view := NewRunState(runId), NewRunView(runId)
			apply := state.Apply
			capture := func() []byte { return encodeRunState(t, codecs, state) }
			if test.input.archive {
				apply = view.Apply
				capture = func() []byte {
					data, err := codecs.Marshal(view)
					assert.NoError(t, err)
					return data
				}
			}
			for _, encoded := range batches[:index] {
				var batch SaveBatch
				assert.NoError(t, codecs.Unmarshal(encoded, &batch))
				result, err := apply(&batch, registry)
				assert.NoError(t, err)
				assert.Equal(t, &ApplyStateResult{Status: ApplyApplied}, result)
			}
			before := capture()
			var batch SaveBatch
			assert.NoError(t, codecs.Unmarshal(batches[index], &batch))
			result, err := apply(&batch, registry)
			assert.Equal(t, test.expected.result, result)
			if test.expected.cause != nil {
				if assert.ErrorIs(t, err, test.expected.cause) {
					assert.Equal(t, test.expected.message, err.Error())
				}
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, before, capture())
			after, err := codecs.Marshal(&batch)
			assert.NoError(t, err)
			assert.Equal(t, batches[index], after)
			// Correct configuration can use the same unmodified inputs immediately.
			result, err = apply(&batch, codecs)
			assert.NoError(t, err)
			assert.Equal(t, &ApplyStateResult{Status: ApplyApplied}, result)
		})
	}
}
