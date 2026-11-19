package trace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSequencer_EventSequenceNumbersReplayAndJSONSafety(t *testing.T) {
	type expected struct {
		lastEventSequenceNumber uint64
		recentCount             int
		replayCount             int
		stitchStatus            StitchStatus
	}

	seq := newTestSequencer(t, "run", Config{MaxRecentEvents: 3})
	recordTestValue(t, seq, "common", &gent.CommonEvent{})
	recordTestValue(t, seq, "common", &gent.CommonEvent{})
	recordTestValue(t, seq, "common", &gent.CommonEvent{})
	recordTestValue(t, seq, "common", &gent.CommonEvent{})

	snapshot := seq.Snapshot()
	replay, err := seq.EventsAfter(2)
	require.NoError(t, err)
	assertJSONSafe(t, snapshot)
	assertJSONSafe(t, replay)
	assertEventSequenceNumbersContiguous(t, snapshot.RecentEvents)
	assert.Equal(t, expected{
		lastEventSequenceNumber: 4,
		recentCount:             3,
		replayCount:             2,
		stitchStatus:            StitchStatusOK,
	}, expected{
		lastEventSequenceNumber: snapshot.LastEventSequenceNumber,
		recentCount:             len(snapshot.RecentEvents),
		replayCount:             len(replay),
		stitchStatus:            ValidateStitch(2, replay).Status,
	})

	_, err = seq.EventsAfter(0)
	assert.ErrorIs(t, err, ErrReplayUnavailable)
}

func TestSequencer_SnapshotModelToolAndStreamCorrelation(t *testing.T) {
	seq := newTestSequencer(t, "", Config{
		IncludeChunkText:      true,
		IncludeModelRequests:  true,
		IncludeModelResponses: true,
		IncludeToolArgs:       true,
		IncludeToolOutput:     true,
	})
	loop := &traceTestLoop{next: func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
		request := gent.NewModelCallRequest([]*gent.ModelMessage{
			gent.TextParts(gent.ChatMessageTypeHuman, "hello")},
			gent.WithTemperature(0.2), gent.WithMaxTokens(123))
		model := &traceModel{chunks: []gent.StreamChunk{
			{Content: []gent.ContentPart{&gent.AssistantContent{Type: gent.AssistantContentIntermediary,
				MessageId: "message", Content: []gent.ContentPart{gent.TextPart("hel")}}}},
			{Content: []gent.ContentPart{&gent.AssistantContent{Type: gent.AssistantContentIntermediary,
				MessageId: "message", Content: []gent.ContentPart{gent.TextPart("lo")}}}},
		}, response: &gent.ModelCallResponse{Info: &gent.GenerationInfo{
			InputTokens: 3, OutputTokens: 4, CachedInputTokens: 1, ReasoningTokens: 2, TotalTokens: 7}}}
		stream, err := execCtx.GenerateContentStream(model, gent.ModelCall{Model: "gpt",
			Provider: "openai", StreamId: "stream", StreamTopicId: "topic"}, request)
		if err != nil {
			return nil, err
		}
		defer stream.Close()
		for range stream.Chunks() {
		}
		if _, err := stream.Response(); err != nil {
			return nil, err
		}
		before := execCtx.PublishBeforeToolCall("search", map[string]any{"query": "gent"})
		execCtx.PublishAfterToolCall("search", before.Args, "result", 5*time.Millisecond, nil,
			gent.WithToolCallId(before.ToolCallId))
		return traceTestDone(execCtx)
	}}
	runner, execCtx, _ := newTraceRun(t, context.Background(), loop,
		&gent.Config{Subscribers: []*gent.SubscriberRegistration{{Subscriber: seq}}})
	_, _, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Search Gent.")))
	require.NoError(t, err)

	snapshot := seq.Snapshot()
	require.Len(t, snapshot.ModelCalls, 1)
	require.Len(t, snapshot.ToolCalls, 1)
	assertJSONSafe(t, snapshot)
	assert.Equal(
		t,
		"hello",
		gent.ContentText(
			gent.AssistantParts(
				snapshot.ModelCalls[0].Stream.Content,
				gent.AssistantContentIntermediary,
			),
		),
	)
	assert.Equal(t, "openai", snapshot.ModelCalls[0].Provider)
	assert.Equal(t, &ModelUsage{
		InputTokens:       3,
		OutputTokens:      4,
		ReasoningTokens:   2,
		CachedInputTokens: 1,
		TotalTokens:       7,
	}, snapshot.ModelCalls[0].Usage)
	assert.Equal(t, "search", snapshot.ToolCalls[0].Name)
	assert.Equal(t, RunStatusSucceeded, snapshot.Status)
}

