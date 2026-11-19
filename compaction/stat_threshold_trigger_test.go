package compaction

import (
	"github.com/rickchristie/gent/internal/tt"

	"testing"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

// newTestExecCtx creates an ExecutionContext with no limits
// for trigger testing.
func newTestExecCtx(t *testing.T) *gent.ExecutionContext {
	execCtx := tt.NewContext(t, &gent.Config{})
	return execCtx
}

func TestStatThresholdTrigger_Counter(t *testing.T) {
	type input struct {
		trigger    *StatThresholdTrigger
		setupStats func(execCtx *gent.ExecutionContext)
		preCompact bool // call NotifyCompacted before check
		postSetup  func(execCtx *gent.ExecutionContext)
	}

	type expected struct {
		shouldCompact bool
	}

	tests := []struct {
		name     string
		input    input
		expected expected
	}{
		{
			name: "counter below delta returns false",
			input: input{
				trigger: NewStatThresholdTrigger().OnCounter("myapp:calls", 10),
				setupStats: func(
					execCtx *gent.ExecutionContext,
				) {
					execCtx.Stats().IncrCounter(
						"myapp:calls", 5,
					)
				},
			},
			expected: expected{shouldCompact: false},
		},
		{
			name: "counter equals delta returns true",
			input: input{
				trigger: NewStatThresholdTrigger().OnCounter("myapp:calls", 10),
				setupStats: func(
					execCtx *gent.ExecutionContext,
				) {
					execCtx.Stats().IncrCounter(
						"myapp:calls", 10,
					)
				},
			},
			expected: expected{shouldCompact: true},
		},
		{
			name: "counter exceeds delta returns true",
			input: input{
				trigger: NewStatThresholdTrigger().OnCounter("myapp:calls", 10),
				setupStats: func(
					execCtx *gent.ExecutionContext,
				) {
					execCtx.Stats().IncrCounter(
						"myapp:calls", 15,
					)
				},
			},
			expected: expected{shouldCompact: true},
		},
		{
			name: "counter delta after compaction resets " +
				"snapshot",
			input: input{
				trigger: NewStatThresholdTrigger().OnCounter("myapp:calls", 10),
				setupStats: func(
					execCtx *gent.ExecutionContext,
				) {
					// Bring to 10 (triggers)
					execCtx.Stats().IncrCounter(
						"myapp:calls", 10,
					)
				},
				preCompact: true,
				postSetup: func(
					execCtx *gent.ExecutionContext,
				) {
					// Only 5 more after compaction
					execCtx.Stats().IncrCounter(
						"myapp:calls", 5,
					)
				},
			},
			expected: expected{shouldCompact: false},
		},
		{
			name: "counter delta after compaction " +
				"re-triggers at full delta",
			input: input{
				trigger: NewStatThresholdTrigger().OnCounter("myapp:calls", 10),
				setupStats: func(
					execCtx *gent.ExecutionContext,
				) {
					execCtx.Stats().IncrCounter(
						"myapp:calls", 10,
					)
				},
				preCompact: true,
				postSetup: func(
					execCtx *gent.ExecutionContext,
				) {
					// Another full delta after snapshot
					execCtx.Stats().IncrCounter(
						"myapp:calls", 10,
					)
				},
			},
			expected: expected{shouldCompact: true},
		},
		{
			name: "multiple counter thresholds one fires",
			input: input{
				trigger: NewStatThresholdTrigger().OnCounter("myapp:a", 10).OnCounter("myapp:b", 100),
				setupStats: func(
					execCtx *gent.ExecutionContext,
				) {
					execCtx.Stats().IncrCounter("myapp:a", 5)
					execCtx.Stats().IncrCounter(
						"myapp:b", 100,
					)
				},
			},
			expected: expected{shouldCompact: true},
		},
		{
			name: "multiple counter thresholds none fire",
			input: input{
				trigger: NewStatThresholdTrigger().OnCounter("myapp:a", 10).OnCounter("myapp:b", 100),
				setupStats: func(
					execCtx *gent.ExecutionContext,
				) {
					execCtx.Stats().IncrCounter("myapp:a", 5)
					execCtx.Stats().IncrCounter(
						"myapp:b", 50,
					)
				},
			},
			expected: expected{shouldCompact: false},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			execCtx := newTestExecCtx(t)
			tc.input.setupStats(execCtx)

			if tc.input.preCompact {
				tc.input.trigger.NotifyCompacted(execCtx)
			}
			if tc.input.postSetup != nil {
				tc.input.postSetup(execCtx)
			}

			result, err := tc.input.trigger.ShouldCompact(execCtx)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected.shouldCompact, result)
		})
	}
}

