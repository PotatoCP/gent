package gent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func recordSavedMap(t *testing.T, codecs *CodecRegistry, value any) (
	*runStateRecorder, string, string,
) {
	t.Helper()
	recorder := &runStateRecorder{codecs: codecs, capture: true}
	loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
		entry := &HistoryEntry{Messages: []*MessageContent{
			{Role: ChatMessageTypeHuman, Parts: []ContentPart{TextPart("Keep the saved map.")}},
		}}
		entry.SetMetadata("app:map", value)
		if err := execCtx.AppendRecord(entry, true); err != nil {
			return nil, err
		}
		if err := execCtx.SetCheckpointState("buffers", value); err != nil {
			return nil, err
		}
		execCtx.PublishCommonEvent("app:values", "Keep every map entry.", value)
		// A real managed call supplies retained evidence as well as history and custom state.
		request := NewModelCallRequest([]*ModelMessage{TextParts(ChatMessageTypeHuman, "Keep it.")},
			WithOptions(&CallOptions{Metadata: map[string]any{"app:map": value}}))
		stream, err := execCtx.GenerateContentStream(&requestOwnershipModel{}, ModelCall{}, request)
		if err != nil {
			return nil, err
		}
		defer stream.Close()
		if _, err := stream.Response(); err != nil {
			return nil, err
		}
		execCtx.Stop()
		return &AgentLoopResult{Action: LAContinue}, nil
	}}
	runner := NewRunner[string](loop, &Config{Codecs: codecs, Recorder: recorder})
	execCtx, err := runner.NewExecutionContext(context.Background(), t.Name())
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	defer execCtx.Release()
	output, result, err := runner.NewTurn(execCtx, NewTask(TextPart("Save the map and pause.")))
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	assert.Equal(t, "", output)
	assert.Equal(t, &ExecutionResult{Status: ExecutionPaused, TurnId: execCtx.TurnId(),
		TerminationReason: TerminationStopped}, result)
	return recorder, execCtx.RunId(), execCtx.TurnId()
}

func applyMapArchive(t *testing.T, view *RunView, codecs *CodecRegistry, batches [][]byte) {
	t.Helper()
	for _, data := range batches {
		var batch SaveBatch
		if !assert.NoError(t, codecs.Unmarshal(data, &batch)) {
			t.FailNow()
		}
		result, err := view.Apply(&batch, codecs)
		if !assert.NoError(t, err) || !assert.Equal(t, &ApplyStateResult{Status: ApplyApplied}, result) {
			t.FailNow()
		}
	}
}

func marshalMapArchive(t *testing.T, codecs *CodecRegistry, view *RunView) []byte {
	t.Helper()
	data, err := codecs.Marshal(view)
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	return data
}

func loadMapArchive(t *testing.T, codecs *CodecRegistry, data []byte) *RunView {
	t.Helper()
	var view RunView
	if !assert.NoError(t, codecs.Unmarshal(data, &view)) {
		t.FailNow()
	}
	assert.NoError(t, ValidateBundle(view, codecs))
	return &view
}

func mapHistoryEntry(t *testing.T, history HistorySnapshot) *HistoryEntry {
	t.Helper()
	for _, entry := range history.Entries {
		if _, found := entry.GetMetadata("app:map"); found {
			return entry
		}
	}
	t.Fatal("saved map history entry is missing")
	return nil
}

func TestSavedMapRecoveryAndArchive(t *testing.T) {
	type input struct {
		equalKeys   bool
		compression CodecCompression
	}
	type expected struct{ entries []savedMapEntry }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"unique plain", input{false, CodecCompressionNone},
			expected{savedMapEntries(false, false)}},
		{"unique compressed", input{false, CodecCompressionSnappy},
			expected{savedMapEntries(false, false)}},
		{"equal plain", input{true, CodecCompressionNone}, expected{savedMapEntries(true, false)}},
		{"equal compressed", input{true, CodecCompressionSnappy},
			expected{savedMapEntries(true, false)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			codecs := NewCodecRegistry(WithCodecCompression(test.input.compression))
			original := savedPointerMap(test.expected.entries)
			recorder, runId, turnId := recordSavedMap(t, codecs, original)
			compressed := false
			for _, data := range recorder.batches {
				var document struct{ FormatVersion int }
				assert.NoError(t, json.Unmarshal(data, &document))
				compressed = compressed || document.FormatVersion == 2
			}
			assert.Equal(t, test.input.compression == CodecCompressionSnappy, compressed)
			view := NewRunView(runId)
			applyMapArchive(t, view, codecs, recorder.batches)
			data := marshalMapArchive(t, codecs, view)
			loaded := loadMapArchive(t, codecs, data)
			assert.Equal(t, data, marshalMapArchive(t, codecs, loaded))
			metadata, found := mapHistoryEntry(t, loaded.History).GetMetadata("app:map")
			assert.True(t, found)
			assert.IsType(t, original, metadata)
			assert.Equal(t, test.expected, expected{inspectSavedMap(t, metadata)})
			verifyMapArchiveOwnership(t, codecs, data, original, test.expected.entries)
			for _, path := range []string{"full batches", "RunStateSnapshot plus tail",
				"latest RunStateSnapshot"} {
				t.Run(path, func(t *testing.T) {
					fresh := NewCodecRegistry(WithCodecCompression(test.input.compression))
					verifySavedMapContinuation(t, fresh, recorder, runId, turnId, path, data,
						test.expected.entries)
				})
			}
		})
	}
}

