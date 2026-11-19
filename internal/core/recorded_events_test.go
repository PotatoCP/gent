package gent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func savedTestEvents(t testing.TB, recorder *runStateRecorder) []*RecordedEvent {
	t.Helper()
	var events []*RecordedEvent
	for _, data := range recorder.batches {
		var batch SaveBatch
		if err := recorder.codecs.Unmarshal(data, &batch); err != nil {
			t.Fatal(err)
		}
		events = append(events, batch.Events...)
	}
	return events
}

func TestRecordedEventsPreserveLifecycleOrder(t *testing.T) {
	codecs := NewCodecRegistry()
	recorder := &runStateRecorder{codecs: codecs}
	loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
		before := execCtx.PublishBeforeModelCall("model", &ModelCallRequest{})
		execCtx.PublishAfterModelCall("model", before.Request, nil, 0, nil,
			WithModelCallId(before.ModelCallId))
		return acceptTurnText(execCtx, "Done")
	}}
	runner := NewRunner[string](loop, &Config{Recorder: recorder, Codecs: codecs})
	execCtx, err := runner.NewExecutionContext(context.Background(), "events")
	require.NoError(t, err)
	defer execCtx.Release()
	_, _, err = runner.NewTurn(execCtx, NewTask(TextPart("Start.")))
	require.NoError(t, err)
	var names []string
	for _, event := range savedTestEvents(t, recorder) {
		if _, ok := event.Payload.(Event); ok {
			names = append(names, event.EventName)
		}
	}
	assert.Equal(t, []string{EventNameExecutionBefore, EventNameIterationBefore,
		EventNameModelCallBefore, EventNameModelCallAfter, EventNameIterationAfter,
		EventNameExecutionAfter}, names)
}

func TestConcurrentPublicationKeepsEverySavedEvent(t *testing.T) {
	type input struct{ workers, count int }
	type expected struct{ total int }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"ten workers", input{10, 100}, expected{1000}},
		{"fifty workers", input{50, 20}, expected{1000}},
		{"one hundred workers", input{100, 10}, expected{1000}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := NewCodecRegistry()
			recorder := &runStateRecorder{codecs: codecs}
			loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				var workers sync.WaitGroup
				start := make(chan struct{})
				for worker := range test.input.workers {
					workers.Add(1)
					go func() {
						defer workers.Done()
						<-start
						for index := range test.input.count {
							execCtx.PublishCommonEvent("test:concurrent", "concurrent event",
								map[string]int{"worker": worker, "event": index})
						}
					}()
				}
				close(start)
				workers.Wait()
				return acceptTurnText(execCtx, "Done")
			}}
			runner := NewRunner[string](loop, &Config{Recorder: recorder, Codecs: codecs})
			execCtx, err := runner.NewExecutionContext(context.Background(), "concurrent")
			require.NoError(t, err)
			defer execCtx.Release()
			_, _, err = runner.NewTurn(execCtx, NewTask(TextPart("Start.")))
			require.NoError(t, err)
			want := map[string]int{}
			for worker := range test.input.workers {
				for index := range test.input.count {
					want[fmt.Sprintf("%d/%d", worker, index)] = 1
				}
			}
			got := map[string]int{}
			for i, event := range savedTestEvents(t, recorder) {
				assert.Equal(t, uint64(i+1), event.EventSequenceNumber)
				if common, ok := event.Payload.(*CommonEvent); ok {
					data := common.Data.(map[string]int)
					got[fmt.Sprintf("%d/%d", data["worker"], data["event"])]++
				}
			}
			assert.Equal(t, expected{len(got)}, test.expected)
			assert.Equal(t, want, got)
		})
	}
}

type modelEventSubscriber struct{ delivered chan *RecordedEvent }

func (subscriber *modelEventSubscriber) OnEvent(_ context.Context, event *RecordedEvent) error {
	if _, ok := event.Payload.(*BeforeModelCallEvent); ok {
		subscriber.delivered <- event
	}
	return nil
}
func (*modelEventSubscriber) OnStop(context.Context, *SubscriberStop) {}