func TestStatThresholdTrigger_Gauge(t *testing.T) {
	type input struct {
		trigger    *StatThresholdTrigger
		setupStats func(execCtx *gent.ExecutionContext)
	}

	type expected struct {
		shouldCompact bool
	}

	tests := []struct {
		name     string
		input    input
		expected expected
	}{
		{
			name: "gauge below value returns false",
			input: input{
				trigger: NewStatThresholdTrigger().OnGauge("myapp:size", 20),
				setupStats: func(
					execCtx *gent.ExecutionContext,
				) {
					execCtx.Stats().SetGauge("myapp:size", 15)
				},
			},
			expected: expected{shouldCompact: false},
		},
		{
			name: "gauge equals value returns true",
			input: input{
				trigger: NewStatThresholdTrigger().OnGauge("myapp:size", 20),
				setupStats: func(
					execCtx *gent.ExecutionContext,
				) {
					execCtx.Stats().SetGauge("myapp:size", 20)
				},
			},
			expected: expected{shouldCompact: true},
		},
		{
			name: "gauge exceeds value returns true",
			input: input{
				trigger: NewStatThresholdTrigger().OnGauge("myapp:size", 20),
				setupStats: func(
					execCtx *gent.ExecutionContext,
				) {
					execCtx.Stats().SetGauge("myapp:size", 25)
				},
			},
			expected: expected{shouldCompact: true},
		},
		{
			name: "gauge decreased below threshold " +
				"returns false",
			input: input{
				trigger: NewStatThresholdTrigger().OnGauge("myapp:size", 20),
				setupStats: func(
					execCtx *gent.ExecutionContext,
				) {
					execCtx.Stats().SetGauge("myapp:size", 25)
					// Simulate compaction reducing it
					execCtx.Stats().SetGauge("myapp:size", 10)
				},
			},
			expected: expected{shouldCompact: false},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			execCtx := newTestExecCtx(t)
			tc.input.setupStats(execCtx)

			result, err := tc.input.trigger.ShouldCompact(execCtx)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected.shouldCompact, result)
		})
	}
}

func TestStatThresholdTrigger_Prefix(t *testing.T) {
	type input struct {
		trigger    *StatThresholdTrigger
		setupStats func(execCtx *gent.ExecutionContext)
	}

	type expected struct {
		shouldCompact bool
	}

	tests := []struct {
		name     string
		input    input
		expected expected
	}{
		{
			name: "counter prefix matches one key " +
				"returns true",
			input: input{
				trigger: NewStatThresholdTrigger().OnCounterPrefix("myapp:tok:", 1000),
				setupStats: func(
					execCtx *gent.ExecutionContext,
				) {
					execCtx.Stats().IncrCounter(
						"myapp:tok:gpt4", 500,
					)
					execCtx.Stats().IncrCounter(
						"myapp:tok:claude", 1000,
					)
				},
			},
			expected: expected{shouldCompact: true},
		},
		{
			name: "counter prefix matches no keys " +
				"returns false",
			input: input{
				trigger: NewStatThresholdTrigger().OnCounterPrefix("myapp:tok:", 1000),
				setupStats: func(
					execCtx *gent.ExecutionContext,
				) {
					execCtx.Stats().IncrCounter(
						"myapp:tok:gpt4", 500,
					)
					execCtx.Stats().IncrCounter(
						"myapp:tok:claude", 500,
					)
				},
			},
			expected: expected{shouldCompact: false},
		},
		{
			name: "gauge prefix matches one key " +
				"returns true",
			input: input{
				trigger: NewStatThresholdTrigger().OnGaugePrefix("myapp:err:", 3),
				setupStats: func(
					execCtx *gent.ExecutionContext,
				) {
					execCtx.Stats().SetGauge(
						"myapp:err:format", 1,
					)
					execCtx.Stats().SetGauge(
						"myapp:err:tool", 3,
					)
				},
			},
			expected: expected{shouldCompact: true},
		},
		{
			name: "gauge prefix matches no keys " +
				"returns false",
			input: input{
				trigger: NewStatThresholdTrigger().OnGaugePrefix("myapp:err:", 3),
				setupStats: func(
					execCtx *gent.ExecutionContext,
				) {
					execCtx.Stats().SetGauge(
						"myapp:err:format", 1,
					)
					execCtx.Stats().SetGauge(
						"myapp:err:tool", 2,
					)
				},
			},
			expected: expected{shouldCompact: false},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			execCtx := newTestExecCtx(t)
			tc.input.setupStats(execCtx)

			result, err := tc.input.trigger.ShouldCompact(execCtx)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected.shouldCompact, result)
		})
	}
}