func verifyMapArchiveOwnership(t *testing.T, codecs *CodecRegistry, data []byte,
	original map[*int]string, want []savedMapEntry,
) {
	t.Helper()
	view := loadMapArchive(t, codecs, data)
	metadata, _ := mapHistoryEntry(t, view.History).GetMetadata("app:map")
	// This deliberate mutation tests ownership, not a supported update to accepted history.
	for key := range metadata.(map[*int]string) {
		*key = -1
		break
	}
	assert.Equal(t, want, inspectSavedMap(t, original))
	found := false
	for _, event := range view.Activity {
		if change := recordedHistoryChange(event); change != nil {
			for _, entry := range change.AppendedEntries {
				if value, ok := entry.GetMetadata("app:map"); ok {
					found = true
					assert.Equal(t, want, inspectSavedMap(t, value))
				}
			}
		}
	}
	assert.True(t, found)
	assert.EqualError(t, ValidateBundle(*view, codecs),
		"gent: history does not match recorded transitions")
	var rejected RunView
	assert.EqualError(t, codecs.Unmarshal(marshalMapArchive(t, codecs, view), &rejected),
		"gent: archive retained projection differs from its replay state")
	assert.Equal(t, RunView{}, rejected)
}

func verifySavedMapContinuation(t *testing.T, codecs *CodecRegistry, recorder *runStateRecorder,
	runId, turnId, path string, archive []byte, want []savedMapEntry,
) {
	t.Helper()
	state := NewRunState(runId)
	start := 0
	if path != "full batches" {
		start = len(recorder.states)
		if path == "RunStateSnapshot plus tail" {
			start = 1
		}
		loaded, err := codecs.UnmarshalRunState(
			encodeRunState(t, recorder.codecs, recorder.states[start-1]))
		if !assert.NoError(t, err) || !assert.Equal(t, RunStateReady, loaded.Status) {
			t.FailNow()
		}
		assert.Equal(t, &RunStateLoadResult{Status: RunStateReady, State: loaded.State}, loaded)
		state = loaded.State
	}
	for _, batch := range recorder.batches[start:] {
		applyRecordedState(t, state, codecs, batch)
	}
	assert.Equal(t, encodeRunState(t, recorder.codecs, recorder.states[len(recorder.states)-1]),
		encodeRunState(t, codecs, state))
	view := loadMapArchive(t, codecs, archive)
	verifyMapReceipts(t, codecs, recorder.batches[len(recorder.batches)-1], state, view)
	loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
		value, found, err := execCtx.CheckpointState("buffers")
		if err != nil {
			return nil, err
		}
		assert.True(t, found)
		assert.Equal(t, want, inspectSavedMap(t, value))
		return acceptTurnText(execCtx, "Kept every map entry.")
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
	assert.Equal(t, "Kept every map entry.", output)
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
	loadMapArchive(t, codecs, marshalMapArchive(t, codecs, view))
}

func verifyMapReceipts(t *testing.T, codecs *CodecRegistry, data []byte,
	state *RunState, view *RunView,
) {
	t.Helper()
	stateBefore, viewBefore := encodeRunState(t, codecs, state), marshalMapArchive(t, codecs, view)
	for _, apply := range []func(*SaveBatch, *CodecRegistry) (*ApplyStateResult, error){
		state.Apply, view.Apply,
	} {
		var batch SaveBatch
		assert.NoError(t, codecs.Unmarshal(data, &batch))
		result, err := apply(&batch, codecs)
		assert.NoError(t, err)
		assert.Equal(t, &ApplyStateResult{Status: ApplyDuplicate}, result)
		// Keep the receipt but change one value. A duplicate receipt must not hide corruption.
		batch.Checkpoint.Contexts[0].Name = "altered"
		result, err = apply(&batch, codecs)
		assert.NoError(t, err)
		assert.Equal(t, &ApplyStateResult{Status: ApplyInvalid,
			Detail: "gent: batch digest mismatch"}, result)
	}
	assert.Equal(t, stateBefore, encodeRunState(t, codecs, state))
	assert.Equal(t, viewBefore, marshalMapArchive(t, codecs, view))
}
