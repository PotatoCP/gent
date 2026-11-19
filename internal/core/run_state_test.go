package gent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

type runStateRecorder struct {
	codecs    *CodecRegistry
	batches   [][]byte
	capture   bool
	states    []*RunState
	callbacks []func() (*RunState, error)
}

func (recorder *runStateRecorder) Save(
	_ context.Context, batch *SaveBatch,
	captureRunStateSnapshot func() (*RunState, error),
) error {
	data, err := recorder.codecs.Marshal(batch)
	if err != nil {
		return err
	}
	if recorder.capture {
		state, err := captureRunStateSnapshot()
		if err != nil {
			return err
		}
		recorder.states = append(recorder.states, state)
		recorder.callbacks = append(recorder.callbacks, captureRunStateSnapshot)
	}
	recorder.batches = append(recorder.batches, data)
	return nil
}

func recordedStateFixture(t *testing.T, count int, codecs *CodecRegistry, metadata any) (
	string, [][]byte,
) {
	t.Helper()
	recorder := &runStateRecorder{codecs: codecs}
	var previous EntryId
	loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
		entry := &HistoryEntry{Messages: []*MessageContent{
			{Role: ChatMessageTypeHuman,
				Parts: []ContentPart{TextPart(fmt.Sprintf("Plan %06d", execCtx.Iteration()))}},
		}}
		if metadata != nil {
			entry.SetMetadata("test:state", metadata)
		}
		if previous != "" {
			entry.Replaces = []EntryId{previous}
			entry.Origin = RecordCompactedModified
		}
		owned, err := execCtx.NewHistoryEntry(entry)
		if err != nil {
			return nil, err
		}
		draft := execCtx.ContextDraft()
		if err := execCtx.UpdateHistory(HistoryUpdate{
			ExpectedDraftRevision: draft.Revision, AppendedEntries: []*HistoryEntry{owned},
			Active: []EntryId{owned.Id}, Reason: ContextReplace,
		}); err != nil {
			return nil, err
		}
		previous = owned.Id
		return &AgentLoopResult{Action: LAContinue}, nil
	}}
	runner := NewRunner[string](loop, &Config{Recorder: recorder, Codecs: codecs,
		Limits: []Limit{{Type: LimitExactKey, Key: SCIterations, MaxValue: float64(count)}}})
	execCtx, err := runner.NewExecutionContext(context.Background(), "retained-state")
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	t.Cleanup(execCtx.Release)
	_, result, err := runner.NewTurn(execCtx, NewTask(TextPart("Review the changing plan.")))
	if !assert.NoError(t, err) || !assert.Equal(t, ExecutionPaused, result.Status) {
		t.FailNow()
	}
	return execCtx.RunId(), recorder.batches
}

func applyRecordedState(t *testing.T, state *RunState, codecs *CodecRegistry, data []byte) {
	t.Helper()
	var batch SaveBatch
	if !assert.NoError(t, codecs.Unmarshal(data, &batch)) {
		t.FailNow()
	}
	result, err := state.Apply(&batch, codecs)
	if !assert.NoError(t, err) || !assert.Equal(t, &ApplyStateResult{Status: ApplyApplied}, result) {
		t.FailNow()
	}
}

func encodeRunState(t *testing.T, codecs *CodecRegistry, state *RunState) []byte {
	t.Helper()
	data, err := codecs.MarshalRunState(state)
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	return data
}

