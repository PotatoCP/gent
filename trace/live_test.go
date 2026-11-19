package trace

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type liveRecorder func(context.Context, *gent.SaveBatch) error

func (save liveRecorder) Save(
	ctx context.Context, batch *gent.SaveBatch,
	captureRunStateSnapshot func() (*gent.RunState, error),
) error {
	return save(ctx, batch)
}

func receiveLive(t *testing.T, events <-chan *Event) *Event {
	t.Helper()
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("subscription closed before expected event")
		}
		return event
	case <-time.After(10 * time.Second):
		t.Fatal("live event did not arrive")
		return nil
	}
}

func TestLiveFeedBeforeSave(t *testing.T) {
	type input struct{ saveError error }
	type expected struct{ acceptedAnswers int }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"commit", input{}, expected{1}},
		{"failure", input{errors.New("save failed")}, expected{0}},
	}
	for _, test := range cases {
		t.Run(
			test.name,
			func(t *testing.T) {
				entered, release := make(chan *gent.SaveBatch, 1), make(chan struct{})
				unblock := sync.OnceFunc(func() { close(release) })
				defer unblock()
				store := &traceRecorder{codecs: gent.NewCodecRegistry()}
				seq := newTestSequencer(t, "", Config{IncludeChunkText: true, IncludeRunOutput: true})
				committed := newTestSequencer(t, "", Config{IncludeChunkText: true, IncludeRunOutput: true})
				defer seq.Close()
				defer committed.Close()
				recorder := &blockedTraceRecorder{store: store, entered: entered, release: release,
					failure: test.input.saveError}
				loop := &traceTestLoop{

					next: func(
						execCtx *gent.ExecutionContext,
					) (
						*gent.AgentLoopResult,
						error,
					) {
						if err := execCtx.EmitChunk(
							gent.StreamChunk{
								Content: []gent.ContentPart{
									&gent.AssistantContent{

										Type:      gent.AssistantContentIntermediary,
										MessageId: "preview",
										Content:   []gent.ContentPart{gent.TextPart("Live preview")},
									},
								},
							},
						); err != nil {
							return nil, err
						}
						return traceTestDone(execCtx)
					},
				}
				runner := executor.NewRunner[string](
					loop,
					&gent.Config{
						Codecs:   store.codecs,
						Recorder: recorder,
						Subscribers: []*gent.SubscriberRegistration{{Subscriber: seq, Delivery: gent.SubscriberLive},
							{Subscriber: committed}},
					},
				)
				execCtx, err := runner.NewExecutionContext(context.Background(), "live")
				require.NoError(t, err)
				defer func() { unblock(); execCtx.Release() }()
				sub, err := seq.SubscribeWithLimits(gent.DeliveryConfig{})
				require.NoError(t, err)
				defer sub.Close()
				done := make(chan error, 1)
				go func() {
					_, _, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Answer.")))
					done <- err
				}()
				batch := <-entered
				var preview *Event
				for preview == nil {
					event := receiveLive(t, sub.Events())
					if event.Type == EventTypeModelStreamChunk {
						preview = event
					}
				}
				assert.Greater(t, preview.EventSequenceNumber, batch.PreviousEventSequenceNumber)
				// Accepted delivery may still be draining earlier batches. Its cursor cannot pass
				// the accepted head while the terminal save is blocked.
				assert.LessOrEqual(t, committed.Snapshot().LastEventSequenceNumber,
					batch.PreviousEventSequenceNumber)
				assert.Equal(t, batch.PreviousEventSequenceNumber, store.view.LastAppliedEventSequenceNumber)
				assert.Equal(t, 0, countTraceEventType(seq.Snapshot().RecentEvents, EventTypeAcceptedAnswer))
				unblock()
				assert.ErrorIs(t, <-done, test.input.saveError)
				assert.Equal(t, test.expected.acceptedAnswers,
					countTraceEventType(committed.Snapshot().RecentEvents, EventTypeAcceptedAnswer))
				assert.Equal(t, test.expected.acceptedAnswers,
					countTraceEventType(seq.Snapshot().RecentEvents, EventTypeAcceptedAnswer))
				assert.Equal(t, store.view.LastAppliedEventSequenceNumber,
					committed.Snapshot().LastEventSequenceNumber)
				if test.input.saveError == nil {
					assert.Equal(t, committed.Snapshot(), seq.Snapshot())
				}
			},
		)
	}
}

type blockedTraceRecorder struct {
	store   *traceRecorder
	entered chan *gent.SaveBatch
	release chan struct{}
	failure error
}

