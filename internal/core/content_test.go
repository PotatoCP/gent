package gent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContentChoiceCompletion(t *testing.T) {
	type input struct{ choice *ContentChoice }
	type expected struct{ err string }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	tool := []*ModelToolCallRequest{{ID: "lookup", Type: "function",
		FunctionCall: &FunctionCall{Name: "lookup", Arguments: "{}"}}}
	cases := []testCase{

		{
			"answer",
			input{&ContentChoice{Completion: ModelCompletion{Status: ModelStatusAnswer}}},
			expected{},
		},
		{"unknown", input{&ContentChoice{}}, expected{`gent: unknown model completion ""`}},
		{"missing calls",
			input{
				&ContentChoice{Completion: ModelCompletion{Status: ModelStatusToolCalls}},
			},
			expected{"gent: tool completion has no calls"}},
		{"answer with calls", input{&ContentChoice{
			Completion: ModelCompletion{Status: ModelStatusAnswer}, Parts: func() []ContentPart {
				var parts []ContentPart
				for _, item := range tool {
					parts = append(parts, item)

				}
				return parts
			}(),
		}},
			expected{"gent: answer completion contains tool calls"}},
		{"tools", input{&ContentChoice{
			Completion: ModelCompletion{Status: ModelStatusToolCalls}, Parts: func() []ContentPart {
				var parts []ContentPart
				for _, item := range tool {
					parts = append(parts, item)

				}
				return parts
			}(),
		}}, expected{}},
		{"incomplete calls", input{&ContentChoice{
			Completion: ModelCompletion{Status: ModelStatusIncomplete}, Parts: func() []ContentPart {
				var parts []ContentPart
				for _, item := range tool {
					parts = append(parts, item)

				}
				return parts
			}(),
		}}, expected{}},
		{
			"refused calls retained",
			input{
				&ContentChoice{

					Completion: ModelCompletion{
						Status:  ModelStatusRefused,
						Refusal: &ModelRefusal{},
					},
					Parts: func() []ContentPart {
						var parts []ContentPart
						for _, item := range tool {
							parts = append(parts, item)

						}
						return parts
					}(),
				},
			},
			expected{},
		},
		{"refusal missing",
			input{
				&ContentChoice{Completion: ModelCompletion{Status: ModelStatusRefused}},
			},
			expected{`gent: completion "refused" has inconsistent refusal data`}},
		{"refusal on answer", input{&ContentChoice{Completion: ModelCompletion{
			Status: ModelStatusAnswer, Refusal: &ModelRefusal{}}}},
			expected{`gent: completion "answer" has inconsistent refusal data`}},
		{"missing continuation",
			input{
				&ContentChoice{Completion: ModelCompletion{Status: ModelStatusContinue}},
			},
			expected{"gent: continuation has no provider data"}},
		{"continuation", input{&ContentChoice{Completion: ModelCompletion{Status: ModelStatusContinue},
			Continuation: &ProviderContinuation{Provider: "fixture"}}}, expected{}},
		{
			"failed",
			input{&ContentChoice{Completion: ModelCompletion{Status: ModelStatusFailed}}},
			expected{},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			actual := expected{}
			if err := tt.input.choice.ValidateCompletion(); err != nil {
				actual.err = err.Error()
			}
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func TestStreamOwnsMetadataAndResponse(t *testing.T) {
	meta := []ContentPart{&AssistantContent{
		Type:      AssistantContentReasoning,
		Content:   []ContentPart{TextContent{Text: "Check."}},
		MessageId: "reason",
	}, &AssistantContent{
		Type:      AssistantContentIntermediary,
		Content:   []ContentPart{TextContent{Text: "Half done."}},
		MessageId: "progress",
	}, &AssistantContent{
		Type:      AssistantContentFinalAnswerCandidate,
		Content:   []ContentPart{TextContent{Text: "Answer."}},
		MessageId: "answer",
	},
	}
	response := &ModelCallResponse{Choices: []*ContentChoice{{Completion: ModelCompletion{
		Status: ModelStatusAnswer,
	},
		CandidateSource: &ContentSource{
			MessageIds: []string{
				"answer",
			},
		},
		Continuation: &ProviderContinuation{Provider: "fixture",
			Items: []json.RawMessage{json.RawMessage(`{"type":"message"}`)}}, Parts: CloneContentParts(meta),
	}},
		Info: &GenerationInfo{RawGenerationInfo: map[string]any{"usage": map[string]any{"tokens": 2}}}}
	want, err := cloneOwned(NewCodecRegistry(), response)
	assert.NoError(t, err)
	stream := NewStreamWithDuration()
	stream.Send(StreamChunk{Content: meta})
	stream.Complete(response, nil)
	meta[0].(*AssistantContent).Content = []ContentPart{TextContent{Text: "producer changed delta"}}
	response.Choices[0].Parts[0].(*AssistantContent).Content = []ContentPart{
		TextContent{Text: "producer changed response"},
	}
	response.Choices[0].CandidateSource.MessageIds[0] = "changed"
	response.Info.RawGenerationInfo["usage"].(map[string]any)["tokens"] = 999
	response.Choices[0].Continuation.Items[0][0] = '!'
	chunks := []StreamChunk{}
	for chunk := range stream.Chunks() {
		chunks = append(chunks, chunk)
	}
	assert.Equal(t, []StreamChunk{{Content: want.Choices[0].Parts}}, chunks)
	chunks[0].Content[0].(*AssistantContent).Content = []ContentPart{
		TextContent{
			Text: "subscriber changed delta",
		},
	}
	first, err := stream.Response()
	assert.NoError(t, err)
	assert.Equal(t, want, first)
	// Response is a read-only view. A consumer that needs an editable copy asks for one.
	first, err = cloneOwned(NewCodecRegistry(), first)
	assert.NoError(t, err)
	first.Choices[0].Parts[0].(*AssistantContent).Content = []ContentPart{
		TextContent{Text: "reader changed response"},
	}
	first.Choices[0].CandidateSource.MessageIds[0] = "changed"
	first.Info.RawGenerationInfo["usage"].(map[string]any)["tokens"] = 999
	first.Choices[0].Continuation.Items[0][0] = '!'
	second, err := stream.Response()
	assert.NoError(t, err)
	assert.Equal(t, want, second)
}
