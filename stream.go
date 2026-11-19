package gent

import (
	"context"
	core "github.com/rickchristie/gent/internal/core"
)

// NewCompletedStream creates a Stream that is already complete with the given
// response. The stream emits each non-empty text channel then closes.
// This is used by mock models and non-streaming callers that need to satisfy the
// [Model] interface without actual streaming.
func NewCompletedStream(resp *ModelCallResponse, err error) Stream {
	return core.NewCompletedStream(resp, err)
}

// StreamWithDuration is a helper to measure streaming duration.
// It wraps a stream buffer to track start time.
type StreamWithDuration = core.StreamWithDuration

// NewStreamWithDuration creates a stream buffer that tracks duration.
func NewStreamWithDuration() *StreamWithDuration {
	return core.NewStreamWithDuration()
}

// NewStreamWithDurationWithClose creates a stream buffer that tracks duration and calls
// onClose when the consumer closes the stream before it completes.
func NewStreamWithDurationWithClose(onClose func()) *StreamWithDuration {
	return core.NewStreamWithDurationWithClose(onClose)
}

// NewBoundedStreamWithDuration is intended for provider adapters that produce in a goroutine.
// The framework drains Chunks while accepting evidence. Cancellation releases a blocked Send;
// Response still waits for the adapter to finish and report its partial output and usage.
func NewBoundedStreamWithDuration(
	ctx context.Context, capacity int, onClose func(),
) (*StreamWithDuration, error) {
	return core.NewBoundedStreamWithDuration(ctx, capacity, onClose)
}
