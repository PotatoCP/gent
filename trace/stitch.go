package trace

import "github.com/rickchristie/gent"

// ErrReplayUnavailable means the requested event range is no longer in memory.
var ErrReplayUnavailable = gent.ErrReplayUnavailable

// CanStitch reports whether the first buffered event could stitch to a snapshot.
func CanStitch(
	snapshotLastEventSequenceNumber uint64,
	firstBufferedEventSequenceNumber uint64,
) bool {
	return firstBufferedEventSequenceNumber <= snapshotLastEventSequenceNumber+1
}

// StitchStatus describes whether buffered events can be applied to a snapshot.
type StitchStatus string

const (
	// StitchStatusOK means buffered events can follow the saved cursor without a gap.
	StitchStatusOK StitchStatus = "ok"
	// StitchStatusGap means an event is missing between the saved cursor and buffered events.
	StitchStatusGap StitchStatus = "gap"
	// StitchStatusUnsorted means buffered event sequence numbers are not strictly increasing.
	StitchStatusUnsorted StitchStatus = "unsorted"
)

// StitchResult describes the result of validating buffered trace events.
// EventSequenceNumber fields refer to the same ordered positions as Event.EventSequenceNumber.
// Missing positions are greater than MissingAfterEventSequenceNumber and less than
// MissingBeforeEventSequenceNumber.
type StitchResult struct {
	Status                           StitchStatus `json:"status"`
	SnapshotLastEventSequenceNumber  uint64       `json:"snapshotLastEventSequenceNumber"`
	FirstBufferedEventSequenceNumber uint64       `json:"firstBufferedEventSequenceNumber,omitempty"`
	MissingAfterEventSequenceNumber  uint64       `json:"missingAfterEventSequenceNumber,omitempty"`
	MissingBeforeEventSequenceNumber uint64       `json:"missingBeforeEventSequenceNumber,omitempty"`
}

// ValidateStitch validates that events are sorted, contiguous, and can apply to a snapshot.
func ValidateStitch(snapshotLastEventSequenceNumber uint64, events []*Event) StitchResult {
	result := StitchResult{
		Status:                          StitchStatusOK,
		SnapshotLastEventSequenceNumber: snapshotLastEventSequenceNumber,
	}
	if len(events) == 0 {
		return result
	}
	result.FirstBufferedEventSequenceNumber = events[0].EventSequenceNumber

	var previous uint64
	for i, event := range events {
		if event == nil {
			continue
		}
		if i > 0 && event.EventSequenceNumber <= previous {
			result.Status = StitchStatusUnsorted
			return result
		}
		if i > 0 && event.EventSequenceNumber != previous+1 {
			result.Status = StitchStatusGap
			result.MissingAfterEventSequenceNumber = previous
			result.MissingBeforeEventSequenceNumber = event.EventSequenceNumber
			return result
		}
		previous = event.EventSequenceNumber
	}

	for _, event := range events {
		if event == nil || event.EventSequenceNumber <= snapshotLastEventSequenceNumber {
			continue
		}
		if event.EventSequenceNumber != snapshotLastEventSequenceNumber+1 {
			result.Status = StitchStatusGap
			result.MissingAfterEventSequenceNumber = snapshotLastEventSequenceNumber
			result.MissingBeforeEventSequenceNumber = event.EventSequenceNumber
		}
		return result
	}
	return result
}
