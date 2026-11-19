package gent_test

import (
	"context"
	"strings"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

type capacitySubscriber struct {
	holding  chan struct{}
	stopped  chan *gent.SubscriberStop
	observer contentObserver
}

func (subscriber *capacitySubscriber) OnEvent(
	ctx context.Context,
	event *gent.RecordedEvent,
) error {
	if event.EventName == "hold" {
		close(subscriber.holding)
		<-ctx.Done()
		return ctx.Err()
	}
	return subscriber.observer.OnEvent(ctx, event)
}
func (subscriber *capacitySubscriber) OnStop(_ context.Context, result *gent.SubscriberStop) {
	subscriber.stopped <- result
}

func TestCheckpointSubscriberCapacityIncludesActiveCallback(t *testing.T) {
	type input struct{ capacity int }
	type expected struct {
		reason gent.SubscriberStopReason
		answer string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"sixteen slots", input{16}, expected{gent.SubscriberOverflow, "Done."}},
		{"thirty two slots", input{32}, expected{gent.SubscriberOverflow, "Done."}},
	} {
		t.Run(
			test.name,
			func(t *testing.T) {
				subscriber := &capacitySubscriber{
					holding: make(
						chan struct{},
					),
					stopped: make(
						chan *gent.SubscriberStop,
						1,
					),
				}
				loop := contentLoop(func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
					execCtx.PublishCommonEvent("hold", "Keep one callback active.", nil)
					checkpointSignal(t, subscriber.holding)
					// The active callback uses one slot. The last event must overflow this queue.
					for range test.input.capacity {
						execCtx.PublishCommonEvent("queued", "Progress", nil)
					}
					return acceptRootOutput(execCtx, "Done.")
				})
				store := newCheckpointStore()
				runner, execCtx := newTestExecution(
					t,
					context.Background(),
					loop,
					&gent.Config{

						Codecs:   store.codecs,
						Recorder: store,
						Subscribers: []*gent.SubscriberRegistration{
							{
								Subscriber: subscriber,
								Delivery:   gent.SubscriberLive,
								Buffer: &gent.DeliveryConfig{
									MaxEvents: test.input.capacity,
								},
							},
						},
					},
				)
				answer, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Answer.")))
				if !assert.NoError(t, err) {
					return
				}
				stop := checkpointReceive(t, subscriber.stopped)
				assert.Equal(t, test.expected, expected{stop.Reason, answer})
				assert.Equal(t, gent.ExecutionFinished, result.Status)
				assert.Equal(t, gent.PhaseTerminal, store.view.Checkpoint.Phase)
				assert.Len(t, acceptedContent(store.view.Activity), 1)
				assert.Less(t, stop.LastDeliveredEventSequenceNumber, store.view.LastAppliedEventSequenceNumber)
				assert.Equal(t, execCtx.RunId(), stop.RunId)
			},
		)
	}
}

func TestCheckpointPendingLimitsDoNotAddSavepoints(t *testing.T) {
	type input struct{ recording *gent.RecordingConfig }
	type expected struct {
		phases []gent.ExecutionPhase
		answer string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	want := expected{[]gent.ExecutionPhase{gent.PhaseInitial, gent.PhaseIterationReady}, ""}
	for _, test := range []testCase{
		{"events", input{&gent.RecordingConfig{MaxPendingEvents: 16}}, want},
		{"bytes", input{&gent.RecordingConfig{MaxPendingBytes: 16 * 1024}}, want},
	} {
		t.Run(
			test.name,
			func(t *testing.T) {
				store := newCheckpointStore()
				emitted := 0
				loop := contentLoop(func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
					for range 256 {
						if err := execCtx.EmitChunk(checkpointChunk(strings.Repeat("x", 1024))); err != nil {
							return nil, err
						}
						emitted++
					}
					t.Error("the recording allowance must stop this attempt")
					return acceptRootOutput(execCtx, "unexpected")
				})
				runner, execCtx := newTestExecution(
					t,
					context.Background(),
					loop,
					&gent.Config{
						Codecs:    store.codecs,
						Recorder:  store,
						Recording: test.input.recording,
					},
				)
				answer, result, err := runner.NewTurn(
					execCtx,
					gent.NewTask(
						gent.TextPart(
							"Emit bounded progress.",
						),
					),
				)
				assert.Error(t, err)
				assert.Nil(t, result)
				actual := expected{answer: answer}
				for _, batch := range store.batches {
					actual.phases = append(actual.phases, batch.Checkpoint.Phase)
				}
				assert.Equal(t, test.expected, actual)
				assert.Greater(t, emitted, 0)
				assert.Less(t, emitted, 256)
				assert.Empty(t, contentChunks(t, store.view.Activity, store.codecs))
				// The failed handle cannot accept more work over its rejected provisional state.
				_, next, err := runner.Continue(execCtx)
				assert.Error(t, err)
				assert.Nil(t, next)
			},
		)
	}
}
