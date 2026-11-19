package gent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPublishBeforeExecution_SetsCorrectEventName(t *testing.T) {
	execCtx := newComponentContext(t, &Config{})

	earliest := time.Now()
	event := execCtx.PublishBeforeExecution()

	assert.Equal(t, EventNameExecutionBefore, event.EventName)
	assert.GreaterOrEqual(t, event.Timestamp.UnixNano(), earliest.UnixNano())
	assert.Equal(t, 0, event.Iteration)
}

func TestPublishAfterExecution_SetsCorrectEventName(t *testing.T) {
	execCtx := newComponentContext(t, &Config{})

	event := execCtx.PublishAfterExecution(TerminationSuccess, nil)

	assert.Equal(t, EventNameExecutionAfter, event.EventName)
	assert.Equal(t, TerminationSuccess, event.TerminationReason)
	assert.Nil(t, event.Error)
}

func TestPublishBeforeIteration_SetsCorrectEventName(t *testing.T) {
	execCtx := newComponentContext(t, &Config{})
	execCtx.IncrementIteration()

	event := execCtx.PublishBeforeIteration()

	assert.Equal(t, EventNameIterationBefore, event.EventName)
	assert.Equal(t, 1, event.Iteration)
}

func TestPublishAfterIteration_SetsCorrectEventName(t *testing.T) {
	execCtx := newComponentContext(t, &Config{})
	execCtx.IncrementIteration()
	result := &AgentLoopResult{Action: LAContinue}

	event := execCtx.PublishAfterIteration(result, 100*time.Millisecond)

	assert.Equal(t, EventNameIterationAfter, event.EventName)
	assert.Equal(t, result, event.Result)
	assert.Equal(t, 100*time.Millisecond, event.Duration)
}

func TestPublishBeforeModelCall_SetsCorrectEventName(t *testing.T) {
	execCtx := newComponentContext(t, &Config{})

	event := execCtx.PublishBeforeModelCall("gpt-4", &ModelCallRequest{})

	assert.Equal(t, EventNameModelCallBefore, event.EventName)
	assert.Equal(t, "gpt-4", event.Model)
}

func TestPublishAfterModelCall_SetsCorrectEventName(t *testing.T) {
	execCtx := newComponentContext(t, &Config{})
	response := &ModelCallResponse{
		Info: &GenerationInfo{
			InputTokens:  100,
			OutputTokens: 50,
		},
	}

	event := execCtx.PublishAfterModelCall(
		"gpt-4", &ModelCallRequest{}, response, 500*time.Millisecond, nil,
	)

	assert.Equal(t, EventNameModelCallAfter, event.EventName)
	assert.Equal(t, "gpt-4", event.Model)
	assert.Equal(t, 100, event.InputTokens)
	assert.Equal(t, 50, event.OutputTokens)
	assert.Equal(t, 500*time.Millisecond, event.Duration)
}

func TestPublishBeforeToolCall_SetsCorrectEventName(t *testing.T) {
	execCtx := newComponentContext(t, &Config{})
	args := map[string]any{"query": "test"}

	event := execCtx.PublishBeforeToolCall("search", args)

	assert.Equal(t, EventNameToolCallBefore, event.EventName)
	assert.Equal(t, "search", event.ToolName)
	assert.Equal(t, args, event.Args)
}

func TestPublishAfterToolCall_SetsCorrectEventName(t *testing.T) {
	execCtx := newComponentContext(t, &Config{})
	args := map[string]any{"query": "test"}
	output := "search results"

	event := execCtx.PublishAfterToolCall("search", args, output, 200*time.Millisecond, nil)

	assert.Equal(t, EventNameToolCallAfter, event.EventName)
	assert.Equal(t, "search", event.ToolName)
	assert.Equal(t, args, event.Args)
	assert.Equal(t, output, event.Output)
	assert.Equal(t, 200*time.Millisecond, event.Duration)
}

func TestPublishParseError_SetsCorrectEventName(t *testing.T) {
	execCtx := newComponentContext(t, &Config{})
	execCtx.IncrementIteration()

	event := execCtx.PublishParseError(ParseErrorTypeToolchain, "invalid content", assert.AnError)

	assert.Equal(t, EventNameParseError, event.EventName)
	assert.Equal(t, ParseErrorTypeToolchain, event.ErrorType)
	assert.Equal(t, "invalid content", event.RawContent)
	assert.Equal(t, assert.AnError, event.Error)
}