func TestSequencer_RedactionMissingIdsAndUTF8Truncation(t *testing.T) {
	seq := newTestSequencer(t, "run", Config{
		IncludeChunkText:      true,
		MaxStreamContentBytes: 5,
		IncludeModelRequests:  true,
		IncludeModelResponses: true,
		IncludeToolOutput:     true,
		Redactor: RedactorFuncs{
			ModelRequest: func(any) any { return func() {} },
			Error: func(error) *Error {
				return &Error{Message: "redacted"}
			},
		},
	})
	base := gent.BaseEvent{
		Timestamp: time.Now(), EventName: gent.EventNameModelCallBefore,
		Iteration: 1, Source: "main/1", ContextId: "ctx:1",
	}
	recordTestValue(t, seq, "model_call_started", &gent.BeforeModelCallEvent{
		BaseEvent: base,
		Model:     "gpt",
		Request: &gent.ModelCallRequest{
			Messages: []*gent.ModelMessage{gent.TextParts(gent.ChatMessageTypeHuman, "secret")},
		},
		ModelCallId: "model-1",
		StreamId:    "stream",
	})
	recordTestValue(
		t,
		seq,
		"model_stream_chunk",
		gent.StreamChunk{
			Content: []gent.ContentPart{
				&gent.AssistantContent{

					Type:    gent.AssistantContentIntermediary,
					Content: []gent.ContentPart{gent.TextContent{Text: "abcédef"}},
				},
			},
		},
		gent.RecordedEvent{ModelCallId: "model-1", ContextId: "ctx:1", Iteration: 1},
	)
	recordTestValue(t, seq, "model_call_finished", &gent.AfterModelCallEvent{
		BaseEvent: base,
		Model:     "gpt",
		Duration:  time.Millisecond,
		Error:     errors.New("raw secret"),
		// Missing ModelCallId is intentionally uncorrelated.
	})

	snapshot := seq.Snapshot()
	require.Len(t, snapshot.ModelCalls, 1)
	require.Len(t, snapshot.Errors, 1)
	assertJSONSafe(t, snapshot)
	assert.Equal(
		t,
		"abcé",
		gent.ContentText(
			gent.AssistantParts(
				snapshot.ModelCalls[0].Stream.Content,
				gent.AssistantContentIntermediary,
			),
		),
	)
	assert.True(t, snapshot.ModelCalls[0].Stream.ContentTruncated)
	assert.Equal(t, StepStatusRunning, snapshot.ModelCalls[0].Status)
	assert.Equal(t, "redacted", snapshot.Errors[0].Message)
	_, ok := snapshot.ModelCalls[0].Request.(map[string]any)
	assert.True(t, ok, "unmarshalable redactor output should become PayloadCaptureError map")
}

