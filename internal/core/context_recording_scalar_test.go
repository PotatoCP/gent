package gent

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

type scalarEventSubscriber struct{ values chan any }

func (subscriber *scalarEventSubscriber) OnEvent(_ context.Context, event *RecordedEvent) error {
	if value, ok := event.Payload.(*CommonEvent); ok && value.EventName == "app:scalar" {
		subscriber.values <- value.Data
	}
	return nil
}

func (*scalarEventSubscriber) OnStop(context.Context, *SubscriberStop) {}

func TestScalarEventMatchesAcceptedArchive(t *testing.T) {
	type input struct {
		delivery SubscriberDelivery
		failure  cloneJSONScalar
	}
	type expected struct{ errorText string }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	var cases []testCase
	for _, delivery := range []SubscriberDelivery{SubscriberAccepted, SubscriberLive} {
		cases = append(cases,
			testCase{string(delivery) + "/value", input{delivery: delivery}, expected{}},
			testCase{string(delivery) + "/encode error", input{delivery, "encode error"}, expected{
				"gent: record *gent.CommonEvent: gent.CommonEvent.Data: " +
					"json: error calling MarshalJSON for type gent.cloneJSONScalar: " +
					"scalar validation failed"}},
			testCase{string(delivery) + "/decode error", input{delivery, "decode error"}, expected{
				"gent: record *gent.CommonEvent: github.com/rickchristie/gent/CommonEvent.Data: " +
					"scalar validation failed"}},
		)
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := NewCodecRegistry()
			assert.NoError(t, codecs.RegisterValueType(reflect.TypeFor[cloneJSONScalar]()))
			recorder := &runStateRecorder{codecs: codecs, capture: true}
			subscriber := &scalarEventSubscriber{values: make(chan any, 4)}
			value := cloneJSONScalar("HELLO")
			loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				execCtx.PublishCommonEvent("app:scalar", "Save the logical scalar value.", value)
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
			runStateSnapshot := encodeRunState(t, codecs, recorder.states[len(recorder.states)-1])
			if test.expected.errorText != "" {
				value = test.input.failure
				output, result, err = runner.Continue(execCtx)
				assert.EqualError(t, err, test.expected.errorText)
				assert.ErrorIs(t, err, errCloneScalar)
				assert.Equal(t, "", output)
				assert.Nil(t, result)
				assert.Equal(t, accepted, recorder.batches)
				assert.Equal(t, runStateSnapshot,
					encodeRunState(t, codecs, recorder.states[len(recorder.states)-1]))
			}
			execCtx.Release()
			close(subscriber.values)
			var delivered []any
			for item := range subscriber.values {
				delivered = append(delivered, item)
			}
			want := []any{cloneJSONScalar("hello")}
			assert.Equal(t, want, delivered)
			state, view := NewRunState(execCtx.RunId()), NewRunView(execCtx.RunId())
			for _, data := range recorder.batches {
				applyRecordedState(t, state, codecs, data)
			}
			applyMapArchive(t, view, codecs, recorder.batches)
			var archived []any
			for _, event := range view.Activity {
				if item, ok := event.Payload.(*CommonEvent); ok && item.EventName == "app:scalar" {
					archived = append(archived, item.Data)
				}
			}
			assert.Equal(t, want, archived)
			assert.Equal(t, runStateSnapshot, encodeRunState(t, codecs, state))
			fixed := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				execCtx.PublishCommonEvent("app:scalar", "Save the corrected value.", cloneJSONScalar("FIXED"))
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
