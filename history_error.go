package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// SavedError preserves error text and the public classifications used during continuation.
// Private implementation types must be rebound through an application codec when needed.
type SavedError = core.SavedError

// CodedError gives application errors a stable classification without serializing private fields.
// Use an application namespace and keep its meaning stable across recovery versions.
type CodedError = core.CodedError

var ErrRepetitionDetected = core.ErrRepetitionDetected

var ErrMaxResponseSize = core.ErrMaxResponseSize

var ErrReplayUnavailable = core.ErrReplayUnavailable

func SaveError(err error) *SavedError {
	return core.SaveError(err)
}
