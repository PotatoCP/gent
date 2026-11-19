package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// PrepareModelRequest resolves a local tool catalog for adapters that use SharedLocalSearch.
// A nil chain leaves explicitly supplied definitions in place. It does not change request.
// Definitions remain structured until native encoding. Hooks run at the managed boundary.
func PrepareModelRequest(execCtx *ExecutionContext, request *ModelCallRequest, chain ToolChain) (
	*ModelCallRequest, error,
) {
	return core.PrepareModelRequest(execCtx, request, chain)
}
