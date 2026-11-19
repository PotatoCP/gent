package gent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStreamAccumulator(t *testing.T) {
	type input struct{ chunks []StreamChunk }
	type expected struct {
		parts  []ContentPart
		source *ContentSource
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	item := func(kind AssistantContentType, id, text string) *AssistantContent {
		return &AssistantContent{Type: kind, MessageId: id, Content: []ContentPart{TextPart(text)}}
	}
	cases := []testCase{
		{"empty", input{}, expected{}},
		{"joined candidate", input{[]StreamChunk{
			{Content: []ContentPart{item(AssistantContentIntermediary, "m1", "The total ")}},
			{Content: []ContentPart{item(AssistantContentFinalAnswerCandidate, "m1", "is 42.")}},
		}}, expected{[]ContentPart{
			item(AssistantContentFinalAnswerCandidate, "m1", "The total is 42."),
		}, &ContentSource{MessageIds: []string{"m1"}}}},
		{"ordered items", input{[]StreamChunk{
			{Content: []ContentPart{item(AssistantContentReasoning, "r1", "Check.")}},
			{Content: []ContentPart{item(AssistantContentCommentary, "p1", "50%")}},
			{Content: []ContentPart{item(AssistantContentReasoning, "r2", "Done.")}},
		}}, expected{parts: []ContentPart{
			item(AssistantContentReasoning, "r1", "Check."),
			item(AssistantContentCommentary, "p1", "50%"),
			item(AssistantContentReasoning, "r2", "Done."),
		}}},
		{"partial progress", input{[]StreamChunk{
			{Content: []ContentPart{item(AssistantContentIntermediary, "partial", "Partial")}},
		}}, expected{parts: []ContentPart{item(AssistantContentIntermediary, "partial", "Partial")}}},
		{"ordered media", input{[]StreamChunk{
			{Content: []ContentPart{&AssistantContent{Type: AssistantContentFinalAnswerCandidate,
				MessageId: "m1", Content: []ContentPart{TextPart("Image: "),
					BinaryContent{MIMEType: "image/png", Data: []byte{1, 2}}, TextPart(" End.")}}}},
		}}, expected{[]ContentPart{&AssistantContent{Type: AssistantContentFinalAnswerCandidate,
			MessageId: "m1", Content: []ContentPart{TextPart("Image: "),
				BinaryContent{MIMEType: "image/png", Data: []byte{1, 2}}, TextPart(" End.")}}},
			&ContentSource{MessageIds: []string{"m1"}}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			acc := NewStreamAccumulator()
			for _, chunk := range test.input.chunks {
				acc.Add(chunk)
			}
			assert.Equal(t, test.expected.parts, acc.Content())
			assert.Equal(t, &ModelCallResponse{Choices: []*ContentChoice{{
				Completion: ModelCompletion{Status: ModelStatusIncomplete},
				Parts:      test.expected.parts, CandidateSource: test.expected.source,
			}}}, acc.Response())
			// A display can edit its independent snapshot without changing later reads.
			parts := acc.Content()
			if len(parts) > 0 {
				parts[0].(*AssistantContent).Content = []ContentPart{TextPart("changed")}
			}
			assert.Equal(t, test.expected.parts, acc.Content())
			acc.Reset()
			assert.Equal(t, []ContentPart(nil), acc.Content())
		})
	}
}