func TestPublishError_SetsCorrectEventName(t *testing.T) {
	execCtx := newComponentContext(t, &Config{})

	event := execCtx.PublishError(assert.AnError)

	assert.Equal(t, EventNameError, event.EventName)
	assert.Equal(t, assert.AnError, event.Error)
}

func TestPublishCommonEvent_SetsCustomEventName(t *testing.T) {
	execCtx := newComponentContext(t, &Config{})
	data := map[string]any{"key": "value"}

	event := execCtx.PublishCommonEvent("myapp:custom_event", "Something happened", data)

	assert.Equal(t, "myapp:custom_event", event.EventName)
	assert.Equal(t, "Something happened", event.Description)
	assert.Equal(t, data, event.Data)
}

func TestPublishLimitExceeded_SetsCorrectEventName(t *testing.T) {
	execCtx := newComponentContext(t, &Config{})
	execCtx.IncrementIteration()
	limit := Limit{
		Type:     LimitExactKey,
		Key:      SCIterations,
		MaxValue: 5,
	}

	earliest := time.Now()
	event := execCtx.PublishLimitExceeded(limit, 6.0, SCIterations)

	assert.Equal(t, EventNameLimitExceeded, event.EventName)
	assert.Equal(t, limit, event.Limit)
	assert.Equal(t, 6.0, event.CurrentValue)
	assert.Equal(t, SCIterations, event.MatchedKey)
	assert.Equal(t, 1, event.Iteration)
	assert.GreaterOrEqual(t, event.Timestamp.UnixNano(), earliest.UnixNano())
}

func TestPublishLimitExceeded_PrefixLimitMatchedKey(t *testing.T) {
	execCtx := newComponentContext(t, &Config{})
	limit := Limit{
		Type:     LimitKeyPrefix,
		Key:      SCInputTokensFor,
		MaxValue: 1000,
	}

	event := execCtx.PublishLimitExceeded(limit, 1500.0, SCInputTokensFor+"gpt-4")

	assert.Equal(t, EventNameLimitExceeded, event.EventName)
	assert.Equal(t, SCInputTokensFor, event.Limit.Key)
	assert.Equal(t, 1500.0, event.CurrentValue)
	assert.Equal(t, SCInputTokensFor+"gpt-4", event.MatchedKey)
}

func TestBaseEvent_TimestampPopulated(t *testing.T) {
	execCtx := newComponentContext(t, &Config{})
	before := time.Now()

	event := execCtx.PublishBeforeExecution()

	after := time.Now()
	assert.True(t, event.Timestamp.After(before) || event.Timestamp.Equal(before))
	assert.True(t, event.Timestamp.Before(after) || event.Timestamp.Equal(after))
}

func TestBaseEvent_IterationPopulated(t *testing.T) {
	execCtx := newComponentContext(t, &Config{})

	// Before first iteration
	event1 := execCtx.PublishBeforeExecution()
	assert.Equal(t, 0, event1.Iteration)

	// After incrementing iteration
	execCtx.IncrementIteration()
	event2 := execCtx.PublishBeforeIteration()
	assert.Equal(t, 1, event2.Iteration)

	execCtx.IncrementIteration()
	event3 := execCtx.PublishBeforeIteration()
	assert.Equal(t, 2, event3.Iteration)
}

func receiveSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("expected signal")
	}
}

func receiveBeforeModelCallEvent(
	t *testing.T,
	ch <-chan *BeforeModelCallEvent,
) *BeforeModelCallEvent {
	t.Helper()
	select {
	case event := <-ch:
		return event
	case <-time.After(time.Second):
		t.Fatal("expected before model call event")
		return nil
	}
}

func modelRequestTexts(request *ModelCallRequest) []string {
	texts := make([]string, 0)
	for _, message := range request.Messages {
		for _, part := range message.Parts {
			text, ok := part.(TextContent)
			if !ok {
				continue
			}
			texts = append(texts, text.Text)
		}
	}
	return texts
}

func TestIncrCounter_NegativeDeltaPanics(t *testing.T) {
	stats := NewExecutionStats()
	assert.Panics(t, func() {
		stats.IncrCounter(StatKey("test:counter"), -1)
	}, "negative delta should panic")
}

func TestIncrCounter_ProtectedKeyIgnored(t *testing.T) {
	stats := NewExecutionStats()
	stats.IncrCounter(SCIterations, 1)
	assert.Equal(t, int64(0),
		stats.GetCounter(SCIterations),
		"protected key should be silently ignored")
}

