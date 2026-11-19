package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// ToolChain selects and executes native tool calls. Implementations can embed toolchain.Registry
// and override NativeTools to control visibility while retaining validation, hooks, and recovery.
// RegisterCodecs registers values needed to read saved tool state.
type ToolChain = core.ToolChain

// ParallelToolChain declares whether ExecuteNext can run concurrently with independent plans
// in the same context. Each plan must have a distinct checkpoint key. The JavaScript wrapper
// always calls ExecuteNext on the supplied chain, including overrides on an embedding type.
type ParallelToolChain = core.ParallelToolChain
