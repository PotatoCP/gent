package examples

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/trace"
)

// docs:start subscriber-events
type EventLog struct {
	mu     sync.Mutex
	Events []*gent.RecordedEvent
	Stops  []*gent.SubscriberStop
}

func (log *EventLog) OnEvent(ctx context.Context, event *gent.RecordedEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	// Events and reachable payloads are read-only, even after this callback returns.
	log.Events = append(log.Events, event)
	return nil
}

// docs:end subscriber-events

// docs:start subscriber-stop
func (log *EventLog) OnStop(ctx context.Context, stop *gent.SubscriberStop) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.Stops = append(log.Stops, stop)
	// A delivery stop is not the result of the agent task.
}

// docs:end subscriber-stop

func subscriberConfig(live, accepted gent.Subscriber) *gent.Config {
	// docs:start subscriber-config
	config := &gent.Config{Subscribers: []*gent.SubscriberRegistration{
		{Subscriber: accepted}, // Empty Delivery selects Accepted.
		{Subscriber: live, Delivery: gent.SubscriberLive, Buffer: &gent.DeliveryConfig{
			MaxEvents: 256,
			MaxBytes:  2 << 20,
			Timeout:   5 * time.Second,
		}},
	}}
	// Separate handlers identify the delivery mode without changing saved events.
	// docs:end subscriber-config
	return config
}

// docs:start stream-keys
type AcceptedKey struct {
	RunId               string
	EventSequenceNumber uint64
}

type LiveKey struct {
	AcceptedKey
	AttemptId string
}

func eventKeys(event *gent.RecordedEvent) (AcceptedKey, LiveKey) {
	accepted := AcceptedKey{event.RunId, event.EventSequenceNumber}
	return accepted, LiveKey{accepted, event.AttemptId}
}

// docs:end stream-keys

func contentPreview(event *gent.RecordedEvent) []gent.ContentPart {
	// docs:start stream-payload
	chunk, ok := event.Payload.(*gent.StreamChunk)
	if !ok {
		return nil
	}
	// The same AssistantContent representation is used for deltas and complete output.
	return chunk.Content
	// docs:end stream-payload
}

func traceConfig(runId string) (*trace.Sequencer, *gent.Config, error) {
	// docs:start trace-attach
	seq, err := trace.NewSequencer(runId, trace.Config{})
	if err != nil {
		return nil, nil, err
	}
	config := &gent.Config{Subscribers: []*gent.SubscriberRegistration{
		{Subscriber: seq, Delivery: gent.SubscriberLive},
	}}
	// The application closes seq when its display session ends.
	// docs:end trace-attach
	return seq, config, nil
}

func traceArchive(view *gent.RunView) (*trace.Sequencer, error) {
	// docs:start trace-replay
	seq, err := trace.RestoreSequencer(*view, trace.Config{})
	if err != nil {
		return nil, err
	}
	// Trace is a display view. Keep SaveBatch storage for execution recovery.
	// docs:end trace-replay
	return seq, nil
}

func traceSubscribe(seq *trace.Sequencer) (*trace.Subscription, error) {
	// docs:start trace-subscribe
	subscription, err := seq.SubscribeWithLimits(gent.DeliveryConfig{
		MaxEvents: 128,
		MaxBytes:  1 << 20,
		Timeout:   5 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	// Read Events and inspect Err when Done closes. The owner must call Close.
	// docs:end trace-subscribe
	return subscription, nil
}

func deliveredPosition(stop *gent.SubscriberStop) string {
	// docs:start subscriber-cursor
	message := fmt.Sprintf("Delivered through %d; accepted through %d",
		stop.LastDeliveredEventSequenceNumber, stop.LastAcceptedEventSequenceNumber)
	// If these are 140 and 150, load accepted events after 140. Deduplicate delivery.
	// docs:end subscriber-cursor
	return message
}