func (recorder *blockedTraceRecorder) Save(ctx context.Context, batch *gent.SaveBatch,
	capture func() (*gent.RunState, error),
) error {
	if batch.Checkpoint.Phase == gent.PhaseTerminal {
		recorder.entered <- batch
		select {
		case <-recorder.release:
		case <-ctx.Done():
			return ctx.Err()
		}
		if recorder.failure != nil {
			return recorder.failure
		}
	}
	return recorder.store.Save(ctx, batch, capture)
}

func TestLiveFeedRestore(t *testing.T) {
	loop := &traceTestLoop{next: traceTestDone}
	runner, execCtx, store := newTraceRun(t, context.Background(), loop, &gent.Config{})
	_, _, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("First task.")))
	require.NoError(t, err)
	config := Config{IncludeRunOutput: true}
	seq, err := RestoreSequencer(*store.view, config)
	require.NoError(t, err)
	defer seq.Close()
	sub, err := seq.SubscribeWithLimits(gent.DeliveryConfig{})
	require.NoError(t, err)
	defer sub.Close()
	codecs := gent.NewCodecRegistry()
	loaded, err := codecs.UnmarshalRunState(store.runStateSnapshot)
	require.NoError(t, err)
	require.Equal(t, gent.RunStateReady, loaded.Status)
	fresh := executor.NewRunner[string](loop, &gent.Config{Codecs: codecs, Recorder: store,
		Subscribers: []*gent.SubscriberRegistration{{Subscriber: seq, Delivery: gent.SubscriberLive}}})
	before := seq.Snapshot()
	restored, err := fresh.Restore(context.Background(), loaded.State)
	require.NoError(t, err)
	require.Equal(t, gent.RestoreReady, restored.Status)
	defer restored.Context.Release()
	assert.Equal(t, before, seq.Snapshot(), "Restore prepares resources without starting delivery")
	_, _, err = fresh.NewTurn(restored.Context, gent.NewTask(gent.TextPart("Second task.")))
	require.NoError(t, err)
	event := receiveLive(t, sub.Events())
	assert.Equal(t, before.LastEventSequenceNumber+1, event.EventSequenceNumber)
	assert.Equal(t, restored.Context.TurnId(), event.TurnId)
	assert.NotEqual(t, before.TurnId, event.TurnId)
	replay, err := RestoreSequencer(*store.view, config)
	require.NoError(t, err)
	defer replay.Close()
	assert.Equal(t, replay.Snapshot(), seq.Snapshot())
}

func TestLiveSubscriptionLimits(t *testing.T) {
	type input struct {
		cfg   gent.DeliveryConfig
		count int
	}
	type expected struct{ err error }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"count", input{gent.DeliveryConfig{MaxEvents: 1}, 2},
			expected{gent.ErrDeliveryOverflow}},
		{"bytes", input{gent.DeliveryConfig{MaxBytes: 1}, 1},
			expected{gent.ErrDeliveryOverflow}},
		{"timeout", input{gent.DeliveryConfig{Timeout: time.Millisecond}, 1},
			expected{gent.ErrDeliveryTimeout}},
	} {
		t.Run(test.name, func(t *testing.T) {
			seq := newTestSequencer(t, "limits", Config{})
			slow, err := seq.SubscribeWithLimits(test.input.cfg)
			assert.NoError(t, err)
			defer slow.Close()
			fast, err := seq.SubscribeWithLimits(gent.DeliveryConfig{})
			assert.NoError(t, err)
			defer fast.Close()
			for range test.input.count {
				recordTestValue(t, seq, "common", &gent.CommonEvent{})
			}
			select {
			case <-slow.Done():
			case <-time.After(10 * time.Second):
				t.Fatal("slow subscriber did not stop")
			}
			assert.ErrorIs(t, slow.Err(), test.expected.err)
			var numbers []uint64
			for range test.input.count {
				numbers = append(numbers, receiveLive(t, fast.Events()).EventSequenceNumber)
			}
			assert.Equal(t, uint64(test.input.count), seq.Snapshot().LastEventSequenceNumber)
			assert.Equal(t, StitchStatusOK, ValidateStitch(0, seq.Snapshot().RecentEvents).Status)
			assert.Equal(t, seq.Snapshot().RecentEvents[len(numbers)-1].EventSequenceNumber,
				numbers[len(numbers)-1])
			assert.NoError(t, fast.Err())
		})
	}
}
