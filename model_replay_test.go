package gent_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

func readModelCall(
	t *testing.T,
	execCtx *gent.ExecutionContext,
	model gent.Model,
	call gent.ModelCall,
) (
	*gent.ModelCallResponse,
	[]gent.StreamChunk,
) {
	t.Helper()
	stream, err := execCtx.GenerateContentStream(model, call, gent.NewModelCallRequest(nil))
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	defer stream.Close()
	var chunks []gent.StreamChunk
	for chunk := range stream.Chunks() {
		chunks = append(chunks, chunk)
	}
	response, err := stream.Response()
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	return response, chunks
}

func TestModelReplayCallOrder(t *testing.T) {
	type input struct {
		saved, calls  []gent.ModelCall
		nextIteration bool
	}
	type expected struct {
		answers, ids  []string
		providerCalls int
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{

		{
			"explicit IDs can replay in a different order",
			input{

				saved: []gent.ModelCall{
					{
						Id: "first",
					},
					{
						Id: "second",
					},
				},
				calls: []gent.ModelCall{
					{
						Id: "second",
					},
					{
						Id: "first",
					},
				},
			},
			expected{[]string{"saved 2", "saved 1"}, []string{"second", "first"}, 0},
		},
		{
			"stream labels do not identify the call",
			input{
				saved: []gent.ModelCall{
					{
						Id:       "first",
						StreamId: "old",
					},
				},
				calls: []gent.ModelCall{
					{
						Id:       "first",
						StreamId: "changed",
					},
				},
			},
			expected{
				[]string{
					"saved 1",
				},
				[]string{
					"first",
				},
				0,
			},
		},
		{
			"automatic IDs allocate new operations",
			input{
				saved: []gent.ModelCall{
					{},
					{},
				},
				calls: []gent.ModelCall{
					{},
					{},
				},
			},
			expected{[]string{"new 1", "new 2"}, []string{":model:3", ":model:4"}, 2},
		},
		{
			"a new iteration skips earlier results",
			input{
				saved: []gent.ModelCall{{Id: "first"}},
				calls: []gent.ModelCall{
					{
						Id: "first",
					},
				},
				nextIteration: true,
			},
			expected{
				[]string{
					"new 1",
				},
				[]string{
					"first",
				},
				1,
			},
		},
		{
			"explicit and automatic IDs stay separate",
			input{saved: []gent.ModelCall{{Id: "named"}, {}, {}},
				calls: []gent.ModelCall{{Id: "named"}, {}, {}, {Id: "new"}}},
			expected{
				[]string{
					"saved 1",
					"new 1",
					"new 2",
					"new 3",
				},
				[]string{
					"named",
					":model:3",
					":model:4",
					"new",
				},
				3,
			},
		},
	} {
		t.Run(
			test.name,
			func(t *testing.T) {
				model := &savedReplyModel{}
				for i := range test.input.saved {
					model.responses = append(model.responses, fmt.Sprintf("saved %d", i+1))
				}
				owner, cancel := context.WithCancel(context.Background())
				defer cancel()
				store := newCheckpointStore()
				if test.input.nextIteration {
					store.after = func(batch *gent.SaveBatch) error {
						if batch.Checkpoint.Phase == gent.PhaseIterationCompleted {
							cancel()
						}
						return nil
					}
				}
				loop := contentLoop(func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
					for _, call := range test.input.saved {
						readModelCall(t, execCtx, model, call)
					}
					if test.input.nextIteration {
						return &gent.AgentLoopResult{Action: gent.LAContinue}, nil
					}
					execCtx.Stop()
					return nil, execCtx.Context().Err()
				})
				runner, execCtx := newTestExecution(
					t,
					owner,
					loop,
					&gent.Config{
						Codecs:   store.codecs,
						Recorder: store,
					},
				)
				_, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read results.")))
				if test.input.nextIteration {
					assert.ErrorIs(t, err, context.Canceled)
				} else {
					assert.NoError(t, err)
					assert.Equal(t, gent.ExecutionPaused, result.Status)
				}
				fresh := &savedReplyModel{responses: []string{"new 1", "new 2", "new 3", "new 4"}}
				actual := expected{}
				continued := contentLoop(
					func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
						for _, call := range test.input.calls {
							response, _ := readModelCall(t, execCtx, fresh, call)
							actual.answers = append(
								actual.answers,
								gent.ContentText(
									response.Choices[0].CandidateContent(),
								),
							)
							actual.ids = append(actual.ids, response.Choices[0].CandidateSource.ModelCallId)
						}
						return acceptRootOutput(execCtx, "Done.")
					},
				)
				freshRunner, restored := restoreTestExecution(t, continued, nil, store.runStateSnapshot)
				_, _, err = freshRunner.Continue(restored)
				assert.NoError(t, err)
				actual.providerCalls = fresh.calls
				want := test.expected
				want.ids = append([]string(nil), want.ids...)
				for i, id := range want.ids {
					if id[0] == ':' {
						want.ids[i] = execCtx.ContextId() + id
					}
				}
				assert.Equal(t, want, actual)
			},
		)
	}
}

