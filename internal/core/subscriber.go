package gent

import (
	"context"
	"fmt"
)

// Subscriber receives read-only evidence. Gent uses one serial worker per registration per
// attempt. Callbacks on different registrations or runs can overlap, even for the same instance.
// A delivery failure stops only that registration; saved evidence remains available for replay.
type Subscriber interface {
	// OnEvent borrows the envelope and every reachable value read-only. Do not modify nested
	// pointers, slices, maps, or bytes. Make independent values only when a mutable view is needed.
	// Honor ctx, including network writes. Accepted consumers handle bursts and duplicates;
	// Live consumers also replace lost previews after recovery. Deduplicate Accepted events by
	// RunId + EventSequenceNumber, and Live events by RunId + AttemptId + EventSequenceNumber.
	// A failed callback can have sent data already. A nil return advances Gent's delivery cursor,
	// not a browser acknowledgment. Reconnect against saved history at a fixed accepted head.
	OnEvent(ctx context.Context, event *RecordedEvent) error

	// OnStop runs once after the active OnEvent returns. It receives a fresh timeout context.
	// Honor that context. Gent waits for this callback; no worker is left running after cleanup.
	OnStop(ctx context.Context, result *SubscriberStop)
}

// SubscriberRegistration chooses a delivery mode and per-attempt queue limits. An empty Delivery
// selects Accepted. A nil Buffer or zero field selects the default. Configure before use.
type SubscriberRegistration struct {
	Subscriber Subscriber
	Delivery   SubscriberDelivery
	Buffer     *DeliveryConfig
}

// SubscriberDelivery selects whether evidence must be accepted before delivery.
type SubscriberDelivery string

const (
	// SubscriberAccepted delivers events after their complete SaveBatch is accepted.
	SubscriberAccepted SubscriberDelivery = "accepted"

	// SubscriberLive delivers provisional events, subject to the accepted-output save barrier.
	SubscriberLive SubscriberDelivery = "live"
)

// SubscriberStopReason explains why one attempt's event delivery ended.
type SubscriberStopReason string

const (
	// SubscriberClosed means the attempt ended or its owner ended delivery.
	SubscriberClosed SubscriberStopReason = "closed"

	// SubscriberOverflow means an event exceeded the queue's count or byte allowance.
	SubscriberOverflow SubscriberStopReason = "overflow"

	// SubscriberTimeout means callback delivery or the final queue drain timed out.
	SubscriberTimeout SubscriberStopReason = "timeout"

	// SubscriberFailed means an unexpected OnEvent error stopped delivery.
	SubscriberFailed SubscriberStopReason = "failed"
)

// SubscriberStop reports runtime delivery state; it is not a saved execution event. The delivered
// cursor advances only after OnEvent returns nil. The accepted cursor reports Gent's accepted head,
// not consumer delivery. For delivered 140 and accepted 150, replay accepted events after 140.
// Neither cursor proves a browser received an event. Use RunId + EventSequenceNumber for accepted
// deduplication, and include AttemptId for Live previews that can disappear after interruption.
// Error retains an unexpected callback failure even when another stop reason took precedence.
type SubscriberStop struct {
	RunId                            string
	AttemptId                        string
	Reason                           SubscriberStopReason
	LastDeliveredEventSequenceNumber uint64
	LastAcceptedEventSequenceNumber  uint64
	Error                            error
}

func subscriberConfig(registration *SubscriberRegistration) (DeliveryConfig, error) {
	if registration.Delivery != "" && registration.Delivery != SubscriberAccepted &&
		registration.Delivery != SubscriberLive {
		return DeliveryConfig{}, fmt.Errorf("unknown delivery mode %q", registration.Delivery)
	}
	config := DeliveryConfig{}
	if registration.Buffer != nil {
		config = *registration.Buffer
	}
	return deliveryConfig(config)
}
