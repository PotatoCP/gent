package gent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type subscriberCallbacks struct {
	event func(context.Context, *RecordedEvent) error
	stop  func(context.Context, *SubscriberStop)
}

func (subscriber *subscriberCallbacks) OnEvent(ctx context.Context, event *RecordedEvent) error {
	return subscriber.event(ctx, event)
}

func (subscriber *subscriberCallbacks) OnStop(ctx context.Context, result *SubscriberStop) {
	subscriber.stop(ctx, result)
}

func waitSubscriber(t *testing.T, worker *subscriberWorker) {
	t.Helper()
	select {
	case <-worker.done:
	case <-time.After(3 * time.Second):
		t.Fatal("subscriber did not join its callbacks")
	}
}

func TestSubscriberConfiguration(t *testing.T) {
	type input struct{ registration SubscriberRegistration }
	type expected struct {
		config DeliveryConfig
		err    string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	defaults := DeliveryConfig{MaxEvents: 4096, MaxBytes: 8 * 1024 * 1024, Timeout: 30 * time.Second}
	tests := []testCase{
		{"omitted", input{}, expected{defaults, ""}},
		{"zero fields", input{SubscriberRegistration{Buffer: &DeliveryConfig{}}},
			expected{defaults, ""}},
		{"live", input{SubscriberRegistration{Delivery: SubscriberLive}}, expected{defaults, ""}},
		{"accepted", input{SubscriberRegistration{Delivery: SubscriberAccepted}},
			expected{defaults, ""}},
		{"custom", input{SubscriberRegistration{
			Buffer: &DeliveryConfig{MaxEvents: 2, MaxBytes: 64, Timeout: time.Second}}},
			expected{DeliveryConfig{MaxEvents: 2, MaxBytes: 64, Timeout: time.Second}, ""}},
		{"unknown mode", input{SubscriberRegistration{Delivery: "all"}},
			expected{DeliveryConfig{}, `unknown delivery mode "all"`}},
		{"negative events", input{SubscriberRegistration{Buffer: &DeliveryConfig{MaxEvents: -1}}},
			expected{DeliveryConfig{MaxEvents: -1}, "gent: delivery limits must not be negative"}},
		{"negative bytes", input{SubscriberRegistration{Buffer: &DeliveryConfig{MaxBytes: -1}}},
			expected{DeliveryConfig{MaxBytes: -1}, "gent: delivery limits must not be negative"}},
		{"negative timeout", input{SubscriberRegistration{Buffer: &DeliveryConfig{Timeout: -1}}},
			expected{DeliveryConfig{Timeout: -1}, "gent: delivery limits must not be negative"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := subscriberConfig(&test.input.registration)
			assert.Equal(t, test.expected.config, config)
			if test.expected.err == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, test.expected.err)
			}
		})
	}
}