func TestSequencer_BoundedSubscriptionOverflow(t *testing.T) {
	type expected struct {
		received []uint64
		overflow OverflowInfo
	}

	seq := newTestSequencer(t, "run", Config{})
	overflowCh := make(chan OverflowInfo, 1)
	eventsCh, unsubscribe := seq.SubscribeWithConfig(SubscribeConfig{
		BufferSize:     1,
		OverflowPolicy: OverflowDropOldest,
		OnOverflow: func(info OverflowInfo) {
			overflowCh <- info
		},
	})
	defer unsubscribe()
	recordTestValue(t, seq, "common", &gent.CommonEvent{})
	recordTestValue(t, seq, "common", &gent.CommonEvent{})

	first := receiveTraceEvent(t, eventsCh)
	overflow := receiveOverflow(t, overflowCh)
	assert.Equal(t, expected{
		received: []uint64{2},
		overflow: OverflowInfo{
			Policy:                     OverflowDropOldest,
			DroppedEventSequenceNumber: 1,
			NewEventSequenceNumber:     2,
		},
	}, expected{received: []uint64{first.EventSequenceNumber}, overflow: overflow})
}

func TestSequencer_UnsubscribeDiscardsQueuedLiveEvents(t *testing.T) {
	seq := newTestSequencer(t, "run", Config{})
	eventsCh, unsubscribe := seq.Subscribe()
	recordTestValue(t, seq, "common", &gent.CommonEvent{})
	recordTestValue(t, seq, "common", &gent.CommonEvent{})

	unsubscribe()

	select {
	case _, ok := <-eventsCh:
		assert.False(t, ok, "unsubscribe should close without stale live events")
	case <-time.After(time.Second):
		t.Fatal("expected live subscription to close")
	}
}

func TestSequencer_ConcurrentRecordingIsMonotonic(t *testing.T) {
	seq := newTestSequencer(t, "", Config{})
	defer seq.Close()
	const workers = 50
	loop := &traceTestLoop{next: func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
		start := make(chan struct{})
		var group sync.WaitGroup
		failures := make(chan error, workers)
		for i := range workers {
			group.Add(1)
			go func() {
				defer group.Done()
				<-start
				failures <- execCtx.EmitChunk(gent.StreamChunk{Content: []gent.ContentPart{
					&gent.AssistantContent{Type: gent.AssistantContentIntermediary,
						MessageId: fmt.Sprintf("message-%d", i), Content: []gent.ContentPart{gent.TextPart("x")}}}})
			}()
		}
		close(start)
		group.Wait()
		close(failures)
		for err := range failures {
			if err != nil {
				return nil, err
			}
		}
		return traceTestDone(execCtx)
	}}
	runner, execCtx, store := newTraceRun(t, context.Background(), loop,
		&gent.Config{Subscribers: []*gent.SubscriberRegistration{{Subscriber: seq}}})
	_, _, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Publish concurrent progress.")))
	require.NoError(t, err)
	snapshot := seq.Snapshot()
	assert.Equal(t, workers, countTraceEventType(snapshot.RecentEvents, EventTypeModelStreamChunk))
	assertEventSequenceNumbersContiguous(t, snapshot.RecentEvents)
	assert.Equal(t, store.view.LastAppliedEventSequenceNumber, snapshot.LastEventSequenceNumber)
}

func assertJSONSafe(t *testing.T, value any) {
	t.Helper()
	_, err := json.Marshal(value)
	require.NoError(t, err)
}

func assertEventSequenceNumbersContiguous(t *testing.T, events []*Event) {
	t.Helper()
	for i := 1; i < len(events); i++ {
		assert.Equal(t, events[i-1].EventSequenceNumber+1, events[i].EventSequenceNumber)
	}
}

func countTraceEventType(events []*Event, eventType EventType) int {
	count := 0
	for _, event := range events {
		if event.Type == eventType {
			count++
		}
	}
	return count
}

func receiveTraceEvent(t *testing.T, ch <-chan *Event) *Event {
	t.Helper()
	select {
	case event := <-ch:
		return event
	case <-time.After(time.Second):
		t.Fatal("expected trace event")
		return nil
	}
}

func receiveOverflow(t *testing.T, ch <-chan OverflowInfo) OverflowInfo {
	t.Helper()
	select {
	case info := <-ch:
		return info
	case <-time.After(time.Second):
		t.Fatal("expected overflow info")
		return OverflowInfo{}
	}
}