func TestRunStateSnapshotAndTailMatchFullReplay(t *testing.T) {
	type input struct{ captureAfter int }
	type expected struct{ tailBatches int }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	codecs := NewCodecRegistry()
	runId, batches := recordedStateFixture(t, 12, codecs, nil)
	full := NewRunState(runId)
	for _, batch := range batches {
		applyRecordedState(t, full, codecs, batch)
	}
	want := encodeRunState(t, codecs, full)
	for _, test := range []testCase{
		{"empty replay target", input{0}, expected{len(batches)}},
		{"first accepted batch", input{1}, expected{len(batches) - 1}},
		{"middle savepoint", input{7}, expected{len(batches) - 7}},
		{"accepted head", input{len(batches)}, expected{0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := NewRunState(runId)
			for _, batch := range batches[:test.input.captureAfter] {
				applyRecordedState(t, state, codecs, batch)
			}
			captured := encodeRunState(t, codecs, state)
			loaded, err := codecs.UnmarshalRunState(captured)
			if !assert.NoError(t, err) || !assert.Equal(t, RunStateReady, loaded.Status) {
				return
			}
			assert.Equal(t, "", loaded.Detail)
			assert.Equal(t, runId, loaded.State.RunId())
			assert.Equal(t, state.LastAppliedEventSequenceNumber(),
				loaded.State.LastAppliedEventSequenceNumber())
			tail := batches[test.input.captureAfter:]
			assert.Len(t, tail, test.expected.tailBatches)
			for _, batch := range tail {
				applyRecordedState(t, loaded.State, codecs, batch)
			}
			assert.Equal(t, want, encodeRunState(t, codecs, loaded.State))
			// A loaded instance and later tail replay cannot change the earlier captured value.
			assert.Equal(t, captured, encodeRunState(t, codecs, state))
		})
	}
}

func TestRunStateRetainsContextWithoutArchiveGrowth(t *testing.T) {
	codecs := NewCodecRegistry()
	runId, batches := recordedStateFixture(t, 120, codecs, nil)
	state := NewRunState(runId)
	var earlierSize int
	for i, batch := range batches[:len(batches)-2] {
		applyRecordedState(t, state, codecs, batch)
		if i == 40 {
			earlierSize = len(encodeRunState(t, codecs, state))
		}
	}
	encoded := encodeRunState(t, codecs, state)
	// Later cursors and allocation values need a few extra digits. Old source references must
	// not retain the full replacement chain merely to keep those references valid.
	assert.LessOrEqual(t, len(encoded)-earlierSize, 128)
	loaded, err := codecs.UnmarshalRunState(encoded)
	assert.NoError(t, err)
	assert.Equal(t, RunStateReady, loaded.Status)
	assert.Equal(t, encoded, encodeRunState(t, codecs, loaded.State))
}

func TestRecorderRunStateSnapshotMatchesItsBatch(t *testing.T) {
	codecs := NewCodecRegistry()
	recorder := &runStateRecorder{codecs: codecs, capture: true}
	loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
		if err := execCtx.AppendRecord(&HistoryEntry{Messages: []*MessageContent{
			{Role: ChatMessageTypeHuman,
				Parts: []ContentPart{TextPart(fmt.Sprintf("Input %d", execCtx.Iteration()))}},
		}}, true); err != nil {
			return nil, err
		}
		if execCtx.Iteration() == 4 {
			return acceptTurnText(execCtx, "Done")
		}
		return &AgentLoopResult{Action: LAContinue}, nil
	}}
	runner := NewRunner[string](loop, &Config{Recorder: recorder, Codecs: codecs})
	execCtx, err := runner.NewExecutionContext(context.Background(), "capture")
	if !assert.NoError(t, err) {
		return
	}
	t.Cleanup(execCtx.Release)
	_, _, err = runner.NewTurn(execCtx, NewTask(TextPart("Remember accepted context.")))
	if !assert.NoError(t, err) {
		return
	}
	// Encode only after later acceptance has happened. Each captured value must still represent
	// its own fixed savepoint, not the worker's latest mutable fields or accepted head.
	state := NewRunState(execCtx.RunId())
	assert.Len(t, recorder.states, len(recorder.batches))
	for i, batch := range recorder.batches {
		applyRecordedState(t, state, codecs, batch)
		assert.Equal(t, encodeRunState(t, codecs, state), encodeRunState(t, codecs, recorder.states[i]))
		result, err := recorder.callbacks[i]()
		assert.Nil(t, result)
		if assert.Error(t, err) {
			assert.Equal(t, "gent: capture RunStateSnapshot only during Recorder.Save", err.Error())
		}
	}
}

