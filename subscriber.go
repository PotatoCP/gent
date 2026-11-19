package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// Subscriber receives read-only evidence. Gent uses one serial worker per registration per
// attempt. Callbacks on different registrations or runs can overlap, even for the same instance.
// A delivery failure stops only that registration; saved evidence remains available for replay.
type Subscriber = core.Subscriber

// SubscriberRegistration chooses a delivery mode and per-attempt queue limits. An empty Delivery
// selects Accepted. A nil Buffer or zero field selects the default. Configure before use.
type SubscriberRegistration = core.SubscriberRegistration

// SubscriberDelivery selects whether evidence must be accepted before delivery.
type SubscriberDelivery = core.SubscriberDelivery

// SubscriberAccepted delivers events after their complete SaveBatch is accepted.
const SubscriberAccepted SubscriberDelivery = core.SubscriberAccepted

// SubscriberLive delivers provisional events, subject to the accepted-output save barrier.
const SubscriberLive SubscriberDelivery = core.SubscriberLive

// SubscriberStopReason explains why one attempt's event delivery ended.
type SubscriberStopReason = core.SubscriberStopReason

// SubscriberClosed means the attempt ended or its owner ended delivery.
const SubscriberClosed SubscriberStopReason = core.SubscriberClosed

// SubscriberOverflow means an event exceeded the queue's count or byte allowance.
const SubscriberOverflow SubscriberStopReason = core.SubscriberOverflow

// SubscriberTimeout means callback delivery or the final queue drain timed out.
const SubscriberTimeout SubscriberStopReason = core.SubscriberTimeout

// SubscriberFailed means an unexpected OnEvent error stopped delivery.
const SubscriberFailed SubscriberStopReason = core.SubscriberFailed

// SubscriberStop reports runtime delivery state; it is not a saved execution event. The delivered
// cursor advances only after OnEvent returns nil. The accepted cursor reports Gent's accepted head,
// not consumer delivery. For delivered 140 and accepted 150, replay accepted events after 140.
// Neither cursor proves a browser received an event. Use RunId + EventSequenceNumber for accepted
// deduplication, and include AttemptId for Live previews that can disappear after interruption.
// Error retains an unexpected callback failure even when another stop reason took precedence.
type SubscriberStop = core.SubscriberStop
