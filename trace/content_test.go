package trace

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFinalContentTraceRetention(t *testing.T) {
	type input struct {
		include bool
		limit   int
		redact  func([]gent.ContentPart) any
	}
	type expected struct {
		content   string
		truncated bool
		err       error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"visible", input{include: true}, expected{content: "Accepted."}},
		{"hidden", input{}, expected{}},
		{"bounded", input{include: true, limit: 5}, expected{"Accep", true, nil}},
		{"redacted text", input{include: true, redact: func([]gent.ContentPart) any {
			return "Removed."
		}}, expected{content: "Removed."}},
		{"redacted parts", input{include: true, redact: func([]gent.ContentPart) any {
			return []gent.ContentPart{gent.TextContent{Text: "Removed."}}
		}}, expected{content: "Removed."}},
		{"redacted non-text", input{include: true, redact: func([]gent.ContentPart) any {
			return map[string]any{"removed": true}
		}}, expected{}},
	}
	for _, test := range cases {
		t.Run(
			test.name,
			func(t *testing.T) {
				cfg := Config{IncludeRunOutput: test.input.include, MaxContentBytes: test.input.limit,
					Redactor: RedactorFuncs{RunOutput: test.input.redact}}
				seq := newTestSequencer(t, "", cfg)
				loop := &traceTestLoop{
					next: func(
						execCtx *gent.ExecutionContext,
					) (
						*gent.AgentLoopResult,
						error,
					) {
						return gent.AcceptOutput(execCtx, &gent.OutputResult[string]{Accepted: true,
							Value: "Accepted.", Content: []gent.ContentPart{gent.TextPart("Accepted.")}},
							&gent.ContentSource{MessageIds: []string{"preview"}})
					},
				}
				runner, ctx, store := newTraceRun(t, context.Background(), loop,
					&gent.Config{Subscribers: []*gent.SubscriberRegistration{{Subscriber: seq}}})
				_, _, err := runner.NewTurn(ctx, gent.NewTask(gent.TextPart("Answer.")))
				require.NoError(t, err)
				snapshot := seq.Snapshot()
				assert.Equal(t, test.expected, expected{gent.ContentText(snapshot.Contexts[0].Content),
					snapshot.Contexts[0].ContentTruncated, err})
				assert.Equal(t, ctx.TurnId()+":answer", snapshot.Contexts[0].MessageId)
				assert.Equal(t, []string{"preview"}, snapshot.Contexts[0].SourceMessageIds)
				assert.Equal(t, snapshot.LastEventSequenceNumber, snapshot.Contexts[0].LastEventSequenceNumber)
				snapshot.Contexts[0].SourceMessageIds[0] = "changed"
				assert.Equal(t, []string{"preview"}, seq.Snapshot().Contexts[0].SourceMessageIds)
				replay, err := RestoreSequencer(*store.view, cfg)
				require.NoError(t, err)
				defer replay.Close()
				assert.Equal(t, seq.Snapshot(), replay.Snapshot())
			},
		)
	}
}

func TestTraceMetadataOrderAndSharedLimit(t *testing.T) {
	seq := newTestSequencer(t, "meta", Config{IncludeChunkText: true, MaxStreamContentBytes: 5})
	recordTestValue(t, seq, "model_call_started", &gent.BeforeModelCallEvent{
		Model: "fixture", ModelCallId: "model-1", Request: gent.NewModelCallRequest(nil)})
	for _, meta := range []gent.ContentPart{&gent.AssistantContent{
		Type:      gent.AssistantContentReasoning,
		Content:   []gent.ContentPart{gent.TextContent{Text: "abc"}},
		MessageId: "reason",
	}, &gent.AssistantContent{
		Type:      gent.AssistantContentCommentary,
		Content:   []gent.ContentPart{gent.TextContent{Text: "def"}},
		MessageId: "comment",
	}, &gent.AssistantContent{
		Type:      "progress",
		Content:   []gent.ContentPart{gent.TextContent{Text: "ghi"}},
		MessageId: "progress",
	},
	} {
		recordTestValue(t, seq, "model_stream_chunk", gent.StreamChunk{Content: []gent.ContentPart{meta}},
			gent.RecordedEvent{ModelCallId: "model-1"})
	}
	want := []gent.ContentPart{&gent.AssistantContent{
		Type:      gent.AssistantContentReasoning,
		Content:   []gent.ContentPart{gent.TextContent{Text: "abc"}},
		MessageId: "reason",
	}, &gent.AssistantContent{
		Type:      gent.AssistantContentCommentary,
		Content:   []gent.ContentPart{gent.TextContent{Text: "de"}},
		MessageId: "comment",
	},
	}
	snapshot := seq.Snapshot()
	assert.Equal(t, want, snapshot.ModelCalls[0].Stream.Content)
	assert.True(t, snapshot.ModelCalls[0].Stream.ContentTruncated)
	snapshot.ModelCalls[0].Stream.Content[0].(*gent.AssistantContent).Content = []gent.ContentPart{
		gent.TextContent{Text: "changed"},
	}
	assert.Equal(t, want, seq.Snapshot().ModelCalls[0].Stream.Content)
}