type savedChunkModel struct {
	response *gent.ModelCallResponse
	chunks   []gent.StreamChunk
	err      error
}

func (model *savedChunkModel) GenerateContentStream(
	context.Context,
	*gent.ModelCallRequest,
) (
	gent.Stream,
	error,
) {
	stream := gent.NewStreamWithDuration()
	for _, chunk := range model.chunks {
		stream.Send(chunk)
	}
	stream.Complete(model.response, model.err)
	return stream, nil
}
func (model *savedChunkModel) PrepareRequest(
	execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest,
	chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}

func TestModelReplayRetriesFailedCall(t *testing.T) {
	type input struct {
		call        gent.ModelCall
		olderResult bool
	}
	type expected struct {
		answer string
		calls  int
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"automatic operation", input{}, expected{"retry", 1}},
		{"explicit ID ignores earlier completed attempt", input{gent.ModelCall{Id: "answer"}, true},
			expected{"retry", 1}},
	} {
		t.Run(
			test.name,
			func(t *testing.T) {
				loop := contentLoop(
					func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
						if test.input.olderResult {
							readModelCall(t, execCtx, &savedReplyModel{responses: []string{"old"}}, test.input.call)
						}
						failed := &savedChunkModel{
							chunks: []gent.StreamChunk{
								checkpointChunk(
									"partial",
								),
							},
							err: context.Canceled,
						}
						stream, err := execCtx.GenerateContentStream(
							failed,
							test.input.call,
							gent.NewModelCallRequest(
								nil,
							),
						)
						if err != nil {
							return nil, err
						}
						_, err = stream.Response()
						stream.Close()
						assert.ErrorIs(t, err, context.Canceled)
						execCtx.Stop()
						return nil, execCtx.Context().Err()
					},
				)
				store := newCheckpointStore()
				runner, execCtx := newTestExecution(
					t,
					context.Background(),
					loop,
					&gent.Config{
						Codecs:   store.codecs,
						Recorder: store,
					},
				)
				_, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read a response.")))
				if !assert.NoError(t, err) {
					return
				}
				assert.Equal(t, gent.ExecutionPaused, result.Status)
				fresh := &savedReplyModel{responses: []string{"retry"}}
				var answer string
				continued := contentLoop(func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
					response, _ := readModelCall(t, execCtx, fresh, test.input.call)
					answer = gent.ContentText(response.Choices[0].CandidateContent())
					return acceptRootOutput(execCtx, answer)
				})
				freshRunner, restored := restoreTestExecution(t, continued, nil, store.runStateSnapshot)
				_, _, err = freshRunner.Continue(restored)
				assert.NoError(t, err)
				assert.Equal(t, test.expected, expected{answer, fresh.calls})
			},
		)
	}
}

