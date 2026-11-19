package gent

import (
	"context"
	"errors"
	"sync"
	"time"
)

type subscriberItem struct {
	event *RecordedEvent
	size  int
}

// One worker owns callback order. Producers only append bounded, frozen references under mu;
// they never wait for a callback or hold an execution lock while application code runs.
type subscriberWorker struct {
	mu         sync.Mutex
	subscriber Subscriber
	config     DeliveryConfig
	delivery   SubscriberDelivery
	ctx        context.Context
	cancel     context.CancelFunc
	accepted   func() uint64
	stopOwner  func() bool
	wake       chan struct{}
	done       chan struct{}
	queue      []subscriberItem
	head       int
	count      int
	bytes      int
	closing    bool
	stopped    bool
	drainEnd   time.Time
	drainTimer *time.Timer
	result     SubscriberStop
}

func newSubscriberWorker(
	ctx context.Context, runId, attemptId string,
	registration *SubscriberRegistration, accepted func() uint64,
) (*subscriberWorker, error) {
	config, err := subscriberConfig(registration)
	if err != nil {
		return nil, err
	}
	workerCtx, cancel := context.WithCancel(ctx)
	worker := &subscriberWorker{subscriber: registration.Subscriber, config: config,
		delivery: registration.Delivery, ctx: workerCtx, cancel: cancel, accepted: accepted,
		wake: make(chan struct{}, 1), done: make(chan struct{}),
		result: SubscriberStop{RunId: runId, AttemptId: attemptId}}
	worker.stopOwner = context.AfterFunc(ctx, func() { worker.abort(SubscriberClosed, nil) })
	go worker.run()
	return worker, nil
}

func (worker *subscriberWorker) send(event *RecordedEvent, size int) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.closing || worker.stopped {
		return
	}
	if worker.count >= worker.config.MaxEvents || size > worker.config.MaxBytes-worker.bytes {
		worker.stopLocked(SubscriberOverflow, nil)
		return
	}
	worker.queue = append(worker.queue, subscriberItem{event: event, size: size})
	worker.count++
	worker.bytes += size
	worker.signal()
}

// close ends enqueueing and starts one total drain allowance, including the active callback.
// Call close on all registrations before waiting so separate subscriptions drain concurrently.
func (worker *subscriberWorker) close() {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.closing || worker.stopped {
		return
	}
	worker.closing = true
	worker.drainEnd = time.Now().Add(worker.config.Timeout)
	worker.drainTimer = time.AfterFunc(worker.config.Timeout, func() {
		worker.abort(SubscriberTimeout, nil)
	})
	worker.signal()
}

func (worker *subscriberWorker) abort(reason SubscriberStopReason, err error) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	worker.stopLocked(reason, err)
}

func (worker *subscriberWorker) stopLocked(reason SubscriberStopReason, err error) {
	if !worker.stopped {
		worker.stopped = true
		worker.result.Reason = reason
		for _, item := range worker.queue[worker.head:] {
			worker.count--
			worker.bytes -= item.size
		}
		worker.queue, worker.head = nil, 0
		worker.cancel()
	}
	if err != nil {
		worker.result.Error = errors.Join(worker.result.Error, err)
	}
	worker.signal()
}

func (worker *subscriberWorker) signal() {
	select {
	case worker.wake <- struct{}{}:
	default:
	}
}

func (worker *subscriberWorker) next() (subscriberItem, time.Time, bool) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.stopped {
		return subscriberItem{}, time.Time{}, false
	}
	if worker.head == len(worker.queue) {
		if worker.closing {
			worker.stopLocked(SubscriberClosed, nil)
		}
		return subscriberItem{}, time.Time{}, false
	}
	item := worker.queue[worker.head]
	worker.queue[worker.head] = subscriberItem{}
	worker.head++
	if worker.head == len(worker.queue) {
		worker.queue, worker.head = nil, 0
	} else if worker.head >= 1024 && worker.head >= len(worker.queue)/2 {
		// Reuse bounded queue space without retaining references to delivered payloads.
		count := copy(worker.queue, worker.queue[worker.head:])
		clear(worker.queue[count:])
		worker.queue, worker.head = worker.queue[:count], 0
	}
	deadline := time.Now().Add(worker.config.Timeout)
	if !worker.drainEnd.IsZero() && worker.drainEnd.Before(deadline) {
		deadline = worker.drainEnd
	}
	return item, deadline, true
}

func (worker *subscriberWorker) run() {
	defer close(worker.done)
	defer worker.cancel()
	defer worker.stopOwner()
	for {
		item, deadline, ok := worker.next()
		if ok {
			worker.deliver(item, deadline)
			continue
		}
		worker.mu.Lock()
		stopped := worker.stopped
		worker.mu.Unlock()
		if stopped {
			break
		}
		<-worker.wake
	}
	worker.mu.Lock()
	if worker.drainTimer != nil {
		worker.drainTimer.Stop()
	}
	result := worker.result
	worker.mu.Unlock()
	result.LastAcceptedEventSequenceNumber = worker.accepted()
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(worker.ctx), worker.config.Timeout)
	defer cancel()
	worker.subscriber.OnStop(stopCtx, &result)
}

func (worker *subscriberWorker) deliver(item subscriberItem, deadline time.Time) {
	ctx, cancel := context.WithDeadline(worker.ctx, deadline)
	err := worker.subscriber.OnEvent(ctx, item.event)
	contextErr, cause := ctx.Err(), context.Cause(ctx)
	cancel()
	worker.mu.Lock()
	defer worker.mu.Unlock()
	worker.count--
	worker.bytes -= item.size
	if errors.Is(contextErr, context.DeadlineExceeded) {
		worker.stopLocked(SubscriberTimeout, nil)
	}
	if err == nil {
		worker.result.LastDeliveredEventSequenceNumber = item.event.EventSequenceNumber
	} else if !onlyCancellation(err, contextErr, cause) {
		worker.stopLocked(SubscriberFailed, err)
	}
}