func TestLiveDeliveryWaitsForMutableHook(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	subscriber := &modelEventSubscriber{delivered: make(chan *RecordedEvent, 1)}
	codecs := NewCodecRegistry()
	recorder := &runStateRecorder{codecs: codecs}
	model := &requestOwnershipModel{}
	loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
		stream, err := execCtx.GenerateContentStream(model, ModelCall{},
			NewModelCallRequest([]*ModelMessage{TextParts(ChatMessageTypeHuman, "original")}))
		if err != nil {
			return nil, err
		}
		defer stream.Close()
		for range stream.Chunks() {
		}
		if _, err := stream.Response(); err != nil {
			return nil, err
		}
		return acceptTurnText(execCtx, "Done")
	}}
	runner := NewRunner[string](loop, &Config{Recorder: recorder, Codecs: codecs,
		Subscribers: []*SubscriberRegistration{{Subscriber: subscriber, Delivery: SubscriberLive}},
		Hooks: &Hooks{BeforeModelCall: func(_ context.Context, event *BeforeModelCallEvent) error {
			event.Request.Messages[0].Parts[0] = TextPart("changed")
			close(entered)
			<-release
			return nil
		}},
	})
	execCtx, err := runner.NewExecutionContext(context.Background(), "mutable")
	require.NoError(t, err)
	defer execCtx.Release()
	done := make(chan error, 1)
	go func() {
		_, _, err := runner.NewTurn(execCtx, NewTask(TextPart("Start.")))
		done <- err
	}()
	<-entered
	select {
	case <-subscriber.delivered:
		t.Error("mutable request escaped before the hook returned")
	default:
	}
	close(release)
	require.NoError(t, <-done)
	var received *RecordedEvent
	select {
	case received = <-subscriber.delivered:
	case <-time.After(time.Second):
		t.Fatal("frozen request was not delivered")
	}
	assert.Equal(t, []*ModelMessage{TextParts(ChatMessageTypeHuman, "changed")},
		received.Payload.(*BeforeModelCallEvent).Request.Messages)
	var saved *RecordedEvent
	for _, event := range savedTestEvents(t, recorder) {
		if event.EventSequenceNumber == received.EventSequenceNumber {
			saved = event
		}
	}
	assert.Equal(t, saved, received)
}

func TestStatsLimitsSaveOneSpecificEvent(t *testing.T) {
	type input struct {
		gauge  bool
		prefix bool
		stop   int
	}
	type expected struct{ value float64 }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"counter first", input{stop: 1}, expected{1}},
		{"counter later", input{stop: 3}, expected{3}},
		{"gauge first", input{gauge: true, stop: 1}, expected{1}},
		{"gauge later", input{gauge: true, stop: 3}, expected{3}},
		{"prefix first", input{prefix: true, stop: 1}, expected{1}},
		{"prefix later", input{prefix: true, stop: 3}, expected{3}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := NewCodecRegistry()
			recorder := &runStateRecorder{codecs: codecs}
			key := StatKey("test:usage")
			limit := Limit{Type: LimitExactKey, Key: key, MaxValue: float64(test.input.stop - 1)}
			if test.input.prefix {
				limit.Type = LimitKeyPrefix
				key += ":beta"
			}
			loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				if test.input.prefix && execCtx.Iteration() == 1 {
					execCtx.Stats().IncrCounter("test:usage:alpha", int64(test.input.stop-1))
				}
				if test.input.gauge {
					execCtx.Stats().IncrGauge(key, 1)
				} else {
					execCtx.Stats().IncrCounter(key, 1)
				}
				if execCtx.ExceededLimit() != nil {
					// Repeated checks must not publish another limit event.
					execCtx.Stats().IncrCounter("test:other", 1)
				}
				return &AgentLoopResult{Action: LAContinue}, nil
			}}
			runner := NewRunner[string](loop, &Config{Recorder: recorder, Codecs: codecs,
				Limits: []Limit{limit}})
			execCtx, err := runner.NewExecutionContext(context.Background(), "limit")
			require.NoError(t, err)
			defer execCtx.Release()
			earliest := time.Now()
			value, result, err := runner.NewTurn(execCtx, NewTask(TextPart("Start.")))
			require.NoError(t, err)
			assert.Equal(t, "", value)
			assert.Equal(t, &ExecutionResult{Status: ExecutionPaused, TurnId: execCtx.TurnId(),
				TerminationReason: TerminationLimitExceeded,
				ExceededLimit: &LimitExceeded{Limit: &limit, MatchedKey: key,
					CurrentValue: test.expected.value}}, result)
			var exceeded []*LimitExceededEvent
			for _, event := range savedTestEvents(t, recorder) {
				if payload, ok := event.Payload.(*LimitExceededEvent); ok {
					exceeded = append(exceeded, payload)
				}
			}
			require.Len(t, exceeded, 1)
			event := exceeded[0]
			assert.GreaterOrEqual(t, event.Timestamp.UnixNano(), earliest.UnixNano())
			base := event.BaseEvent
			assert.Equal(t, test.input.stop, base.Iteration)
			assert.Equal(t, execCtx.ContextId(), base.ContextId)
			assert.Equal(t, &LimitExceededEvent{BaseEvent: base, Limit: limit,
				CurrentValue: test.expected.value, MatchedKey: key}, event)
		})
	}
}