func TestRunViewReconstructsFullArchiveAndOldReceipts(t *testing.T) {
	codecs := NewCodecRegistry()
	runId, batches := recordedStateFixture(t, 8, codecs, nil)
	view := NewRunView(runId)
	var events []*RecordedEvent
	var entries []*HistoryEntry
	var changes []HistoryChange
	var checkpoint *Checkpoint
	var runtime RuntimeState
	receipts := map[string]string{}
	for _, data := range batches {
		var batch SaveBatch
		assert.NoError(t, codecs.Unmarshal(data, &batch))
		result, err := view.Apply(&batch, codecs)
		assert.NoError(t, err)
		assert.Equal(t, &ApplyStateResult{Status: ApplyApplied}, result)
		events = append(events, batch.Events...)
		for _, event := range batch.Events {
			if recordedHistoryChange(event) != nil {
				entries = append(entries, recordedHistoryChange(event).AppendedEntries...)
				changes = append(changes, *recordedHistoryChange(event))
			}
		}
		checkpoint, runtime = batch.Checkpoint, batch.Runtime
		receipts[batch.BatchId] = batch.BatchId
		encoded, err := codecs.Marshal(view)
		assert.NoError(t, err)
		var restored RunView
		assert.NoError(t, codecs.Unmarshal(encoded, &restored))
		view = &restored
	}
	assert.Equal(t, HistorySnapshot{Revision: checkpoint.Revision,
		Entries: entries, Changes: changes}, view.History)
	assert.Equal(t, events, view.Activity)
	assert.Equal(t, checkpoint, view.Checkpoint)
	assert.Equal(t, runtime, view.Runtime)
	assert.Equal(t, receipts, view.Batches)
	assert.Equal(t, runId, view.RunId)
	assert.Equal(t, RunSchemaVersion, view.SchemaVersion)
	assert.Equal(t, events[len(events)-1].EventSequenceNumber, view.LastAppliedEventSequenceNumber)
	before, err := codecs.Marshal(view)
	assert.NoError(t, err)
	var first SaveBatch
	assert.NoError(t, codecs.Unmarshal(batches[0], &first))
	result, err := view.Apply(&first, codecs)
	assert.NoError(t, err)
	assert.Equal(t, &ApplyStateResult{Status: ApplyDuplicate}, result)
	after, err := codecs.Marshal(view)
	assert.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestRunStateRejectsBatchesWithoutMutation(t *testing.T) {
	type input struct {
		targetId string
		prefix   int
		batch    int
		change   func(*SaveBatch)
	}
	type expected struct {
		result ApplyStateResult
		err    error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	codecs := NewCodecRegistry()
	runId, batches := recordedStateFixture(t, 3, codecs, nil)
	var first SaveBatch
	assert.NoError(t, codecs.Unmarshal(batches[0], &first))
	firstCursor := first.Events[len(first.Events)-1].EventSequenceNumber
	tests := []testCase{

		{"empty target", input{"", 0, 0, nil}, expected{ApplyStateResult{
			Status: ApplyInvalid, Detail: "gent: replay target run ID is empty"}, nil}},
		{
			"wrong run",
			input{"different", 0, 0, nil},
			expected{
				ApplyStateResult{

					Status: ApplyInvalid,
					Detail: fmt.Sprintf(
						"gent: expected run %q, received %q",
						"different",
						runId,
					),
				},
				nil,
			},
		},
		{"empty batch run", input{runId, 0, 0, func(batch *SaveBatch) { batch.RunId = "" }},
			expected{ApplyStateResult{Status: ApplyInvalid, Detail: "gent: batch run ID is empty"}, nil}},
		{
			"both run IDs empty",
			input{"", 0, 0, func(batch *SaveBatch) { batch.RunId = "" }},
			expected{
				ApplyStateResult{
					Status: ApplyInvalid,
					Detail: "gent: replay target run ID is empty",
				},
				nil,
			},
		},
		{"wrong digest", input{runId, 1, 1, func(batch *SaveBatch) { batch.BatchId = "altered" }},
			expected{ApplyStateResult{Status: ApplyInvalid, Detail: "gent: batch digest mismatch"}, nil}},
		{"newer schema", input{runId, 1, 1, func(batch *SaveBatch) { batch.SchemaVersion++ }},
			expected{ApplyStateResult{Status: ApplyIncompatible,
				Detail: fmt.Sprintf("gent: expected run schema %d; state has %d, batch has %d",
					RunSchemaVersion, RunSchemaVersion, RunSchemaVersion+1)}, nil}},
		{"gap", input{runId, 0, 1, nil}, expected{ApplyStateResult{
			Status: ApplyCursorConflict, Detail: "gent: batch does not follow the accepted event sequence",
			ReceivedPreviousEventSequenceNumber: firstCursor}, nil}},
		{"known duplicate", input{runId, 1, 0, nil}, expected{ApplyStateResult{
			Status: ApplyDuplicate}, nil}},
		{"older unknown receipt", input{runId, 3, 0, nil}, expected{ApplyStateResult{
			Status: ApplyReceiptRequired,
			Detail: "gent: prove this older batch's exact accepted receipt in storage"}, nil}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := NewRunState(test.input.targetId)
			for _, data := range batches[:test.input.prefix] {
				applyRecordedState(t, state, codecs, data)
			}
			var batch SaveBatch
			assert.NoError(t, codecs.Unmarshal(batches[test.input.batch], &batch))
			if test.input.change != nil {
				test.input.change(&batch)
			}
			batchBefore, err := codecs.Marshal(&batch)
			assert.NoError(t, err)
			cursorBefore := state.LastAppliedEventSequenceNumber()
			var stateBefore []byte
			if state.RunId() != "" {
				stateBefore = encodeRunState(t, codecs, state)
			}
			result, err := state.Apply(&batch, codecs)
			assert.Equal(t, test.expected.err, err)
			assert.Equal(t, &test.expected.result, result)
			assert.Equal(t, cursorBefore, state.LastAppliedEventSequenceNumber())
			assert.Equal(t, test.input.targetId, state.RunId())
			if stateBefore != nil {
				assert.Equal(t, stateBefore, encodeRunState(t, codecs, state))
			}
			batchAfter, err := codecs.Marshal(&batch)
			assert.NoError(t, err)
			assert.Equal(t, batchBefore, batchAfter)
		})
	}
}

type runStateCustomValue struct{ Label string }

type runStateCustomCodec struct{ failure error }

func (runStateCustomCodec) Type() string {
	return "test/run-state-value"
}
func (runStateCustomCodec) Version() int                              { return 1 }
func (runStateCustomCodec) Encode(value any) (json.RawMessage, error) { return json.Marshal(value) }
func (codec runStateCustomCodec) Decode(data json.RawMessage) (any, error) {
	if codec.failure != nil {
		return nil, codec.failure
	}
	var value runStateCustomValue
	err := json.Unmarshal(data, &value)
	return value, err
}

func TestRunStateLoadSeparatesMissingCodecAndOperationFailure(t *testing.T) {
	type input struct {
		register bool
		failure  error
	}
	type expected struct {
		result  *RunStateLoadResult
		cause   error
		message string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	codecs := NewCodecRegistry()
	assert.NoError(t, codecs.Register(runStateCustomValue{}, runStateCustomCodec{}))
	runId, batches := recordedStateFixture(t, 1, codecs, runStateCustomValue{Label: "saved"})
	state := NewRunState(runId)
	for _, batch := range batches {
		applyRecordedState(t, state, codecs, batch)
	}
	data := encodeRunState(t, codecs, state)
	path := []string{
		"github.com/rickchristie/gent/runStateData.History",
		"github.com/rickchristie/gent/HistorySnapshot.Entries",
		"github.com/rickchristie/gent/HistoryEntry.Metadata",
	}
	missing := strings.Join(append(path, `gent: unknown codec "test/run-state-value"`), ": ")
	failure := errors.New("decode service failed")
	failurePath := append([]string{"gent: decode RunStateSnapshot"}, path...)
	failed := strings.Join(append(failurePath,
		`gent: decode codec "test/run-state-value"`, "decode service failed"), ": ")
	tests := []testCase{
		{"ready", input{true, nil}, expected{
			result: &RunStateLoadResult{Status: RunStateReady, State: state}}},
		{"missing codec", input{}, expected{
			result: &RunStateLoadResult{Status: RunStateIncompatible, Detail: missing}}},
		{"custom operation failed", input{true, failure}, expected{cause: failure, message: failed}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := NewCodecRegistry()
			if test.input.register {
				assert.NoError(t, registry.Register(runStateCustomValue{},
					runStateCustomCodec{failure: test.input.failure}))
			}
			result, err := registry.UnmarshalRunState(data)
			assert.Equal(t, test.expected.result, result)
			if test.expected.cause == nil {
				assert.NoError(t, err)
				return
			}
			if assert.ErrorIs(t, err, test.expected.cause) {
				assert.Equal(t, test.expected.message, err.Error())
			}
		})
	}
}
