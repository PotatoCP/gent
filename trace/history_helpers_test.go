package trace

import (
	"context"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

func newTestSequencer(t *testing.T, id string, cfg Config) *Sequencer {
	t.Helper()
	seq, err := NewSequencer(id, cfg)
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	return seq
}

// Project a fixture representing an already persisted event. History tests exercise acceptance
// through real execution contexts; these cases isolate display/retention behavior.
func recordTestValue(
	t *testing.T,
	seq *Sequencer,
	kind string,
	value any,
	envelopes ...gent.RecordedEvent,
) {
	t.Helper()
	registry := gent.NewCodecRegistry()
	frozen, err := registry.Clone(value)
	if !assert.NoError(t, err) {
		return
	}
	snapshot := seq.Snapshot()
	event := &gent.RecordedEvent{}
	if len(envelopes) > 0 {
		*event = envelopes[0]
	}
	event.Type, event.Payload, event.RunId = kind, frozen, snapshot.RunId
	event.EventSequenceNumber, event.Ts = snapshot.LastEventSequenceNumber+1, time.Now()
	if typed, ok := value.(interface{ EventBase() *gent.BaseEvent }); ok {
		base := typed.EventBase()
		event.ContextId, event.Iteration = base.ContextId, base.Iteration
		event.AttemptId = base.AttemptId
		event.EventName = base.EventName
		if !base.Timestamp.IsZero() {
			event.Ts = base.Timestamp
		}
	}
	switch typed := value.(type) {
	case *gent.BeforeModelCallEvent:
		event.ModelCallId = typed.ModelCallId
	case *gent.AfterModelCallEvent:
		event.ModelCallId = typed.ModelCallId
	case *gent.BeforeToolCallEvent:
		event.ToolCallId = typed.ToolCallId
	case *gent.AfterToolCallEvent:
		event.ToolCallId = typed.ToolCallId
	case gent.StreamChunk:
		chunk := frozen.(gent.StreamChunk)
		event.Payload = &chunk
	}
	assert.NoError(t, seq.OnEvent(context.Background(), event))
}
