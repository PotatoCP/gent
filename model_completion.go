package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

type ModelStatus = core.ModelStatus

// ModelStatusAnswer identifies a complete candidate that the Loop can validate as output.
const ModelStatusAnswer ModelStatus = core.ModelStatusAnswer

// ModelStatusToolCalls identifies complete native calls that the Loop can prepare for execution.
const ModelStatusToolCalls ModelStatus = core.ModelStatusToolCalls

// ModelStatusContinue requires another model call with the returned continuation context.
const ModelStatusContinue ModelStatus = core.ModelStatusContinue

// ModelStatusIncomplete means generation stopped before a usable answer or call was complete.
const ModelStatusIncomplete ModelStatus = core.ModelStatusIncomplete

// ModelStatusRefused means the provider explicitly refused the request.
const ModelStatusRefused ModelStatus = core.ModelStatusRefused

// ModelStatusFailed means the provider returned a failed generation outcome.
const ModelStatusFailed ModelStatus = core.ModelStatusFailed

// ModelRefusal preserves only native refusal fields. Empty content is valid when the provider
// blocks a request without an explanation. Ordinary answer text never establishes refusal.
type ModelRefusal = core.ModelRefusal

// ModelCompletion describes the provider response. Gent accepts an answer only after Output
// validation and a terminal checkpoint; provider completion alone cannot establish that success.
type ModelCompletion = core.ModelCompletion
