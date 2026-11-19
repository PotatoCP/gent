package models

import (
	"context"
	"errors"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

// Record assertion failures without failing the enclosing test. This proves that the exact
// helper used by live tests rejects invalid streams even when the final response is valid.
type liveCheckResult struct{ failures int }

func (r *liveCheckResult) Helper()               {}
func (r *liveCheckResult) Errorf(string, ...any) { r.failures++ }

type liveCheckModel struct {
	chunks []gent.StreamChunk
	err    error
}

func (liveCheckModel) PrepareRequest(execCtx *gent.ExecutionContext, request *gent.ModelCallRequest,
	chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}

func (model liveCheckModel) GenerateContentStream(context.Context, *gent.ModelCallRequest) (
	gent.Stream, error,
) {
	stream := gent.NewStreamWithDuration()
	for _, chunk := range model.chunks {
		stream.Send(chunk)
	}
	stream.Complete(
		&gent.ModelCallResponse{
			Choices: []*gent.ContentChoice{
				{

					Completion: gent.ModelCompletion{
						Status: gent.ModelStatusAnswer,
					},
					CandidateSource: &gent.ContentSource{
						MessageIds: []string{
							"answer",
						},
					},
					Parts: []gent.ContentPart{
						&gent.AssistantContent{
							Type:      gent.AssistantContentFinalAnswerCandidate,
							MessageId: "answer",
							Content: []gent.ContentPart{
								gent.TextPart(
									"gent-live-ok",
								),
							},
						},
					},
				},
			},
			Info: &gent.GenerationInfo{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
		},
		model.err,
	)
	return stream, nil
}

func TestLiveTextCheckRejectsInvalidStreams(t *testing.T) {
	type input struct {
		chunks []gent.StreamChunk
		err    error
	}
	type expected struct{ failures int }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	preview := func(text string) gent.StreamChunk {
		return gent.StreamChunk{
			Content: []gent.ContentPart{
				&gent.AssistantContent{
					Type:      gent.AssistantContentFinalAnswerCandidate,
					MessageId: "answer",
					Content:   []gent.ContentPart{gent.TextPart(text)},
				},
			},
		}
	}
	cases := []testCase{
		{"complete preview", input{chunks: []gent.StreamChunk{preview("gent-"), preview("live-ok")}},
			expected{0}},
		{"no chunks", input{}, expected{1}},
		{"empty chunk", input{chunks: []gent.StreamChunk{{}}}, expected{1}},
		{"wrong preview", input{chunks: []gent.StreamChunk{preview("wrong")}}, expected{1}},
		{"stream error", input{chunks: []gent.StreamChunk{preview("gent-live-ok")},
			err: errors.New("lost preview")}, expected{1}},
		{"unvalidated output", input{chunks: []gent.StreamChunk{preview("gent-live-ok"),
			{Content: []gent.ContentPart{gent.TextPart("unvalidated")}}}}, expected{1}},
	}
	for _, test := range cases {
		t.Run(
			test.name,
			func(t *testing.T) {
				result := &liveCheckResult{}
				checkLiveText(
					result,
					context.Background(),
					liveCheckModel{
						chunks: test.input.chunks,
						err:    test.input.err,
					},
				)
				assert.Equal(t, test.expected, expected{result.failures})
			},
		)
	}
}
