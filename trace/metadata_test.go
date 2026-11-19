package trace

import (
	"testing"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

func TestMetadataTraceRetention(t *testing.T) {
	type input struct {
		include bool
		limit   int
		redact  bool
	}
	type expected struct {
		text      string
		truncated bool
		err       error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"visible", input{true, 0, false}, expected{text: "Checking."}},
		{"hidden", input{false, 0, false}, expected{}},
		{"bounded", input{true, 5, false}, expected{text: "Check", truncated: true}},
		{"unlimited", input{true, NoContentLimit, false}, expected{text: "Checking."}},
		{"redacted", input{true, 0, true}, expected{text: "Removed."}},
	}
	for _, test := range cases {
		t.Run(
			test.name,
			func(t *testing.T) {
				cfg := Config{IncludeChunkText: test.input.include, MaxStreamContentBytes: test.input.limit}
				if test.input.redact {
					cfg.Redactor = RedactorFuncs{
						Chunk: func(chunk gent.StreamChunk) gent.StreamChunk {
							chunk.Content[0].(*gent.AssistantContent).Content = []gent.ContentPart{
								gent.TextContent{
									Text: "Removed.",
								},
							}
							return chunk
						},
					}
				}
				seq := newTestSequencer(t, "commentary", cfg)
				recordTestValue(t, seq, "model_call_started", &gent.BeforeModelCallEvent{
					Model: "fixture", ModelCallId: "model-1", Request: gent.NewModelCallRequest(nil)})
				chunk := gent.StreamChunk{Content: []gent.ContentPart{&gent.AssistantContent{
					Type: gent.AssistantContentCommentary, MessageId: "commentary",
					Content: []gent.ContentPart{gent.TextPart("Checking.")}}}}
				recordTestValue(t, seq, "model_stream_chunk", chunk, gent.RecordedEvent{ModelCallId: "model-1"})
				// The display redactor owns its copy. It must not change saved model evidence.
				assert.Equal(t, "Checking.", gent.ContentText(chunk.Content))
				calls := seq.Snapshot().ModelCalls
				if !assert.Len(t, calls, 1) || !assert.NotNil(t, calls[0].Stream) {
					return
				}
				stream := calls[0].Stream
				assert.Equal(t, test.expected, expected{
					gent.ContentText(gent.AssistantParts(stream.Content, gent.AssistantContentCommentary)),
					stream.ContentTruncated, nil})
			},
		)
	}
}
