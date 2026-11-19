package gent

import (
	"context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestOwnedHistoryAndSelectionChanges(t *testing.T) {
	task := NewTask(TextPart("original task"))
	codecs := NewCodecRegistry()
	recorder := &runStateRecorder{codecs: codecs, capture: true}
	var policy *HistoryEntry
	var first ContextDraft
	loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
		if execCtx.Iteration() == 1 {
			task.Messages[0].Parts[0] = TextPart("caller changed task")
			owned, err := execCtx.Task()
			if err != nil {
				return nil, err
			}
			assert.Equal(t, NewTask(TextPart("original task")), owned)
			policy, err = execCtx.NewHistoryEntry(&HistoryEntry{Origin: RecordOriginal,
				ExpireAfterIteration: 3, Messages: []*MessageContent{{Role: ChatMessageTypeHuman,
					Parts: []ContentPart{TextPart("original policy")}}}})
			if err != nil {
				return nil, err
			}
			draft := execCtx.ContextDraft()
			if err := execCtx.UpdateHistory(HistoryUpdate{ExpectedDraftRevision: draft.Revision,
				AppendedEntries: []*HistoryEntry{policy}, Active: append(draft.Active, policy.Id),
				Reason: ContextAppend}); err != nil {
				return nil, err
			}
			first = execCtx.ContextDraft()
		}
		if execCtx.Iteration() < 3 {
			return &AgentLoopResult{Action: LAContinue}, nil
		}
		records, err := execCtx.Scratchpad()
		if err != nil {
			return nil, err
		}
		require.Len(t, records, 1)
		assert.Equal(t, RecordUserInput, records[0].Origin)
		assert.ErrorIs(t, execCtx.UpdateHistory(HistoryUpdate{ExpectedDraftRevision: first.Revision}),
			ErrHistoryConflict)
		return acceptTurnText(execCtx, "Done")
	}}
	runner := NewRunner[string](loop, &Config{Codecs: codecs, Recorder: recorder})
	execCtx, err := runner.NewExecutionContext(context.Background(), "history")
	require.NoError(t, err)
	defer execCtx.Release()
	_, _, err = runner.NewTurn(execCtx, task)
	require.NoError(t, err)
	view := NewRunView(execCtx.RunId())
	for _, data := range recorder.batches {
		var batch SaveBatch
		require.NoError(t, codecs.Unmarshal(data, &batch))
		applied, err := view.Apply(&batch, codecs)
		require.NoError(t, err)
		require.Equal(t, ApplyApplied, applied.Status)
	}
	require.NoError(t, ValidateBundle(*view, codecs))
	archived, found := view.History.Entry(policy.Id)
	require.True(t, found)
	assert.Equal(t, policy, archived)
	active, err := view.History.ContextAt(execCtx.ContextId(), HistoryRevision(first.Revision))
	assert.NoError(t, err)
	assert.Equal(t, first.Active, active)
	for _, id := range execCtx.ContextDraft().Active {
		assert.NotEqual(t, policy.Id, id)
	}
	saved := recorder.states[len(recorder.states)-1]
	restored, err := runner.Restore(context.Background(), saved)
	require.NoError(t, err)
	require.Equal(t, RestoreReady, restored.Status)
	defer restored.Context.Release()
	assert.Equal(t, execCtx.ContextDraft(), restored.Context.ContextDraft())
	assert.Equal(t, execCtx.History(), restored.Context.History())
	value, result, err := runner.GetLastTurnResult(restored.Context)
	assert.NoError(t, err)
	assert.Equal(t, "Done", value)
	assert.Equal(t, ExecutionFinished, result.Status)
}

type gateRecorder struct {
	*runStateRecorder
	entered chan struct{}
	release chan struct{}
	blocked bool
}

func (recorder *gateRecorder) Save(ctx context.Context, batch *SaveBatch,
	capture func() (*RunState, error),
) error {
	if !recorder.blocked {
		recorder.blocked = true
		close(recorder.entered)
		select {
		case <-recorder.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return recorder.runStateRecorder.Save(ctx, batch, capture)
}

type channelSubscriber struct{ events chan *RecordedEvent }

func (subscriber *channelSubscriber) OnEvent(_ context.Context, event *RecordedEvent) error {
	subscriber.events <- event
	return nil
}
func (*channelSubscriber) OnStop(context.Context, *SubscriberStop) {}

func TestRunCommitVisibility(t *testing.T) {
	codecs := NewCodecRegistry()
	recorder := &gateRecorder{runStateRecorder: &runStateRecorder{codecs: codecs},
		entered: make(chan struct{}), release: make(chan struct{})}
	subscriber := &channelSubscriber{events: make(chan *RecordedEvent, 100)}
	loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
		return acceptTurnText(execCtx, "Done")
	}}
	runner := NewRunner[string](loop, &Config{Codecs: codecs, Recorder: recorder,
		Subscribers: []*SubscriberRegistration{{Subscriber: subscriber}}})
	execCtx, err := runner.NewExecutionContext(context.Background(), "blocked-save")
	require.NoError(t, err)
	defer execCtx.Release()
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		_, _, err := runner.NewTurn(execCtx, NewTask(TextPart("Start.")))
		done <- err
	}()
	<-recorder.entered
	assert.Equal(t, HistorySnapshot{}, execCtx.History())
	_, last, err := runner.GetLastTurnResult(execCtx)
	assert.NoError(t, err)
	assert.Nil(t, last)
	select {
	case event := <-subscriber.events:
		t.Errorf("unaccepted event reached Subscriber: %v", event)
	default:
	}
	close(recorder.release)
	require.NoError(t, <-done)
	want := savedTestEvents(t, recorder.runStateRecorder)
	var received []*RecordedEvent
	for len(subscriber.events) > 0 {
		received = append(received, <-subscriber.events)
	}
	assert.Equal(t, want, received)
	for _, event := range received {
		assert.GreaterOrEqual(t, event.Ts.UnixNano(), start.UnixNano())
	}
}

func TestRunViewRejectsInvalidDeltas(t *testing.T) {
	type input struct {
		runId            string
		previous, number uint64
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
	cases := []testCase{
		{"wrong run", input{"other", 0, 1}, expected{ApplyStateResult{
			Status: ApplyInvalid, Detail: `gent: expected run "fixture", received "other"`}, nil}},
		{"missing predecessor", input{"fixture", 1, 2}, expected{ApplyStateResult{
			Status:                              ApplyCursorConflict,
			Detail:                              "gent: batch does not follow the accepted event sequence",
			ReceivedPreviousEventSequenceNumber: 1}, nil}},
		{"number gap", input{"fixture", 0, 2}, expected{ApplyStateResult{
			Status: ApplyInvalid, Detail: "gent: nonconsecutive event at batch position 0"}, nil}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := NewCodecRegistry()
			view := NewRunView("fixture")
			before, err := codecs.Marshal(view)
			assert.NoError(t, err)
			batch := &SaveBatch{SchemaVersion: RunSchemaVersion, RunId: test.input.runId,
				PreviousEventSequenceNumber: test.input.previous, Events: []*RecordedEvent{{
					RunId: test.input.runId, EventSequenceNumber: test.input.number,
					Type: "progress", Ts: time.Now()}}}
			batch.BatchId, err = BatchDigest(batch, codecs)
			assert.NoError(t, err)
			result, err := view.Apply(batch, codecs)
			assert.Equal(t, test.expected.err, err)
			assert.Equal(t, &test.expected.result, result)
			after, err := codecs.Marshal(view)
			assert.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}
