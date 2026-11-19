package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// ExecutionStats stores cumulative counters, accepted turn baselines, and current-turn gauges.
// Counter changes are monotonic. NewTurn resets gauges and records new baselines; Continue and
// Restore preserve them. Updates check configured limits. Methods are safe for concurrent use.
type ExecutionStats = core.ExecutionStats

// NewExecutionStats creates a new ExecutionStats instance without
// context association. Use this for standalone stats that don't need
// limit checking.
func NewExecutionStats() *ExecutionStats {
	return core.NewExecutionStats()
}
