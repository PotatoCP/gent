package gent

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

type allocationSubscriber struct{ values chan any }

func (subscriber *allocationSubscriber) OnEvent(_ context.Context, event *RecordedEvent) error {
	if value, ok := event.Payload.(*CommonEvent); ok && value.EventName == "app:allocation" {
		subscriber.values <- value.Data
	}
	return nil
}

func (*allocationSubscriber) OnStop(context.Context, *SubscriberStop) {}

func TestRecordedEventCloneAllocationBound(t *testing.T) {
	type input struct{ delivery SubscriberDelivery }
	type expected struct {
		delivered []any
		errorText string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	want := expected{[]any{[]int64{7}},
		"gent: record *gent.CommonEvent: gent.CommonEvent.Data: " +
			"gent: decoded allocation of 131073 int64 values (8 bytes each) exceeds 1048576 bytes"}
	cases := []testCase{
		{"accepted", input{SubscriberAccepted}, want},
		{"live", input{SubscriberLive}, want},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := NewCodecRegistry(WithCodecLimits(CodecLimits{MaxExpandedBytes: 1 << 20}))
			recorder := &runStateRecorder{codecs: codecs, capture: true}
			subscriber := &allocationSubscriber{values: make(chan any, 4)}
			value := []int64{7}
			loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				execCtx.PublishCommonEvent("app:allocation", "Keep accepted event data.", value)
				execCtx.Stop()
				return &AgentLoopResult{Action: LAContinue}, nil
			}}
			runner := NewRunner[string](loop, &Config{Codecs: codecs, Recorder: recorder,
				Subscribers: []*SubscriberRegistration{
					{Subscriber: subscriber, Delivery: test.input.delivery}}})
			execCtx, err := runner.NewExecutionContext(context.Background(), t.Name())
			if !assert.NoError(t, err) {
				return
			}
			t.Cleanup(execCtx.Release)
			output, result, err := runner.NewTurn(execCtx, NewTask(TextPart("Keep this task.")))
			if !assert.NoError(t, err) {
				return
			}
			assert.Equal(t, "", output)
			assert.Equal(t, &ExecutionResult{Status: ExecutionPaused, TurnId: execCtx.TurnId(),
				TerminationReason: TerminationStopped}, result)
			accepted := make([][]byte, len(recorder.batches))
			for i, batch := range recorder.batches {
				accepted[i] = bytes.Clone(batch)
			}
			runStateSnapshots := make([][]byte, len(recorder.states))
			for i, state := range recorder.states {
				runStateSnapshots[i] = encodeRunState(t, codecs, state)
			}
			value = make([]int64, (1<<20)/8+1)
			output, result, err = runner.Continue(execCtx)
			if !assert.EqualError(t, err, test.expected.errorText) {
				return
			}
			assert.Equal(t, "", output)
			assert.Nil(t, result)
			assert.Equal(t, accepted, recorder.batches)
			assert.Len(t, recorder.states, len(runStateSnapshots))
			for i, state := range recorder.states {
				assert.Equal(t, runStateSnapshots[i], encodeRunState(t, codecs, state))
			}
			execCtx.Release()
			close(subscriber.values)
			actual := expected{errorText: err.Error()}
			for item := range subscriber.values {
				actual.delivered = append(actual.delivered, item)
			}
			assert.Equal(t, test.expected, actual)
			state, view := NewRunState(execCtx.RunId()), NewRunView(execCtx.RunId())
			for _, data := range recorder.batches {
				applyRecordedState(t, state, codecs, data)
			}
			applyMapArchive(t, view, codecs, recorder.batches)
			assert.Equal(t, runStateSnapshots[len(runStateSnapshots)-1],
				encodeRunState(t, codecs, state))
			fixed := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				execCtx.PublishCommonEvent("app:allocation", "Keep the same allocation bound.", []int64{9})
				return acceptTurnText(execCtx, "Continued the accepted task.")
			}}
			tail := &runStateRecorder{codecs: codecs, capture: true}
			recoveryRunner := NewRunner[string](fixed, &Config{Codecs: codecs, Recorder: tail})
			restored, err := recoveryRunner.Restore(context.Background(), state)
			if !assert.NoError(t, err) || !assert.Equal(t, RestoreReady, restored.Status) {
				return
			}
			defer restored.Context.Release()
			output, result, err = recoveryRunner.Continue(restored.Context)
			assert.NoError(t, err)
			assert.Equal(t, "Continued the accepted task.", output)
			assert.Equal(t, &ExecutionResult{Status: ExecutionFinished, TurnId: execCtx.TurnId(),
				TerminationReason: TerminationSuccess, Output: []ContentPart{TextPart(output)}}, result)
			applyMapArchive(t, view, codecs, tail.batches)
		})
	}
}
