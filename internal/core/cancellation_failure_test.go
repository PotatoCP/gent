package gent

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestStopPreservesJoinedExecutionFailure(t *testing.T) {
	type input struct {
		failure func(context.Context, error) error
	}
	type expected struct {
		failed bool
		phases []ExecutionPhase
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"canceled", input{func(ctx context.Context, _ error) error { return ctx.Err() }},
			expected{false, []ExecutionPhase{PhaseInitial, PhaseIterationReady, PhasePaused}}},
		{"wrapped cause", input{func(ctx context.Context, _ error) error {
			return fmt.Errorf("operation stopped: %w", context.Cause(ctx))
		}}, expected{false, []ExecutionPhase{PhaseInitial, PhaseIterationReady, PhasePaused}}},
		{"joined cancellation only", input{func(ctx context.Context, _ error) error {
			return errors.Join(ctx.Err(), context.Cause(ctx))
		}}, expected{false, []ExecutionPhase{PhaseInitial, PhaseIterationReady, PhasePaused}}},
		{"joined failure", input{func(ctx context.Context, failure error) error {
			return errors.Join(ctx.Err(), failure)
		}}, expected{true, []ExecutionPhase{PhaseInitial, PhaseIterationReady}}},
		{"nested joined failure", input{func(ctx context.Context, failure error) error {
			return fmt.Errorf("operation ended: %w", errors.Join(context.Cause(ctx),
				fmt.Errorf("cleanup: %w", errors.Join(ctx.Err(), failure))))
		}}, expected{true, []ExecutionPhase{PhaseInitial, PhaseIterationReady}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			failure := errors.New("application invariant failed")
			codecs := NewCodecRegistry()
			store := &runStateRecorder{codecs: codecs, capture: true}
			var returned error
			loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				execCtx.Stop()
				returned = test.input.failure(execCtx.Context(), failure)
				return nil, returned
			}}
			runner := NewRunner[string](loop, &Config{Codecs: codecs, Recorder: store})
			execCtx, err := runner.NewExecutionContext(context.Background(), test.name)
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			value, result, err := runner.NewTurn(execCtx, NewTask(TextPart("Keep this task.")))
			assert.Equal(t, "", value)
			if test.expected.failed {
				assert.Nil(t, result)
				assert.ErrorIs(t, err, failure)
				assert.EqualError(t, err, fmt.Sprintf(
					"gent: Loop.Next %T failed in run %q turn %q iteration %d: %s",
					loop, execCtx.RunId(), execCtx.TurnId(), execCtx.Iteration(), returned))
			} else {
				assert.NoError(t, err)
				assert.Equal(t, &ExecutionResult{Status: ExecutionPaused, TurnId: execCtx.TurnId(),
					TerminationReason: TerminationStopped}, result)
			}
			var phases []ExecutionPhase
			state := NewRunState(execCtx.RunId())
			for _, data := range store.batches {
				var batch SaveBatch
				if !assert.NoError(t, codecs.Unmarshal(data, &batch)) {
					return
				}
				phases = append(phases, batch.Checkpoint.Phase)
				applyRecordedState(t, state, codecs, data)
			}
			assert.Equal(t, test.expected.phases, phases)
			assert.Equal(t, encodeRunState(t, codecs, state),
				encodeRunState(t, codecs, store.states[len(store.states)-1]))
			fixed := NewRunner[string](&turnTestLoop{next: func(execCtx *ExecutionContext) (
				*AgentLoopResult, error,
			) {
				return acceptTurnText(execCtx, "Recovered")
			}}, &Config{Codecs: codecs})
			restored, err := fixed.Restore(context.Background(), state)
			if !assert.NoError(t, err) || !assert.Equal(t, RestoreReady, restored.Status) {
				return
			}
			defer restored.Context.Release()
			value, result, err = fixed.Continue(restored.Context)
			assert.NoError(t, err)
			assert.Equal(t, "Recovered", value)
			assert.Equal(t, &ExecutionResult{Status: ExecutionFinished, TurnId: execCtx.TurnId(),
				TerminationReason: TerminationSuccess,
				Output:            []ContentPart{TextPart("Recovered")}}, result)
		})
	}
}

func TestSubscriberTimeoutPreservesJoinedCallbackFailure(t *testing.T) {
	type input struct {
		failure func(context.Context, error) error
	}
	type expected struct{ failed bool }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"cancellation only", input{func(ctx context.Context, _ error) error {
			return fmt.Errorf("write stopped: %w", ctx.Err())
		}}, expected{}},
		{"joined failure", input{func(ctx context.Context, failure error) error {
			return errors.Join(ctx.Err(), failure)
		}}, expected{true}},
		{"nested joined failure", input{func(ctx context.Context, failure error) error {
			return fmt.Errorf("write: %w", errors.Join(ctx.Err(),
				fmt.Errorf("flush: %w", failure)))
		}}, expected{true}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			failure := errors.New("socket write failed after sending data")
			var returned error
			var stop SubscriberStop
			var stops int
			var acceptedAtStop uint64
			loopReady := make(chan struct{})
			subscriberStopped := make(chan struct{})
			codecs := NewCodecRegistry()
			store := &runStateRecorder{codecs: codecs, capture: true}
			subscriber := &subscriberCallbacks{
				event: func(ctx context.Context, _ *RecordedEvent) error {
					<-loopReady
					<-ctx.Done()
					returned = test.input.failure(ctx, failure)
					return returned
				},
				stop: func(_ context.Context, result *SubscriberStop) {
					stop = *result
					stops++
					close(subscriberStopped)
				},
			}
			loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				// Hold the accepted head until OnStop reports the stop. The turn can then advance, but
				// the stop cursor must still identify the earlier accepted position.
				acceptedAtStop = store.states[len(store.states)-1].LastAppliedEventSequenceNumber()
				close(loopReady)
				<-subscriberStopped
				return acceptTurnText(execCtx, "Done")
			}}
			runner := NewRunner[string](loop, &Config{Codecs: codecs, Recorder: store,
				Subscribers: []*SubscriberRegistration{{Subscriber: subscriber,
					Buffer: &DeliveryConfig{Timeout: 30 * time.Millisecond}}}})
			execCtx, err := runner.NewExecutionContext(context.Background(), test.name)
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			value, result, err := runner.NewTurn(execCtx, NewTask(TextPart("Answer.")))
			assert.NoError(t, err)
			assert.Equal(t, "Done", value)
			assert.Equal(t, &ExecutionResult{Status: ExecutionFinished, TurnId: execCtx.TurnId(),
				TerminationReason: TerminationSuccess, Output: []ContentPart{TextPart("Done")}}, result)
			var callbackError error
			if test.expected.failed {
				callbackError = errors.Join(returned)
				assert.ErrorIs(t, stop.Error, failure)
			}
			assert.Equal(t, SubscriberStop{RunId: execCtx.RunId(), AttemptId: execCtx.AttemptId(),
				Reason: SubscriberTimeout, Error: callbackError,
				LastAcceptedEventSequenceNumber: acceptedAtStop}, stop)
			assert.Greater(t, store.states[len(store.states)-1].LastAppliedEventSequenceNumber(),
				stop.LastAcceptedEventSequenceNumber)
			assert.Equal(t, 1, stops)
		})
	}
}
