package trace

import (
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

func TestSequencerCallCorrelationRequiresIdentity(t *testing.T) {
	type input struct {
		modelCallId string
		toolCallId  string
	}
	type expected struct {
		model  ModelCall
		tool   ToolCall
		cursor uint64
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	running := expected{

		model: ModelCall{
			Id:                         "model-1",
			Model:                      "model",
			StreamId:                   "stream",
			ContextId:                  "ctx",
			Iteration:                  1,
			Status:                     StepStatusRunning,
			StartedEventSequenceNumber: 1,
			LastEventSequenceNumber:    1,
		},
		tool: ToolCall{Id: "tool-1", Name: "lookup", ContextId: "ctx", Iteration: 1,
			Status: StepStatusRunning, StartedEventSequenceNumber: 2, LastEventSequenceNumber: 2},
		cursor: 5,
	}
	completed := expected{
		model: ModelCall{Id: "model-1", Model: "model", StreamId: "stream", ContextId: "ctx",
			Iteration: 1, Status: StepStatusSucceeded, DurationMs: 1, StartedEventSequenceNumber: 1,
			LastEventSequenceNumber: 4, CompletedEventSequenceNumber: 4, Usage: &ModelUsage{},
			Stream: &ModelStream{Content: []gent.ContentPart{&gent.AssistantContent{
				Type: gent.AssistantContentIntermediary,
				Content: []gent.ContentPart{
					gent.TextContent{Text: "answer"},
				},
			},
			}, ChunkCount: 1, LastChunkEventSequenceNumber: 3,
			}},
		tool: ToolCall{Id: "tool-1", Name: "lookup", ContextId: "ctx", Iteration: 1,
			Status: StepStatusSucceeded, DurationMs: 1, StartedEventSequenceNumber: 2,
			LastEventSequenceNumber: 5, CompletedEventSequenceNumber: 5},
		cursor: 5,
	}
	cases := []testCase{
		{"matching call IDs", input{"model-1", "tool-1"}, completed},
		{"missing IDs with matching stream and tool name", input{}, running},
		{"unknown IDs with matching stream and tool name", input{"unknown", "unknown"}, running},
	}
	for _, test := range cases {
		t.Run(
			test.name,
			func(t *testing.T) {
				started := time.Now()
				seq := newTestSequencer(t, "run", Config{IncludeChunkText: true})
				defer seq.Close()
				base := gent.BaseEvent{Timestamp: started, ContextId: "ctx", Iteration: 1}
				recordTestValue(t, seq, "model_call_started", &gent.BeforeModelCallEvent{
					BaseEvent: base, Model: "model", ModelCallId: "model-1", StreamId: "stream"})
				recordTestValue(t, seq, "tool_call_started", &gent.BeforeToolCallEvent{
					BaseEvent: base, ToolName: "lookup", ToolCallId: "tool-1"})
				recordTestValue(
					t,
					seq,
					"model_stream_chunk",
					gent.StreamChunk{
						Content: []gent.ContentPart{
							&gent.AssistantContent{

								Type: gent.AssistantContentIntermediary,
								Content: []gent.ContentPart{
									gent.TextContent{Text: "answer"},
								},
							},
						},
					},
					gent.RecordedEvent{ModelCallId: test.input.modelCallId, ContextId: "ctx", Iteration: 1},
				)
				finished := time.Now()
				base.Timestamp = finished
				recordTestValue(t, seq, "model_call_finished", &gent.AfterModelCallEvent{
					BaseEvent: base, Model: "model", ModelCallId: test.input.modelCallId,
					StreamId: "stream", Duration: time.Millisecond})
				recordTestValue(t, seq, "tool_call_finished", &gent.AfterToolCallEvent{
					BaseEvent: base, ToolName: "lookup", ToolCallId: test.input.toolCallId,
					Duration: time.Millisecond})

				want := test.expected
				model, tool := &want.model, &want.tool
				model.StartedTs, tool.StartedTs = started, started
				model.LastUpdatedTs, tool.LastUpdatedTs = started, started
				if model.Status == StepStatusSucceeded {
					model.LastUpdatedTs, tool.LastUpdatedTs = finished, finished
					model.CompletedTs, tool.CompletedTs = &finished, &finished
				}
				snapshot := seq.Snapshot()
				assert.GreaterOrEqual(t, snapshot.LastUpdatedTs, started)
				assert.Equal(t, []*ModelCall{model}, snapshot.ModelCalls)
				assert.Equal(t, []*ToolCall{tool}, snapshot.ToolCalls)
				assert.Equal(t, want.cursor, snapshot.LastEventSequenceNumber)
			},
		)
	}
}
