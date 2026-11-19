package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// StreamAccumulator joins deltas by logical item identity. It observes provider progress; only an
// accepted_answer RecordedEvent establishes acceptance. Response cannot replace native continuation
// from Stream.Response. A snapshot owns its mutable values so later deltas cannot change it.
type StreamAccumulator = core.StreamAccumulator

func NewStreamAccumulator() *StreamAccumulator {
	return core.NewStreamAccumulator()
}
