package gent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestHooksIgnoreUnrelatedEvents(t *testing.T) {
	type input struct{ hooks *Hooks }
	type expected struct {
		value     string
		hookCalls int
		payloads  []Event
		err       error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	value := BaseEvent{EventName: "test:custom"}
	hookCalls := 0
	cases := []testCase{
		{"no hooks", input{}, expected{"Done", 0, []Event{value}, nil}},
		{"empty hooks", input{&Hooks{}}, expected{"Done", 0, []Event{value}, nil}},
		{"model hook", input{&Hooks{BeforeModelCall: func(context.Context,
			*BeforeModelCallEvent,
		) error {
			hookCalls++
			return nil
		}}}, expected{"Done", 0, []Event{value}, nil}},
		{"tool hook", input{&Hooks{BeforeToolCall: func(context.Context,
			*BeforeToolCallEvent,
		) error {
			hookCalls++
			return nil
		}}}, expected{"Done", 0, []Event{value}, nil}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := NewCodecRegistry()
			recorder := &runStateRecorder{codecs: codecs}
			pointer := &BaseEvent{EventName: "test:pointer"}
			started := time.Now()
			loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				if err := execCtx.Publish(value); err != nil {
					return nil, err
				}
				if err := execCtx.Publish(pointer); err != nil {
					return nil, err
				}
				return acceptTurnText(execCtx, "Done")
			}}
			runner := NewRunner[string](loop, &Config{Codecs: codecs, Recorder: recorder,
				Hooks: test.input.hooks})
			execCtx, err := runner.NewExecutionContext(context.Background(), test.name)
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			value, result, err := runner.NewTurn(execCtx, NewTask(TextPart("Work")))
			var payloads []Event
			for _, event := range savedTestEvents(t, recorder) {
				if event.Type == "custom" {
					payloads = append(payloads, event.Payload.(Event))
				}
			}
			want := test.expected
			savedPointer := *pointer
			// Portable timestamps keep wall time; the process-local monotonic reading is not saved.
			savedPointer.Timestamp = pointer.Timestamp.Round(0)
			want.payloads = append(want.payloads, &savedPointer)
			assert.Equal(t, want, expected{value, hookCalls, payloads, err})
			assert.GreaterOrEqual(t, pointer.Timestamp, started)
			assert.Equal(t, &ExecutionResult{Status: ExecutionFinished, TurnId: execCtx.TurnId(),
				TerminationReason: TerminationSuccess, Output: []ContentPart{TextPart("Done")}}, result)
		})
	}
}
