package gent

// LimitType specifies how to match keys for limit checking.
type LimitType string

const (
	// LimitExactKey matches an exact key.
	// Use for specific stats like SCIterations or SCInputTokens.
	LimitExactKey LimitType = "exact"

	// LimitKeyPrefix matches any key with the given prefix.
	// Use for limits across all models/tools (e.g., SCToolCallsFor
	// matches all tool calls).
	LimitKeyPrefix LimitType = "prefix"
)

// LimitScope selects the counter lifetime used by a limit.
type LimitScope string

const (
	// LimitTurn measures counters in the current turn across all continued attempts.
	LimitTurn LimitScope = "turn"

	// LimitRun measures counters across all turns of the run.
	LimitRun LimitScope = "run"
)

// Limit defines a threshold that pauses execution. Exact iteration limits use >= at completed
// iteration boundaries. Other counter and gauge limits use > on each statistics update.
// A changed configuration cannot reset accepted usage. Continue checks saved usage before work.
type Limit struct {
	Type     LimitType
	Key      StatKey
	MaxValue float64
	// Scope defaults to LimitTurn. It changes counter measurement only. All gauges measure
	// the current turn, including under LimitRun; there are no cumulative gauge totals.
	Scope LimitScope
}

// DefaultLimits returns an independent standard preset for explicit caller configuration.
// Gent applies no execution limits for nil or empty Config.Limits. A supplied slice replaces
// the complete set; edit a preset rule to change it, or append a rule to add another condition.
// Recording and Subscriber bounds remain active even when no execution limit is configured.
func DefaultLimits() []Limit {
	return []Limit{
		// Stop after 100 iterations (per-context)
		{
			Type:     LimitExactKey,
			Key:      SCIterations,
			MaxValue: 100,
		},

		// Stop after 3 consecutive output rejections (gauge)
		{
			Type:     LimitExactKey,
			Key:      SGOutputRejectedConsecutive,
			MaxValue: 3,
		},

		// Stop after 3 consecutive toolchain parse errors (gauge)
		{
			Type:     LimitExactKey,
			Key:      SGToolchainParseErrorConsecutive,
			MaxValue: 3,
		},

		// Stop after 3 consecutive tool call errors (gauge)
		{
			Type:     LimitExactKey,
			Key:      SGToolCallsErrorConsecutive,
			MaxValue: 3,
		},

		// Stop after 3 consecutive code execution errors
		{
			Type:     LimitExactKey,
			Key:      SGCodeExecutionsErrorConsecutive,
			MaxValue: 3,
		},

		// Stop after 10 total output rejections
		{
			Type:     LimitExactKey,
			Key:      SCOutputRejectedTotal,
			MaxValue: 10,
		},
	}
}