func TestSubscriberDeliveryOutcomes(t *testing.T) {
	type input struct {
		limits    DeliveryConfig
		sizes     []int
		wait      bool
		failure   error
		waitFirst bool
	}
	type expected struct {
		sequences []uint64
		stop      SubscriberStop
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	failure := errors.New("socket write failed after sending data")
	stop := func(reason SubscriberStopReason, delivered uint64, err error) SubscriberStop {
		return SubscriberStop{RunId: "run", AttemptId: "attempt", Reason: reason,
			LastDeliveredEventSequenceNumber: delivered, LastAcceptedEventSequenceNumber: 20, Error: err}
	}
	tests := []testCase{
		{"normal drain", input{sizes: []int{3, 4, 2}},
			expected{[]uint64{10, 11, 12}, stop(SubscriberClosed, 12, nil)}},
		{"active callback counts toward event bound", input{
			limits: DeliveryConfig{MaxEvents: 1}, sizes: []int{3, 4}, wait: true, waitFirst: true},
			expected{[]uint64{10}, stop(SubscriberOverflow, 0, nil)}},
		{"active callback counts toward byte bound", input{
			limits: DeliveryConfig{MaxBytes: 6}, sizes: []int{4, 3}, wait: true, waitFirst: true},
			expected{[]uint64{10}, stop(SubscriberOverflow, 0, nil)}},
		{"one oversized event", input{limits: DeliveryConfig{MaxBytes: 6}, sizes: []int{7}},
			expected{nil, stop(SubscriberOverflow, 0, nil)}},
		{"failed callback leaves delivery cursor unchanged", input{sizes: []int{3}, failure: failure},
			expected{[]uint64{10}, stop(SubscriberFailed, 0, errors.Join(failure))}},
		{"callback timeout", input{limits: DeliveryConfig{Timeout: 20 * time.Millisecond},
			sizes: []int{3}, wait: true},
			expected{[]uint64{10}, stop(SubscriberTimeout, 0, nil)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var received []*RecordedEvent
			var sequences []uint64
			var stopped SubscriberStop
			var stopError error
			var stopDeadline time.Time
			var stopStarted time.Time
			entered := make(chan struct{}, 1)
			subscriber := &subscriberCallbacks{
				event: func(ctx context.Context, event *RecordedEvent) error {
					received = append(received, event)
					sequences = append(sequences, event.EventSequenceNumber)
					if event.EventSequenceNumber == 10 {
						entered <- struct{}{}
					}
					if test.input.wait {
						<-ctx.Done()
						return ctx.Err()
					}
					return test.input.failure
				},
				stop: func(ctx context.Context, result *SubscriberStop) {
					stopStarted = time.Now()
					stopError = ctx.Err()
					stopDeadline, _ = ctx.Deadline()
					stopped = *result
				},
			}
			worker, err := newSubscriberWorker(context.Background(), "run", "attempt",
				&SubscriberRegistration{Subscriber: subscriber, Buffer: &test.input.limits},
				func() uint64 { return 20 })
			if !assert.NoError(t, err) {
				return
			}
			var sent []*RecordedEvent
			for i, size := range test.input.sizes {
				event := &RecordedEvent{EventSequenceNumber: uint64(i + 10), Type: "progress"}
				sent = append(sent, event)
				worker.send(event, size)
				if i == 0 && test.input.waitFirst {
					<-entered
				}
			}
			worker.close()
			waitSubscriber(t, worker)
			assert.Equal(t, test.expected.sequences, sequences)
			assert.Equal(t, test.expected.stop, stopped)
			assert.NoError(t, stopError)
			assert.GreaterOrEqual(t, stopDeadline, stopStarted)
			for i, event := range received {
				assert.Same(t, sent[i], event, "delivery borrows each frozen event")
			}
		})
	}
}

func TestSubscriberShutdownJoinsActiveCallbacks(t *testing.T) {
	entered := make(chan struct{})
	canceled := make(chan struct{})
	finishEvent := make(chan struct{})
	stopEntered := make(chan struct{})
	finishStop := make(chan struct{})
	failure := errors.New("write completed with an unexpected failure")
	var stopped SubscriberStop
	subscriber := &subscriberCallbacks{
		event: func(ctx context.Context, event *RecordedEvent) error {
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-finishEvent
			return failure
		},
		stop: func(ctx context.Context, result *SubscriberStop) {
			stopped = *result
			close(stopEntered)
			<-finishStop
		},
	}
	worker, err := newSubscriberWorker(context.Background(), "run", "attempt",
		&SubscriberRegistration{Subscriber: subscriber, Buffer: &DeliveryConfig{MaxEvents: 1}},
		func() uint64 { return 5 })
	if !assert.NoError(t, err) {
		return
	}
	worker.send(&RecordedEvent{EventSequenceNumber: 6}, 1)
	<-entered
	worker.send(&RecordedEvent{EventSequenceNumber: 7}, 1)
	<-canceled
	select {
	case <-stopEntered:
		t.Fatal("OnStop overlapped OnEvent")
	default:
	}
	close(finishEvent)
	<-stopEntered
	select {
	case <-worker.done:
		t.Fatal("worker ended before OnStop returned")
	default:
	}
	close(finishStop)
	waitSubscriber(t, worker)
	assert.Equal(t, SubscriberStop{RunId: "run", AttemptId: "attempt", Reason: SubscriberOverflow,
		LastAcceptedEventSequenceNumber: 5, Error: errors.Join(failure)}, stopped)
}

func TestSubscriberFinalDrainHasOneTotalDeadline(t *testing.T) {
	entered := make(chan struct{})
	finishFirst := make(chan struct{})
	var stopped SubscriberStop
	var secondDeadline time.Time
	timeout := 200 * time.Millisecond
	subscriber := &subscriberCallbacks{
		event: func(ctx context.Context, event *RecordedEvent) error {
			if event.EventSequenceNumber == 1 {
				close(entered)
				<-finishFirst
				return nil
			}
			secondDeadline, _ = ctx.Deadline()
			<-ctx.Done()
			return ctx.Err()
		},
		stop: func(_ context.Context, result *SubscriberStop) { stopped = *result },
	}
	worker, err := newSubscriberWorker(context.Background(), "run", "attempt",
		&SubscriberRegistration{Subscriber: subscriber, Buffer: &DeliveryConfig{Timeout: timeout}},
		func() uint64 { return 2 })
	if !assert.NoError(t, err) {
		return
	}
	worker.send(&RecordedEvent{EventSequenceNumber: 1}, 1)
	<-entered
	worker.send(&RecordedEvent{EventSequenceNumber: 2}, 1)
	beforeClose := time.Now()
	worker.close()
	afterClose := time.Now()
	// Spend part of the drain allowance in the first callback. The second must inherit the
	// remaining allowance rather than start another full per-event shutdown wait.
	<-time.After(50 * time.Millisecond)
	close(finishFirst)
	waitSubscriber(t, worker)
	assert.GreaterOrEqual(t, secondDeadline, beforeClose.Add(timeout))
	assert.LessOrEqual(t, secondDeadline, afterClose.Add(timeout))
	assert.Equal(t, SubscriberStop{RunId: "run", AttemptId: "attempt", Reason: SubscriberTimeout,
		LastDeliveredEventSequenceNumber: 1, LastAcceptedEventSequenceNumber: 2}, stopped)
}

func TestSubscriberRegistrationsRunIndependently(t *testing.T) {
	blocked := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var events []uint64
	slow := &subscriberCallbacks{
		event: func(ctx context.Context, event *RecordedEvent) error {
			close(blocked)
			<-release
			return nil
		},
		stop: func(context.Context, *SubscriberStop) {},
	}
	fast := &subscriberCallbacks{
		event: func(ctx context.Context, event *RecordedEvent) error {
			mu.Lock()
			events = append(events, event.EventSequenceNumber)
			mu.Unlock()
			return nil
		},
		stop: func(context.Context, *SubscriberStop) {},
	}
	cursor := func() uint64 { return 2 }
	first, err := newSubscriberWorker(context.Background(), "run", "attempt",
		&SubscriberRegistration{Subscriber: slow}, cursor)
	if !assert.NoError(t, err) {
		return
	}
	second, err := newSubscriberWorker(context.Background(), "run", "attempt",
		&SubscriberRegistration{Subscriber: fast}, cursor)
	if !assert.NoError(t, err) {
		close(release)
		first.close()
		waitSubscriber(t, first)
		return
	}
	first.send(&RecordedEvent{EventSequenceNumber: 1}, 1)
	<-blocked
	second.send(&RecordedEvent{EventSequenceNumber: 1}, 1)
	second.send(&RecordedEvent{EventSequenceNumber: 2}, 1)
	first.close()
	second.close()
	waitSubscriber(t, second)
	assert.Equal(t, []uint64{1, 2}, events)
	select {
	case <-first.done:
		t.Fatal("slow callback was abandoned")
	default:
	}
	close(release)
	waitSubscriber(t, first)
}
