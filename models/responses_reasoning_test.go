package models

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

func TestResponsesReasoningAndSavedEvidence(t *testing.T) {
	type input struct {
		streaming bool
		reasoning string
	}
	type chunkText struct{ content, reasoning, commentary string }
	type expected struct {
		choice *gent.ContentChoice
		chunks []chunkText
		err    error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	const visible = `{"id":"reason-1","type":"reasoning","summary":[
{"type":"summary_text","text":"I checked "},{"type":"summary_text","text":"the result."}],
"encrypted_content":"opaque","provider_extra":{"keep":true}}`
	const opaque = `{"id":"reason-1","type":"reasoning","summary":[],
"encrypted_content":"opaque","provider_extra":{"keep":true}}`
	const commentary = `{"id":"update-1","type":"message","role":"assistant","phase":"commentary",
"content":[{"type":"output_text","text":"Checking the result."}]}`
	const message = `{"type":"message","role":"assistant","phase":"final_answer","content":[
{"type":"output_text","text":"Done."}]}`
	choice := func(raw, summary string) *gent.ContentChoice {
		return &gent.ContentChoice{

			Completion: gent.ModelCompletion{
				Status:         gent.ModelStatusAnswer,
				ProviderReason: "completed",
			},
			CandidateSource: &gent.ContentSource{MessageIds: []string{"output:2"}},
			Continuation: &gent.ProviderContinuation{
				Provider: "openai",
				Items: []json.RawMessage{

					json.RawMessage(
						raw,
					),
					json.RawMessage(
						commentary,
					),
					json.RawMessage(
						message,
					),
				},
			},
			Parts: responseTestMeta(
				summary,
			),
		}
	}
	cases := []testCase{
		{"JSON with summary", input{false, visible}, expected{
			choice: choice(visible, "I checked the result."),
		}},
		{"SSE with summary", input{true, visible}, expected{
			choice: choice(visible, "I checked the result."),
			chunks: []chunkText{{reasoning: "I checked "}, {reasoning: "the result."},
				{commentary: "Checking the result."}, {content: "Done."}},
		}},
		{"JSON with opaque data only", input{false, opaque}, expected{choice: choice(opaque, "")}},
		{"SSE with opaque data only", input{true, opaque}, expected{
			choice: choice(opaque, ""),
			chunks: []chunkText{{commentary: "Checking the result."}, {content: "Done."}},
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			final := `{"status":"completed","output":[` + test.input.reasoning + `,` +
				commentary + `,` + message + `]}`
			model := NewResponses(&ResponsesConfig{
				Provider: "openai", BaseURL: "https://fixture.test/v1", Model: "fixture",
				HTTPClient: &http.Client{Transport: responseTransport(func(
					*http.Request,
				) (*http.Response, error) {
					if !test.input.streaming {
						return &http.Response{StatusCode: 200,
							Header: http.Header{"Content-Type": {"application/json"}},
							Body:   io.NopCloser(strings.NewReader(final))}, nil
					}
					var payloads []string
					if test.input.reasoning == visible {
						payloads = append(payloads,
							`{"type":"response.reasoning_summary_text.delta","delta":"I checked "}`,
							`{"type":"response.reasoning_summary_text.delta","delta":"the result."}`)
					}
					payloads = append(payloads,
						`{"type":"response.output_item.added","output_index":1,"item":`+commentary+`}`,
						`{"type":"response.output_item.added","output_index":2,"item":`+message+`}`,
						`{"type":"response.output_text.delta","output_index":1,
"delta":"Checking the result."}`,
						`{"type":"response.output_text.delta","output_index":2,"delta":"Done."}`,
						`{"type":"response.completed","response":`+final+`}`)
					return sseResponse(payloads...), nil
				})},
			})
			started := time.Now()
			var chunks []gent.StreamChunk
			var result *gent.ModelCallResponse
			actual := expected{}
			loop := modelFixtureLoop(func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
				stream, err := execCtx.GenerateContentStream(model, gent.ModelCall{Id: "fixture-call"},
					gent.NewModelCallRequest(nil))
				if err != nil {
					return nil, err
				}
				defer stream.Close()
				for chunk := range stream.Chunks() {
					chunks = append(chunks, chunk)
					actual.chunks = append(actual.chunks, chunkText{
						gent.ContentText(gent.AssistantParts(chunk.Content,
							gent.AssistantContentFinalAnswerCandidate)),
						gent.ContentText(gent.AssistantParts(chunk.Content, gent.AssistantContentReasoning)),
						gent.ContentText(gent.AssistantParts(chunk.Content, gent.AssistantContentCommentary))})
				}
				result, err = stream.Response()
				if err != nil {
					return nil, err
				}
				return modelFixtureDone(execCtx)
			})
			runner, execCtx := newModelExecution(t, context.Background(), loop, nil)
			_, _, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read the record.")))
			if !assert.NoError(t, err) || !assert.Len(t, result.Choices, 1) {
				return
			}
			actual.choice, actual.err = result.Choices[0], err
			prefix := strings.TrimSuffix(result.Choices[0].CandidateSource.MessageIds[0], "output:2")
			for _, item := range test.expected.choice.Parts {
				content := item.(*gent.AssistantContent)
				content.MessageId = prefix + content.MessageId
			}
			test.expected.choice.CandidateSource = &gent.ContentSource{ModelCallId: "fixture-call",
				MessageIds: []string{prefix + "output:2"}}
			if !test.input.streaming {
				test.expected.chunks = []chunkText{}
				if test.input.reasoning == visible {
					test.expected.chunks = append(test.expected.chunks,
						chunkText{reasoning: "I checked the result."})
				}
				test.expected.chunks = append(test.expected.chunks,
					chunkText{commentary: "Checking the result."}, chunkText{content: "Done."})
			}
			assert.Equal(t, test.expected, actual)

			// Verify the portable record, including raw provider fields and reasoning chunks.
			view := execCtx.Config().Recorder.(*modelTestStore).view
			data, err := execCtx.Codecs().Marshal(view)
			if !assert.NoError(t, err) {
				return
			}
			codecs := gent.NewCodecRegistry()
			var saved gent.RunView
			if !assert.NoError(t, codecs.Unmarshal(data, &saved)) {
				return
			}
			var savedChunks []gent.StreamChunk
			var savedResponses []*gent.ModelCallResponse
			for _, event := range saved.Activity {
				assert.GreaterOrEqual(t, event.Ts, started)
				switch event.Type {
				case "model_call_finished":
					savedResponses = append(savedResponses, event.Payload.(*gent.AfterModelCallEvent).Response)
				case "model_stream_chunk":
					savedChunks = append(savedChunks, *event.Payload.(*gent.StreamChunk))
				}
			}
			assert.Equal(t, []*gent.ModelCallResponse{result}, savedResponses)
			assert.Equal(t, chunks, savedChunks)
		})
	}
}

func responseTestMeta(summary string) []gent.ContentPart {
	var parts []gent.ContentPart
	if summary != "" {
		parts = append(parts, &gent.AssistantContent{Type: gent.AssistantContentReasoning,
			MessageId: "output:0", Content: []gent.ContentPart{
				gent.TextPart("I checked "), gent.TextPart("the result.")}})
	}
	return append(parts,
		&gent.AssistantContent{Type: gent.AssistantContentCommentary, MessageId: "output:1",
			Content: []gent.ContentPart{gent.TextPart("Checking the result.")}},
		&gent.AssistantContent{Type: gent.AssistantContentFinalAnswerCandidate, MessageId: "output:2",
			Content: []gent.ContentPart{gent.TextPart("Done.")}})
}