func TestGauge_ScratchpadLength_SetOnScratchPadChange(
	t *testing.T,
) {
	execCtx := newComponentContext(t, &Config{})

	// Empty scratchpad → gauge 0
	setTestRecords(t, execCtx, []*HistoryEntry{})
	assert.Equal(t, float64(0),
		execCtx.Stats().GetGauge(SGScratchpadLength),
		"empty scratchpad should set gauge to 0")

	// 1 iteration → gauge 1
	setTestRecords(t, execCtx, []*HistoryEntry{
		{Messages: []*MessageContent{}},
	})
	assert.Equal(t, float64(1),
		execCtx.Stats().GetGauge(SGScratchpadLength),
		"1-element scratchpad should set gauge to 1")

	// 3 iterations → gauge 3
	setTestRecords(t, execCtx, []*HistoryEntry{
		{Messages: []*MessageContent{}},
		{Messages: []*MessageContent{}},
		{Messages: []*MessageContent{}},
	})
	assert.Equal(t, float64(3),
		execCtx.Stats().GetGauge(SGScratchpadLength),
		"3-element scratchpad should set gauge to 3")

	// Compaction back to 1 → gauge 1
	setTestRecords(t, execCtx, []*HistoryEntry{
		{Messages: []*MessageContent{}},
	})
	assert.Equal(t, float64(1),
		execCtx.Stats().GetGauge(SGScratchpadLength),
		"compacted scratchpad should set gauge to 1")
}

func TestGauge_LastIterationTokens_SetOnModelCall(
	t *testing.T,
) {
	execCtx := newComponentContext(t, &Config{})

	// First model call: 100 input, 50 output
	execCtx.PublishAfterModelCall(
		"gpt-4", &ModelCallRequest{},
		&ModelCallResponse{
			Info: &GenerationInfo{
				InputTokens:  100,
				OutputTokens: 50,
			},
		},
		0, nil,
	)

	assert.Equal(t, float64(100),
		execCtx.Stats().GetGauge(SGInputTokensLastIteration),
		"input gauge should be 100")
	assert.Equal(t, float64(50),
		execCtx.Stats().GetGauge(SGOutputTokensLastIteration),
		"output gauge should be 50")
	assert.Equal(t, float64(150),
		execCtx.Stats().GetGauge(SGTotalTokensLastIteration),
		"total gauge should be 150")
	assert.Equal(t, float64(100),
		execCtx.Stats().GetGauge(
			SGInputTokensLastIterationFor+"gpt-4",
		),
		"per-model input gauge should be 100")
	assert.Equal(t, float64(50),
		execCtx.Stats().GetGauge(
			SGOutputTokensLastIterationFor+"gpt-4",
		),
		"per-model output gauge should be 50")
	assert.Equal(t, float64(150),
		execCtx.Stats().GetGauge(
			SGTotalTokensLastIterationFor+"gpt-4",
		),
		"per-model total gauge should be 150")

	// Second model call (different model): 200 input, 100 output
	// Gauges should accumulate within the same iteration
	execCtx.PublishAfterModelCall(
		"claude", &ModelCallRequest{},
		&ModelCallResponse{
			Info: &GenerationInfo{
				InputTokens:  200,
				OutputTokens: 100,
			},
		},
		0, nil,
	)

	assert.Equal(t, float64(300),
		execCtx.Stats().GetGauge(SGInputTokensLastIteration),
		"input gauge should accumulate to 300")
	assert.Equal(t, float64(150),
		execCtx.Stats().GetGauge(SGOutputTokensLastIteration),
		"output gauge should accumulate to 150")
	assert.Equal(t, float64(450),
		execCtx.Stats().GetGauge(SGTotalTokensLastIteration),
		"total gauge should accumulate to 450")
	// Per-model: gpt-4 unchanged
	assert.Equal(t, float64(100),
		execCtx.Stats().GetGauge(
			SGInputTokensLastIterationFor+"gpt-4",
		),
		"gpt-4 input gauge should remain 100")
	// Per-model: claude has its own values
	assert.Equal(t, float64(200),
		execCtx.Stats().GetGauge(
			SGInputTokensLastIterationFor+"claude",
		),
		"claude input gauge should be 200")
	assert.Equal(t, float64(100),
		execCtx.Stats().GetGauge(
			SGOutputTokensLastIterationFor+"claude",
		),
		"claude output gauge should be 100")
	assert.Equal(t, float64(300),
		execCtx.Stats().GetGauge(
			SGTotalTokensLastIterationFor+"claude",
		),
		"claude total gauge should be 300")
}

