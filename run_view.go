package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// NewRunView creates an empty archive replay target with Gent's format metadata. An empty run ID
// is allowed at construction and rejected by Apply. The ID is never inferred from a batch.
// RunView retains the full archive; use RunState for bounded execution restoration.
func NewRunView(runId string) *RunView {
	return core.NewRunView(runId)
}
