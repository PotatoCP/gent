package compaction_test

import (
	"fmt"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/compaction"
	"github.com/rickchristie/gent/format"
	"github.com/rickchristie/gent/internal/tt"
	"github.com/stretchr/testify/assert"
)

func TestSummarization_RequiresCompletedSummary(t *testing.T) {
	type input struct{ choice *gent.ContentChoice }
	type expected struct {
		summary      string
		err          string
		modelFailure bool
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{

		{
			"completed summary",
			input{
				&gent.ContentChoice{

					Completion: gent.ModelCompletion{

						Status:         gent.ModelStatusAnswer,
						ProviderReason: "stop",
					},
					CandidateSource: &gent.ContentSource{
						MessageIds: []string{
							"answer",
						},
					},
					Parts: []gent.ContentPart{
						&gent.AssistantContent{

							Type:      gent.AssistantContentReasoning,
							MessageId: "reason",
							Content:   []gent.ContentPart{gent.TextContent{Text: "Check payment."}},
						},
						&gent.AssistantContent{
							Type: gent.AssistantContentFinalAnswerCandidate, MessageId: "answer",
							Content: []gent.ContentPart{gent.TextContent{Text: "The order is paid."}},
						},
					},
				},
			},
			expected{summary: `<prior_work_summary>
The order is paid.
</prior_work_summary>`},
		},
		{
			"custom adapter without stop reason",
			input{
				&gent.ContentChoice{
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
								gent.TextContent{
									Text: "The order is paid.",
								},
							},
						},
					},
				},
			},
			expected{summary: `<prior_work_summary>
The order is paid.
</prior_work_summary>`},
		},
		{"commentary only",
			input{&gent.ContentChoice{Completion: gent.ModelCompletion{
				Status:         gent.ModelStatusAnswer,
				ProviderReason: "stop",
			}, Parts: []gent.ContentPart{&gent.AssistantContent{
				Type: gent.AssistantContentCommentary, MessageId: "comment",
				Content: []gent.ContentPart{gent.TextContent{Text: "I will summarize the order."}},
			},
			},
			}}, expected{err: "summarization model returned no summary text"}},
		{"reasoning only",
			input{&gent.ContentChoice{Completion: gent.ModelCompletion{
				Status:         gent.ModelStatusAnswer,
				ProviderReason: "stop",
			}, Parts: []gent.ContentPart{&gent.AssistantContent{
				Type: gent.AssistantContentReasoning, MessageId: "reason",
				Content: []gent.ContentPart{gent.TextContent{Text: "Check payment."}},
			},
			},
			}}, expected{err: "summarization model returned no summary text"}},
		{
			"unknown metadata only",
			input{
				&gent.ContentChoice{
					Completion: gent.ModelCompletion{

						Status:         gent.ModelStatusAnswer,
						ProviderReason: "stop",
					},
					Parts: []gent.ContentPart{
						&gent.AssistantContent{
							Type:      "status",
							MessageId: "status",
							Content: []gent.ContentPart{
								gent.TextContent{
									Text: "Preparing summary.",
								},
							},
						},
					},
				},
			},
			expected{err: "summarization model returned no summary text"},
		},
		{"empty candidate",
			input{
				&gent.ContentChoice{
					Completion: gent.ModelCompletion{Status: gent.ModelStatusAnswer, ProviderReason: "stop"},
				},
			},
			expected{err: "summarization model returned no summary text"}},
		{
			"blank candidate",
			input{
				&gent.ContentChoice{
					Completion: gent.ModelCompletion{

						Status:         gent.ModelStatusAnswer,
						ProviderReason: "stop",
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
								gent.TextContent{
									Text: `
	 `,
								},
							},
						},
					},
				},
			},
			expected{err: "summarization model returned no summary text"},
		},
		{
			"truncated summary",
			input{
				&gent.ContentChoice{

					Completion: gent.ModelCompletion{

						Status:         gent.ModelStatusIncomplete,
						ProviderReason: "max_output_tokens",
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

								gent.TextContent{Text: "The order is"},
							},
						},
					},
				},
			},
			expected{err: "summarization model returned incomplete instead of a completed summary"},
		},
		{
			"tool request with text",
			input{
				&gent.ContentChoice{

					Completion: gent.ModelCompletion{

						Status:         gent.ModelStatusToolCalls,
						ProviderReason: "tool_calls",
					},
					CandidateSource: &gent.ContentSource{
						MessageIds: []string{
							"answer",
						},
					},
					Parts: append(
						[]gent.ContentPart{
							&gent.AssistantContent{
								Type:      gent.AssistantContentFinalAnswerCandidate,
								MessageId: "answer",
								Content: []gent.ContentPart{

									gent.TextContent{Text: "Check payment first."},
								},
							},
						},
						[]gent.ContentPart{&gent.ModelToolCallRequest{ID: "call-1", Type: "function",
							FunctionCall: &gent.FunctionCall{Name: "lookup", Arguments: `{}`}}}...,
					),
				},
			},
			expected{err: "summarization model returned tool_calls instead of a completed summary"},
		},
		{
			"continuation with text",
			input{
				&gent.ContentChoice{

					Continuation: &gent.ProviderContinuation{Provider: "fixture"},
					Completion: gent.ModelCompletion{

						Status:         gent.ModelStatusContinue,
						ProviderReason: "pause_turn",
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

								gent.TextContent{Text: "The order is"},
							},
						},
					},
				},
			},
			expected{err: "summarization model returned continue instead of a completed summary"},
		},
		{
			"unknown completion state",
			input{
				&gent.ContentChoice{

					Completion: gent.ModelCompletion{

						Status:         gent.ModelStatusFailed,
						ProviderReason: "unknown",
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

								gent.TextContent{Text: "The order is paid."},
							},
						},
					},
				},
			},
			expected{err: "summarization model returned failed instead of a completed summary"},
		},
		{
			"answer with tool calls",
			input{
				&gent.ContentChoice{

					Completion: gent.ModelCompletion{
						Status: gent.ModelStatusAnswer,
					},
					CandidateSource: &gent.ContentSource{
						MessageIds: []string{
							"answer",
						},
					},
					Parts: append(
						[]gent.ContentPart{
							&gent.AssistantContent{
								Type:      gent.AssistantContentFinalAnswerCandidate,
								MessageId: "answer",
								Content: []gent.ContentPart{
									gent.TextContent{
										Text: "Check payment first.",
									},
								},
							},
						},
						[]gent.ContentPart{
							&gent.ModelToolCallRequest{
								ID:           "call-1",
								Type:         "function",
								FunctionCall: &gent.FunctionCall{Name: "lookup", Arguments: `{}`},
							},
						}...,
					),
				},
			},
			expected{modelFailure: true, err: "gent: answer completion contains tool calls"},
		},
		{
			"answer with refusal data",
			input{
				&gent.ContentChoice{

					Completion: gent.ModelCompletion{
						Status: gent.ModelStatusAnswer,
						Refusal: &gent.ModelRefusal{
							Code: "blocked",
						},
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
								gent.TextContent{
									Text: "Check payment first.",
								},
							},
						},
					},
				},
			},
			expected{modelFailure: true, err: `gent: completion "answer" has inconsistent refusal data`},
		},
		{"invalid custom state", input{&gent.ContentChoice{
			Completion: gent.ModelCompletion{Status: "custom"},
		}}, expected{modelFailure: true, err: `gent: unknown model completion "custom"`}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			execCtx := tt.NewContext(t, &gent.Config{})
			defer execCtx.Release()
			if !assert.NoError(t, execCtx.AppendRecord(&gent.HistoryEntry{
				Messages: []*gent.MessageContent{{Role: gent.ChatMessageTypeHuman,
					Parts: []gent.ContentPart{gent.TextContent{Text: "The order is paid."}}}},
			}, true)) {
				return
			}
			// This unit test inspects an unsaved proposal. It does not create an execution savepoint.
			original, err := execCtx.Scratchpad()
			if !assert.NoError(t, err) {
				return
			}
			originalBytes, err := execCtx.Codecs().Marshal(original)
			if !assert.NoError(t, err) {
				return
			}
			draft := execCtx.ContextDraft()
			selection, err := execCtx.CompactionInput()
			if !assert.NoError(t, err) {
				return
			}
			model := tt.NewMockModel().AddRawResponse(&gent.ModelCallResponse{
				Choices: []*gent.ContentChoice{test.input.choice},
			})
			strategy := compaction.NewSummarization(model, format.NewXML())
			update, err := strategy.Compact(execCtx, selection)
			if test.expected.err != "" {
				wantError := test.expected.err
				if test.expected.modelFailure {
					callId := execCtx.ContextId() + ":model:1"
					wantError = fmt.Sprintf("summarization model call: gent: model stream/response "+
						"for call %q in run %q turn %q context %q: gent: model call %q choice 0: %s",
						callId, execCtx.RunId(), execCtx.TurnId(), execCtx.ContextId(), callId, wantError)
				}
				assert.EqualError(t, err, wantError)
				assert.Nil(t, update)
				active, readErr := execCtx.Scratchpad()
				assert.NoError(t, readErr)
				assert.Equal(t, original, active)
				assert.Equal(t, draft, execCtx.ContextDraft())
				return
			}
			if !assert.NoError(t, err) || !assert.NoError(t, execCtx.UpdateHistory(*update)) {
				return
			}
			active, err := execCtx.Scratchpad()
			if !assert.NoError(t, err) || !assert.Len(t, active, 1) {
				return
			}
			assert.Equal(t, []*gent.MessageContent{{Role: gent.ChatMessageTypeAI,
				Parts: []gent.ContentPart{gent.TextContent{Text: test.expected.summary}}}},
				active[0].Messages)
			assert.Equal(t, update.Active, execCtx.ContextDraft().Active)
			// Compact proposes a selection; it must not mutate the borrowed source records.
			afterBytes, err := execCtx.Codecs().Marshal(original)
			assert.NoError(t, err)
			assert.Equal(t, originalBytes, afterBytes)
		})
	}
}
