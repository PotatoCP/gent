package gent

import (
	"maps"
	"sync"
)

// ExecutionStats stores cumulative counters, accepted turn baselines, and current-turn gauges.
// Counter changes are monotonic. NewTurn resets gauges and records new baselines; Continue and
// Restore preserve them. Updates check configured limits. Methods are safe for concurrent use.
type ExecutionStats struct {
	mu            sync.RWMutex
	counters      map[string]int64
	turnBaselines map[string]int64
	gauges        map[string]float64
	execCtx       *ExecutionContext // back-ref for limit checking
}

// NewExecutionStats creates a new ExecutionStats instance without
// context association. Use this for standalone stats that don't need
// limit checking.
func NewExecutionStats() *ExecutionStats {
	return &ExecutionStats{
		counters: make(map[string]int64),
		gauges:   make(map[string]float64),
	}
}

// newExecutionStatsWithContext creates a new ExecutionStats with a
// back-reference to the ExecutionContext for limit checking.
func newExecutionStatsWithContext(
	execCtx *ExecutionContext,
) *ExecutionStats {
	return &ExecutionStats{
		counters: make(map[string]int64),
		gauges:   make(map[string]float64),
		execCtx:  execCtx,
	}
}

// IncrCounter adds a nonnegative delta to a cumulative run counter. A new key starts at zero.
// NewTurn records baselines instead of reducing these values. Negative deltas panic.
// Protected keys such as SCIterations are ignored; only the Runner changes them.
func (s *ExecutionStats) IncrCounter(key StatKey, delta int64) {
	if delta < 0 {
		panic("gent: IncrCounter called with negative delta")
	}

	if isProtectedKey(key) {
		return // Silently ignore protected keys
	}
	s.incrCounterDirect(key, delta)
}

// incrCounterDirect updates a run counter and checks limits. Framework events can use this
// for protected keys; the public IncrCounter method cannot change those keys.
func (s *ExecutionStats) incrCounterDirect(
	key StatKey,
	delta int64,
) {
	sKey := string(key)

	s.mu.Lock()
	s.counters[sKey] += delta
	s.mu.Unlock()

	// Check limits on this context
	if s.execCtx != nil {
		s.execCtx.checkLimits()
	}

}

// GetCounter returns the current value of a counter, or 0 if not set.
func (s *ExecutionStats) GetCounter(key StatKey) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.counters[string(key)]
}

// GetTurnCounter returns usage since the accepted start of the current turn. Continue and
// Restore preserve that baseline. Active reads include provisional updates; saved replay restores
// only accepted usage. A new turn does not reduce GetCounter's cumulative value.
func (s *ExecutionStats) GetTurnCounter(key StatKey) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.counters[string(key)] - s.turnBaselines[string(key)]
}

func (s *ExecutionStats) resetTurn() {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Baselines must remain independent of later cumulative updates. Gauges all have turn scope.
	s.turnBaselines = maps.Clone(s.counters)
	clear(s.gauges)
}

func (s *ExecutionStats) savedTurnBaselines() map[string]int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return maps.Clone(s.turnBaselines)
}

func (s *ExecutionStats) limitCounter(key StatKey, scope LimitScope) int64 {
	if scope == LimitRun {
		return s.GetCounter(key)
	}
	return s.GetTurnCounter(key)
}

// IncrGauge increments a gauge by delta (positive or negative).
// Creates the gauge if it doesn't exist.
//
// Gauges keep the current turn reading regardless of Limit.Scope. They are local to the
// execution context that modifies them.
func (s *ExecutionStats) IncrGauge(key StatKey, delta float64) {
	s.mu.Lock()
	s.gauges[string(key)] += delta
	s.mu.Unlock()

	// Check limits on this context
	if s.execCtx != nil {
		s.execCtx.checkLimits()
	}
}

// incrGaugeInternal increments a gauge without any public API checks.
// Used internally by updateStatsForEvent for framework gauge updates.
func (s *ExecutionStats) incrGaugeInternal(
	key StatKey,
	delta float64,
) {
	s.mu.Lock()
	s.gauges[string(key)] += delta
	s.mu.Unlock()

	// Check limits on this context
	if s.execCtx != nil {
		s.execCtx.checkLimits()
	}
}

// SetGauge sets a gauge to a specific value.
// Gauges keep the current turn reading regardless of Limit.Scope.
func (s *ExecutionStats) SetGauge(key StatKey, value float64) {
	s.mu.Lock()
	s.gauges[string(key)] = value
	s.mu.Unlock()

	// Check limits on this context
	if s.execCtx != nil {
		s.execCtx.checkLimits()
	}
}

// GetGauge returns the current value of a gauge, or 0.0 if not set.
func (s *ExecutionStats) GetGauge(key StatKey) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.gauges[string(key)]
}

// ResetGauge sets a gauge to 0.0.
func (s *ExecutionStats) ResetGauge(key StatKey) {
	s.mu.Lock()
	s.gauges[string(key)] = 0
	s.mu.Unlock()
}

// resetGaugesByPrefix resets all gauges matching the given prefix
// to 0. Used internally for resetting per-iteration gauges at
// iteration boundaries.
func (s *ExecutionStats) resetGaugesByPrefix(
	prefix StatKey,
) {
	prefixStr := string(prefix)
	s.mu.Lock()
	for key := range s.gauges {
		if len(key) >= len(prefixStr) &&
			key[:len(prefixStr)] == prefixStr {
			s.gauges[key] = 0
		}
	}
	s.mu.Unlock()
}

// Counters returns an independent measurement of cumulative run counters. Copying the map
// keeps it stable while concurrent operations change the live measurements.
func (s *ExecutionStats) Counters() map[string]int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]int64, len(s.counters))
	for k, v := range s.counters {
		result[k] = v
	}
	return result
}

// Gauges returns an independent measurement that stays stable during later gauge updates.
func (s *ExecutionStats) Gauges() map[string]float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]float64, len(s.gauges))
	for k, v := range s.gauges {
		result[k] = v
	}
	return result
}

// GetTotalInputTokens returns the total input tokens across all
// models and turns in this run.
func (s *ExecutionStats) GetTotalInputTokens() int64 {
	return s.GetCounter(SCInputTokens)
}

// GetTotalOutputTokens returns the total output tokens across all
// models and turns in this run.
func (s *ExecutionStats) GetTotalOutputTokens() int64 {
	return s.GetCounter(SCOutputTokens)
}

// GetTotalTokens returns the total tokens (input + output) across
// all models and turns in this run.
func (s *ExecutionStats) GetTotalTokens() int64 {
	return s.GetCounter(SCTotalTokens)
}

// GetToolCallCount returns the total number of tool calls
// across all turns in this run.
func (s *ExecutionStats) GetToolCallCount() int64 {
	return s.GetCounter(SCToolCalls)
}

// GetIterations returns the cumulative iteration count across all turns in this run.
func (s *ExecutionStats) GetIterations() int64 {
	return s.GetCounter(SCIterations)
}
