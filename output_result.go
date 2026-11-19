package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// UsefulSteering checks for useful user-message content. Tool protocol parts cannot serve as
// correction instructions. Media can supply a reference example without being converted to text.
func UsefulSteering(parts []ContentPart) bool {
	return core.UsefulSteering(parts)
}

// AcceptOutput freezes a validated typed value and its delivery content into one loop result.
// Callers do not decode or assert T. The runner saves this result before it returns T to its
// caller.
// Result codecs must be registered during setup, before model calls or application effects.
func AcceptOutput[T any](
	execCtx *ExecutionContext,
	result *OutputResult[T],
	source *ContentSource,
) (
	*AgentLoopResult,
	error,
) {
	return core.AcceptOutput[T](execCtx, result, source)
}

// OutputValue decodes a saved successful result directly as T. It does not call the processor or
// validator. A nil pointer is a valid T; a missing typed value is a different, invalid saved state.
func OutputValue[T any](execCtx *ExecutionContext) (T, error) {
	return core.OutputValue[T](execCtx)
}
