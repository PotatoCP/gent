package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// PrepareOutput registers the result type and validates output controls before model work.
// Configuration does not process output or bind a saved run to a Runner version. Restore checks
// that saved values can be decoded with the current type and registered codecs.
func PrepareOutput[T any](execCtx *ExecutionContext, output Output[T]) error {
	return core.PrepareOutput[T](execCtx, output)
}
