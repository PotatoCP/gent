package gent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStreamPreservesAssistantItems(t *testing.T) {
	type input struct{ completed bool }
	type expected struct {
		response *ModelCallResponse
		chunks   []StreamChunk
		err      error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	parts := []ContentPart{
		&AssistantContent{Type: AssistantContentReasoning, MessageId: "reasoning",
			Content: []ContentPart{TextPart("Checked.")}},
		&AssistantContent{Type: AssistantContentCommentary, MessageId: "commentary",
			Content: []ContentPart{TextPart("Checking.")}},
		&AssistantContent{Type: AssistantContentFinalAnswerCandidate, MessageId: "answer",
			Content: []ContentPart{TextPart("Done.")}},
	}
	response := &ModelCallResponse{Choices: []*ContentChoice{{
		Completion:      ModelCompletion{Status: ModelStatusAnswer},
		CandidateSource: &ContentSource{MessageIds: []string{"answer"}}, Parts: parts,
	}}}
	chunks := []StreamChunk{{Content: parts[:1]}, {Content: parts[1:2]}, {Content: parts[2:]}}
	cases := []testCase{
		{"completed provider", input{true}, expected{response, []StreamChunk{{Content: parts}}, nil}},
		{"streaming provider", input{false}, expected{response, chunks, nil}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var stream Stream
			if test.input.completed {
				stream = NewCompletedStream(response, nil)
			} else {
				provider := NewStreamWithDuration()
				for _, chunk := range chunks {
					assert.True(t, provider.Send(chunk))
				}
				provider.Complete(response, nil)
				stream = provider
			}
			defer stream.Close()
			acc := NewStreamAccumulator()
			actual := expected{}
			for chunk := range stream.Chunks() {
				acc.Add(chunk)
				actual.chunks = append(actual.chunks, chunk)
			}
			actual.response, actual.err = stream.Response()
			assert.Equal(t, test.expected, actual)
			assert.Equal(t, parts, acc.Content())
			assert.Equal(t, &ModelCallResponse{Choices: []*ContentChoice{{
				Completion:      ModelCompletion{Status: ModelStatusIncomplete},
				CandidateSource: &ContentSource{MessageIds: []string{"answer"}}, Parts: parts,
			}}}, acc.Response())
		})
	}
}