func TestModelReplayKeepsStreamAndUsage(t *testing.T) {
	type input struct {
		chunks bool
		retry  bool
	}
	type expected struct {
		providerCalls int
		totalTokens   int64
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"saved chunks", input{chunks: true}, expected{0, 8}},
		{"response without chunks", input{}, expected{0, 8}},
		{"only last physical attempt is replayed", input{chunks: true, retry: true}, expected{0, 10}},
	} {
		t.Run(
			test.name,
			func(t *testing.T) {
				first := recoveryAnswerChoice("hello")
				first.Completion.ProviderReason = "stop"
				first.Parts = append(
					[]gent.ContentPart{
						&gent.AssistantContent{
							Type:      gent.AssistantContentReasoning,
							MessageId: "reasoning",
							Content:   []gent.ContentPart{gent.TextPart("reason")},
						},
					},
					first.Parts...,
				)
				response := &gent.ModelCallResponse{
					Choices: []*gent.ContentChoice{
						first,
						recoveryAnswerChoice(
							"another choice",
						),
					},
					Info: &gent.GenerationInfo{InputTokens: 3, OutputTokens: 5, TotalTokens: 8},
				}
				response.Choices[1].Parts[0].(*gent.AssistantContent).MessageId = "other"
				response.Choices[1].CandidateSource.MessageIds = []string{"other"}
				var chunks []gent.StreamChunk
				if test.input.chunks {
					for _, item := range []*gent.AssistantContent{

						{
							Type:      gent.AssistantContentReasoning,
							MessageId: "reasoning",
							Content: []gent.ContentPart{
								gent.TextPart(
									"reason",
								),
							},
						},
						{
							Type:      gent.AssistantContentFinalAnswerCandidate,
							MessageId: "answer",
							Content: []gent.ContentPart{
								gent.TextPart(
									"he",
								),
							},
						},
						{
							Type:      gent.AssistantContentFinalAnswerCandidate,
							MessageId: "answer",
							Content: []gent.ContentPart{
								gent.TextPart(
									"llo",
								),
							},
						},
					} {
						chunks = append(chunks, gent.StreamChunk{Content: []gent.ContentPart{item}})
					}
				}
				call := gent.ModelCall{Id: "answer", StreamId: "stream", StreamTopicId: "topic"}
				var originalResponse *gent.ModelCallResponse
				var originalChunks []gent.StreamChunk
				loop := contentLoop(
					func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
						if test.input.retry {
							failed := &savedChunkModel{
								response: &gent.ModelCallResponse{
									Info: &gent.GenerationInfo{

										InputTokens:  1,
										OutputTokens: 1,
										TotalTokens:  2,
									},
								},
								chunks: []gent.StreamChunk{
									checkpointChunk(
										"partial",
									),
								},
								err: context.Canceled,
							}
							stream, err := execCtx.GenerateContentStream(failed, call, gent.NewModelCallRequest(nil))
							if err != nil {
								return nil, err
							}
							_, err = stream.Response()
							stream.Close()
							assert.ErrorIs(t, err, context.Canceled)
						}
						originalResponse, originalChunks = readModelCall(
							t,
							execCtx,
							&savedChunkModel{
								response: response,
								chunks:   chunks,
							},
							call,
						)
						execCtx.Stop()
						return nil, execCtx.Context().Err()
					},
				)
				store := newCheckpointStore()
				runner, execCtx := newTestExecution(
					t,
					context.Background(),
					loop,
					&gent.Config{
						Codecs:   store.codecs,
						Recorder: store,
					},
				)
				_, _, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read saved evidence.")))
				if !assert.NoError(t, err) {
					return
				}
				fresh := &savedReplyModel{}
				var replayResponse *gent.ModelCallResponse
				var replayChunks []gent.StreamChunk
				continued := contentLoop(func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
					replayResponse, replayChunks = readModelCall(t, execCtx, fresh, call)
					return acceptRootOutput(execCtx, "Done.")
				})
				freshRunner, restored := restoreTestExecution(t, continued, nil, store.runStateSnapshot)
				_, _, err = freshRunner.Continue(restored)
				assert.NoError(t, err)
				assert.Equal(t, originalResponse, replayResponse)
				assert.Equal(t, originalChunks, replayChunks)
				assert.Equal(
					t,
					test.expected,
					expected{
						fresh.calls,
						restored.Stats().GetCounter(
							gent.SCTotalTokens,
						),
					},
				)
				assert.Equal(
					t,
					execCtx.Stats().GetCounter(
						gent.SCTotalTokens,
					),
					restored.Stats().GetCounter(
						gent.SCTotalTokens,
					),
				)
			},
		)
	}
}