func TestGauge_LastIterationTokens_ResetOnNewIteration(
	t *testing.T,
) {
	execCtx := newComponentContext(t, &Config{})

	// Simulate iteration 1: model call sets gauges
	execCtx.IncrementIteration()
	execCtx.PublishBeforeIteration()
	execCtx.PublishAfterModelCall(
		"gpt-4", &ModelCallRequest{},
		&ModelCallResponse{
			Info: &GenerationInfo{
				InputTokens:  100,
				OutputTokens: 50,
			},
		},
		0, nil,
	)

	// Verify gauges are set
	assert.Equal(t, float64(100), execCtx.Stats().GetGauge(SGInputTokensLastIteration))
	assert.Equal(t, float64(50), execCtx.Stats().GetGauge(SGOutputTokensLastIteration))
	assert.Equal(t, float64(150), execCtx.Stats().GetGauge(SGTotalTokensLastIteration))
	assert.Equal(t, float64(100),
		execCtx.Stats().GetGauge(
			SGInputTokensLastIterationFor+"gpt-4",
		))
	assert.Equal(t, float64(50),
		execCtx.Stats().GetGauge(
			SGOutputTokensLastIterationFor+"gpt-4",
		))
	assert.Equal(t, float64(150),
		execCtx.Stats().GetGauge(
			SGTotalTokensLastIterationFor+"gpt-4",
		))

	// Simulate iteration 2 start: BeforeIterationEvent resets
	execCtx.IncrementIteration()
	execCtx.PublishBeforeIteration()

	// All gauges should be reset to 0
	assert.Equal(t, float64(0),
		execCtx.Stats().GetGauge(SGInputTokensLastIteration),
		"input gauge should reset to 0")
	assert.Equal(t, float64(0),
		execCtx.Stats().GetGauge(SGOutputTokensLastIteration),
		"output gauge should reset to 0")
	assert.Equal(t, float64(0),
		execCtx.Stats().GetGauge(SGTotalTokensLastIteration),
		"total gauge should reset to 0")
	assert.Equal(t, float64(0),
		execCtx.Stats().GetGauge(
			SGInputTokensLastIterationFor+"gpt-4",
		),
		"per-model input gauge should reset to 0")
	assert.Equal(t, float64(0),
		execCtx.Stats().GetGauge(
			SGOutputTokensLastIterationFor+"gpt-4",
		),
		"per-model output gauge should reset to 0")
	assert.Equal(t, float64(0),
		execCtx.Stats().GetGauge(
			SGTotalTokensLastIterationFor+"gpt-4",
		),
		"per-model total gauge should reset to 0")
}

func TestResetGaugesByPrefix(t *testing.T) {
	stats := NewExecutionStats()

	// Set multiple gauges with same prefix
	stats.IncrGauge(
		SGTotalTokensLastIterationFor+"gpt-4", 100,
	)
	stats.IncrGauge(
		SGTotalTokensLastIterationFor+"claude", 200,
	)
	// Set a gauge with different prefix (should not be reset)
	stats.IncrGauge(
		SGToolCallsErrorConsecutive, 5,
	)

	// Reset only total tokens last iteration "For" gauges
	stats.resetGaugesByPrefix(
		SGTotalTokensLastIterationFor,
	)

	// Matching gauges should be reset to 0
	assert.Equal(t, float64(0),
		stats.GetGauge(
			SGTotalTokensLastIterationFor+"gpt-4",
		),
		"gpt-4 gauge should be reset to 0")
	assert.Equal(t, float64(0),
		stats.GetGauge(
			SGTotalTokensLastIterationFor+"claude",
		),
		"claude gauge should be reset to 0")

	// Non-matching gauge should be untouched
	assert.Equal(t, float64(5),
		stats.GetGauge(SGToolCallsErrorConsecutive),
		"unrelated gauge should remain 5")
}

func TestDefaultLimits_UsesTurnIterations(t *testing.T) {
	limits := DefaultLimits()
	found := false
	for _, l := range limits {
		if l.Key == SCIterations {
			found = true
			assert.Equal(t, float64(100), l.MaxValue)
		}
	}
	assert.True(t, found, "DefaultLimits should have SCIterations")
}
