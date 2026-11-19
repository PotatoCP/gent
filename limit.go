package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// LimitType specifies how to match keys for limit checking.
type LimitType = core.LimitType

// LimitExactKey matches an exact key.
// Use for specific stats like SCIterations or SCInputTokens.
const LimitExactKey LimitType = core.LimitExactKey

// LimitKeyPrefix matches any key with the given prefix.
// Use for limits across all models/tools (e.g., SCToolCallsFor
// matches all tool calls).
const LimitKeyPrefix LimitType = core.LimitKeyPrefix

// LimitScope selects the counter lifetime used by a limit.
type LimitScope = core.LimitScope

// LimitTurn measures counters in the current turn across all continued attempts.
const LimitTurn LimitScope = core.LimitTurn

// LimitRun measures counters across all turns of the run.
const LimitRun LimitScope = core.LimitRun

// Limit defines a threshold that pauses execution. Exact iteration limits use >= at completed
// iteration boundaries. Other counter and gauge limits use > on each statistics update.
// A changed configuration cannot reset accepted usage. Continue checks saved usage before work.
type Limit = core.Limit

// DefaultLimits returns an independent standard preset for explicit caller configuration.
// Gent applies no execution limits for nil or empty Config.Limits. A supplied slice replaces
// the complete set; edit a preset rule to change it, or append a rule to add another condition.
// Recording and Subscriber bounds remain active even when no execution limit is configured.
func DefaultLimits() []Limit {
	return core.DefaultLimits()
}