func TestStatThresholdTrigger_NotifyCompacted(t *testing.T) {
	type input struct {
		trigger    *StatThresholdTrigger
		setupStats func(execCtx *gent.ExecutionContext)
	}

	type expected struct {
		lastValues map[string]int64
	}

	tests := []struct {
		name     string
		input    input
		expected expected
	}{
		{
			name: "exact counter snapshot updated",
			input: input{
				trigger: NewStatThresholdTrigger().OnCounter("myapp:calls", 10),
				setupStats: func(
					execCtx *gent.ExecutionContext,
				) {
					execCtx.Stats().IncrCounter(
						"myapp:calls", 15,
					)
				},
			},
			expected: expected{
				lastValues: map[string]int64{
					"myapp:calls": 15,
				},
			},
		},
		{
			name: "prefix counter snapshot updated " +
				"for all matching keys",
			input: input{
				trigger: NewStatThresholdTrigger().OnCounterPrefix("myapp:tok:", 1000),
				setupStats: func(
					execCtx *gent.ExecutionContext,
				) {
					execCtx.Stats().IncrCounter(
						"myapp:tok:gpt4", 500,
					)
					execCtx.Stats().IncrCounter(
						"myapp:tok:claude", 1200,
					)
				},
			},
			expected: expected{
				lastValues: map[string]int64{
					"myapp:tok:gpt4":   500,
					"myapp:tok:claude": 1200,
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			execCtx := newTestExecCtx(t)
			tc.input.setupStats(execCtx)

			tc.input.trigger.NotifyCompacted(execCtx)

			saved, ok, err := execCtx.CheckpointState("gent:compaction_baseline")
			assert.NoError(t, err)
			assert.True(t, ok)
			assert.Equal(t, tc.expected.lastValues, saved)
		})
	}
}

// TestStatThresholdTrigger_CombinedScenario tests the
// real-world example from the design doc: iteration + token
// triggers together over multiple compaction cycles.
func TestStatThresholdTrigger_CombinedScenario(t *testing.T) {
	trigger := NewStatThresholdTrigger().OnCounter("myapp:iterations", 10).
		OnCounter("myapp:input_tokens", 100000)

	execCtx := newTestExecCtx(t)

	// Iteration 5, 120K tokens → token fires
	execCtx.Stats().IncrCounter("myapp:iterations", 5)
	execCtx.Stats().IncrCounter("myapp:input_tokens", 120000)
	assert.True(t, testShouldCompact(t, trigger, execCtx), "should fire: 120K tokens >= 100K delta")
	trigger.NotifyCompacted(execCtx)

	// Iteration 10, 200K tokens → neither fires
	// delta iterations = 5 (10-5), delta tokens = 80K
	execCtx.Stats().IncrCounter("myapp:iterations", 5)
	execCtx.Stats().IncrCounter("myapp:input_tokens", 80000)
	assert.False(t, testShouldCompact(t, trigger, execCtx),
		"should not fire: delta 5 iter < 10, "+
			"delta 80K tokens < 100K")

	// Iteration 15, 250K tokens → iteration fires
	// delta iterations = 10 (15-5), delta tokens = 130K
	execCtx.Stats().IncrCounter("myapp:iterations", 5)
	execCtx.Stats().IncrCounter("myapp:input_tokens", 50000)
	assert.True(t, testShouldCompact(t, trigger, execCtx),
		"should fire: delta 10 iterations >= 10 delta")
	trigger.NotifyCompacted(execCtx)

	// After second compaction: both snapshots reset
	// delta iterations = 0, delta tokens = 0
	assert.False(t, testShouldCompact(t, trigger, execCtx),
		"should not fire: both deltas are 0 after "+
			"compaction")
}
