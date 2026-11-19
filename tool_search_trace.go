package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// ToolSearchTrace is private diagnostic evidence for local searches used by the next request.
// It is never model content. An unsaved search trace can be lost on interruption; the local
// catalog read can be repeated without a separate search recovery state machine.
type ToolSearchTrace = core.ToolSearchTrace
