package gent_test

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/internal/tt"
	"github.com/rickchristie/gent/output"
	"github.com/rickchristie/gent/toolchain"
	"github.com/rickchristie/gent/trace"
	"github.com/stretchr/testify/assert"
)

type contentReply struct {
	choice *gent.ContentChoice
	deltas []string
	err    error
}

type contentModel struct {
	replies  []contentReply
	requests []*gent.ModelCallRequest
	complete func()
}

func (model *contentModel) GenerateContentStream(_ context.Context, request *gent.ModelCallRequest,
) (gent.Stream, error) {
	index := len(model.requests)
	model.requests = append(model.requests, request)
	if index >= len(model.replies) {
		return nil, errors.New("unexpected model request")
	}
	reply := model.replies[index]
	stream := gent.NewStreamWithDuration()
	id, kind := "answer", gent.AssistantContentIntermediary
	for _, part := range reply.choice.Parts {
		if item, ok := part.(*gent.AssistantContent); ok {
			id, kind = item.MessageId, item.Type
			break
		}
	}
	for _, delta := range reply.deltas {
		stream.Send(gent.StreamChunk{Content: []gent.ContentPart{&gent.AssistantContent{
			Type: kind, MessageId: id, Content: []gent.ContentPart{gent.TextPart(delta)}}}})
	}
	if model.complete != nil {
		model.complete()
	}
	stream.Complete(&gent.ModelCallResponse{Choices: []*gent.ContentChoice{reply.choice},
		Info: &gent.GenerationInfo{InputTokens: 2, OutputTokens: 4, TotalTokens: 6}}, reply.err)
	return stream, nil
}

func (model *contentModel) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}

type contentLoop func(*gent.ExecutionContext) (*gent.AgentLoopResult, error)

func (loop contentLoop) Next(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
	return loop(execCtx)
}
func (contentLoop) Output() gent.Output[string] { return output.NewTextOutput() }

type contentObserver struct {
	mu     sync.Mutex
	events []*gent.RecordedEvent
	before func(context.Context, *gent.RecordedEvent) error
}

func (observer *contentObserver) OnEvent(ctx context.Context, event *gent.RecordedEvent) error {
	if observer.before != nil {
		if err := observer.before(ctx, event); err != nil {
			return err
		}
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.events = append(observer.events, event)
	return nil
}
func (*contentObserver) OnStop(context.Context, *gent.SubscriberStop) {}
func (observer *contentObserver) recorded() []*gent.RecordedEvent {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	return append([]*gent.RecordedEvent(nil), observer.events...)
}
func acceptedContent(events []*gent.RecordedEvent) []*gent.StreamChunk {
	var accepted []*gent.StreamChunk
	for _, event := range events {
		if event.Type == "accepted_answer" {
			accepted = append(accepted, event.Payload.(*gent.StreamChunk))
		}
	}
	return accepted
}

func TestExecutionContent_FailuresKeepOnlyPreviews(t *testing.T) {
	providerErr := errors.New("provider failed")
	type input struct {
		status                               gent.ModelStatus
		provider, cancel, reject, tokenLimit bool
	}
	type expected struct {
		reason         gent.TerminationReason
		operationError bool
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"provider failure", input{provider: true}, expected{operationError: true}},
		{"owner cancellation", input{cancel: true}, expected{operationError: true}},
		{"truncated", input{status: gent.ModelStatusIncomplete},
			expected{reason: gent.TerminationLimitExceeded}},
		{"rejected", input{reject: true}, expected{reason: gent.TerminationLimitExceeded}},
		{"token limit", input{tokenLimit: true}, expected{reason: gent.TerminationLimitExceeded}},
		{"unknown completion", input{status: gent.ModelStatusFailed}, expected{operationError: true}},
	}
	for _, test := range cases {
		t.Run(
			test.name,
			func(t *testing.T) {
				owner, cancel := context.WithCancel(context.Background())
				defer cancel()
				choice := recoveryAnswerChoice(`{"status":"pending"}`)
				if test.input.status != "" {
					choice.Completion.Status = test.input.status
				}
				choice.Completion.ProviderReason = "fixture stop"
				reply := contentReply{
					choice: choice,
					deltas: []string{
						gent.ContentText(
							choice.CandidateContent(),
						),
					},
				}
				if test.input.provider {
					reply.err = providerErr
				}
				model := &contentModel{replies: []contentReply{reply}}
				if test.input.cancel {
					model.complete = cancel
				}
				out, err := output.NewJsonOutput[contentAnswer]()
				if !assert.NoError(t, err) {
					return
				}
				if test.input.reject {
					out.SetCustomValidator("status", validateContentAnswer)
				}
				observer := &contentObserver{}
				store := &recoveryStore{codecs: gent.NewCodecRegistry()}
				config := &gent.Config{
					Codecs:   store.codecs,
					Recorder: store,
					Subscribers: []*gent.SubscriberRegistration{
						{
							Subscriber: observer,
							Delivery:   gent.SubscriberLive,
						},
					},
				}
				if test.input.reject || test.input.status == gent.ModelStatusIncomplete {
					config.Limits = []gent.Limit{{Type: gent.LimitExactKey, Key: gent.SCIterations, MaxValue: 1}}
				}
				if test.input.tokenLimit {
					config.Limits = []gent.Limit{{Type: gent.LimitExactKey, Key: gent.SCOutputTokens, MaxValue: 3}}
				}
				runner, execCtx := newTestExecution(t, owner, react.NewWithOutput(model, out), config)
				value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Get the status.")))
				assert.Equal(t, contentAnswer{}, value)
				if test.expected.operationError {
					assert.Error(t, err)
					assert.Nil(t, result)
					if test.input.provider {
						assert.ErrorIs(t, err, providerErr)
					}
					if test.input.cancel {
						assert.ErrorIs(t, err, context.Canceled)
					}
					assert.Nil(t, store.view.Checkpoint.Outcome)
				} else if assert.NoError(t, err) && assert.NotNil(t, result) {
					assert.Equal(t, gent.ExecutionPaused, result.Status)
					assert.Equal(t, test.expected.reason, result.TerminationReason)
					assert.Empty(t, result.Output)
					assert.Equal(t, gent.PhasePaused, store.view.Checkpoint.Phase)
				}
				assert.Empty(t, acceptedContent(observer.recorded()))
				assert.Empty(t, acceptedContent(store.view.Activity))
			},
		)
	}
}

func TestContentIdentity_NewRequestsDoNotReusePreviewIDs(t *testing.T) {
	first, second := tt.NewContext(t, &gent.Config{}), tt.NewContext(t, &gent.Config{})
	model := &contentModel{replies: []contentReply{
		{choice: recoveryAnswerChoice("Partial."), deltas: []string{"Partial."}, err: context.Canceled},
		{choice: recoveryAnswerChoice("Retry."), deltas: []string{"Retry."}},
		{choice: recoveryAnswerChoice("Other run."), deltas: []string{"Other run."}},
	}}
	seen := map[string]bool{}
	for index, owner := range []*gent.ExecutionContext{first, first, second} {
		stream, err := owner.GenerateContentStream(model, gent.ModelCall{Id: "answer"},
			gent.NewModelCallRequest(nil))
		if !assert.NoError(t, err) {
			return
		}
		var ids []string
		for chunk := range stream.Chunks() {
			for _, part := range chunk.Content {
				ids = append(ids, part.(*gent.AssistantContent).MessageId)
			}
		}
		response, err := stream.Response()
		stream.Close()
		if index == 0 {
			assert.ErrorIs(t, err, context.Canceled)
		} else {
			assert.NoError(t, err)
		}
		if !assert.Len(t, ids, 1) {
			return
		}
		assert.Regexp(t, "^"+regexp.QuoteMeta(owner.ContextId())+":message:[a-f0-9]{32}:answer$", ids[0])
		assert.False(t, seen[ids[0]], "each physical call has a distinct item identity")
		seen[ids[0]] = true
		assert.Equal(t, &gent.ContentSource{ModelCallId: "answer", MessageIds: ids},
			response.Choices[0].CandidateSource)
	}
}

func TestExecutionContent_ProviderContinuationIsNotFinal(t *testing.T) {
	continuation := &gent.ProviderContinuation{Provider: "fixture", Items: []json.RawMessage{
		json.RawMessage(`{"type":"server_tool_use","id":"work"}`)}}
	working := &gent.ContentChoice{Completion: gent.ModelCompletion{
		Status: gent.ModelStatusContinue, ProviderReason: "pause_turn"}, Continuation: continuation,
		Parts: []gent.ContentPart{&gent.AssistantContent{Type: gent.AssistantContentCommentary,
			MessageId: "work", Content: []gent.ContentPart{gent.TextPart("Still working.")}}}}
	model := &contentModel{replies: []contentReply{
		{choice: working, deltas: []string{"Still working."}},
		{choice: recoveryAnswerChoice("Done."), deltas: []string{"Done."}},
	}}
	observer := &contentObserver{}
	runner, execCtx := newTestExecution(t, context.Background(), react.NewAgent(model),
		&gent.Config{Subscribers: []*gent.SubscriberRegistration{{Subscriber: observer}}})
	_, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Finish the work.")))
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(
		t,
		&gent.ExecutionResult{
			Status:            gent.ExecutionFinished,
			TurnId:            execCtx.TurnId(),
			TerminationReason: gent.TerminationSuccess,
			Output: []gent.ContentPart{
				gent.TextPart(
					"Done.",
				),
			},
		},
		result,
	)
	if !assert.Len(t, model.requests, 2) {
		return
	}
	assert.Equal(t, continuation, model.requests[1].Messages[1].Continuation)
	accepted := acceptedContent(observer.recorded())
	if assert.Len(t, accepted, 1) {
		assert.Equal(t, "Done.", gent.ContentText(accepted[0].Content))
	}
}

func TestExecutionContent_SubscriberDelayDoesNotLoseAnswer(t *testing.T) {
	entered, release, saved := make(chan struct{}), make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	observer := &contentObserver{before: func(ctx context.Context, event *gent.RecordedEvent) error {
		if event.EventName != "hold" {
			return nil
		}
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	loop := contentLoop(func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
		execCtx.PublishCommonEvent("hold", "Hold delivery while the answer is saved.", nil)
		return acceptRootOutput(execCtx, "Done.")
	})
	recorder := checkpointRecorder(func(_ context.Context, batch *gent.SaveBatch) error {
		if batch.Checkpoint.Phase == gent.PhaseTerminal {
			close(saved)
		}
		return nil
	})
	runner, execCtx := newTestExecution(t, context.Background(), loop, &gent.Config{Recorder: recorder,
		Subscribers: []*gent.SubscriberRegistration{{Subscriber: observer}}})
	done := make(chan error, 1)
	go func() {
		_, _, err := runner.NewTurn(
			execCtx,
			gent.NewTask(
				gent.TextPart(
					"Answer.",
				),
			),
		)
		done <- err
	}()
	checkpointSignal(t, entered)
	checkpointSignal(t, saved)
	assert.Empty(t, acceptedContent(observer.recorded()))
	unblock()
	assert.NoError(t, checkpointReceive(t, done))
	accepted := acceptedContent(observer.recorded())
	if assert.Len(t, accepted, 1) {
		assert.Equal(t, "Done.", gent.ContentText(accepted[0].Content))
	}
}

type contentAnswer struct {
	Status string `json:"status"`
}

func validateContentAnswer(_ *gent.ExecutionContext, answer contentAnswer,
) (*gent.OutputValidationResult, error) {
	if answer.Status == "done" {
		return &gent.OutputValidationResult{Accepted: true}, nil
	}
	return &gent.OutputValidationResult{
		Steering: []gent.ContentPart{
			gent.TextPart(
				"Check the status.",
			),
		},
	}, nil
}

// Fail before storage or lose its acknowledgment after storage. Restoring accepted bytes must
// distinguish these cases without retrying an uncertain old worker or repeating accepted effects.
type contentStore struct {
	mu               sync.Mutex
	view             gent.RunView
	codecs           *gent.CodecRegistry
	runStateSnapshot []byte
	terminal         *gent.SaveBatch
	reached          chan struct{}
	release          chan struct{}
	err              error
	accept, failed   bool
}

func (store *contentStore) Save(ctx context.Context, batch *gent.SaveBatch,
	capture func() (*gent.RunState, error),
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if batch.Checkpoint.Phase == gent.PhaseTerminal && !store.failed {
		store.terminal = batch
		if store.reached != nil {
			close(store.reached)
			select {
			case <-store.release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if store.err != nil {
			store.failed = true
			if !store.accept {
				return store.err
			}
		}
	}
	if store.view.RunId == "" {
		store.view = *gent.NewRunView(batch.RunId)
	}
	if err := applyTestBatch(&store.view, batch, store.codecs); err != nil {
		return err
	}
	state, err := capture()
	if err != nil {
		return err
	}
	store.runStateSnapshot, err = store.codecs.MarshalRunState(state)
	if err != nil {
		return err
	}
	if store.failed && store.err != nil {
		failure := store.err
		store.err = nil
		return failure
	}
	return nil
}
func contentChunks(
	t *testing.T,
	events []*gent.RecordedEvent,
	_ *gent.CodecRegistry,
) []gent.StreamChunk {
	t.Helper()
	var chunks []gent.StreamChunk
	for _, event := range events {
		if event.Type == "model_stream_chunk" || event.Type == "accepted_answer" {
			chunks = append(chunks, *event.Payload.(*gent.StreamChunk))
		}
	}
	return chunks
}

func TestExecutionContent_AcceptSaveAndRestore(t *testing.T) {
	type input struct{ fail, accept bool }
	type expected struct {
		finals       int
		err          error
		resumedCalls int
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	saveErr := errors.New("terminal save failed")
	cases := []testCase{
		{"accepted", input{}, expected{1, nil, 0}},
		{"failed save", input{fail: true}, expected{0, saveErr, 1}},
		{"lost acknowledgment", input{true, true}, expected{0, saveErr, 0}},
	}
	for _, test := range cases {
		t.Run(
			test.name,
			func(t *testing.T) {
				store := &contentStore{codecs: gent.NewCodecRegistry(), reached: make(chan struct{}),
					release: make(chan struct{}), accept: test.input.accept}
				if test.input.fail {
					store.err = saveErr
				}
				unblock := sync.OnceFunc(func() { close(store.release) })
				defer unblock()
				seq, err := trace.NewSequencer("", trace.Config{Codecs: store.codecs,
					IncludeChunkText: true, IncludeRunOutput: true})
				if !assert.NoError(t, err) {
					return
				}
				defer seq.Close()
				observer := &contentObserver{}
				lookup := &gent.ContentChoice{
					Completion: gent.ModelCompletion{
						Status: gent.ModelStatusToolCalls,
					},
					Parts: []gent.ContentPart{
						&gent.AssistantContent{
							MessageId: "lookup",
							Type:      gent.AssistantContentCommentary,
							Content: []gent.ContentPart{
								gent.TextPart(
									"Looking up.",
								),
							},
						},
						&gent.ModelToolCallRequest{ID: "lookup", Type: "function",
							FunctionCall: &gent.FunctionCall{Name: "lookup", Arguments: "{}"}},
					},
				}
				model := &contentModel{
					replies: []contentReply{

						{choice: lookup, deltas: []string{"Looking up."}},
						{
							choice: recoveryAnswerChoice(
								`{"status":"pending"}`,
							),
							deltas: []string{
								`{"status":"pending"}`,
							},
						},
						{choice: recoveryAnswerChoice(` { "status" : "done" } `),
							deltas: []string{` { "status" : `, `"done" } `}},
					},
				}
				out, err := output.NewJsonOutput[contentAnswer]()
				if !assert.NoError(t, err) {
					return
				}
				validations := 0
				out.SetCustomValidator("status", func(execCtx *gent.ExecutionContext, value contentAnswer) (
					*gent.OutputValidationResult, error,
				) {
					validations++
					return validateContentAnswer(execCtx, value)
				})
				toolCalls := 0
				chain := toolchain.New().RegisterTool(
					gent.DefineTool(
						"lookup",
						func(
							context.Context,
							map[string]any,
						) (
							*gent.ToolExecutionResponse,
							error,
						) {
							toolCalls++
							return capturedToolResponse("done", nil)
						},
					).ParameterSchema(nil).MustBuild(),
				)
				runner, execCtx := newTestExecution(t, context.Background(), react.NewWithOutput(model, out),
					&gent.Config{Codecs: store.codecs, Recorder: store, ToolChain: chain,
						Subscribers: []*gent.SubscriberRegistration{
							{Subscriber: observer, Delivery: gent.SubscriberLive}, {Subscriber: seq}}})
				type outcome struct {
					value  contentAnswer
					result *gent.ExecutionResult
					err    error
				}
				done := make(chan outcome, 1)
				started := time.Now()
				go func() {
					value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read the status.")))
					done <- outcome{value, result, err}
				}()
				checkpointSignal(t, store.reached)
				assert.Empty(t, acceptedContent(observer.recorded()))
				assert.Empty(t, seq.Snapshot().Contexts[0].Content)
				unblock()
				finished := checkpointReceive(t, done)
				assert.ErrorIs(t, finished.err, test.expected.err)
				accepted := acceptedContent(observer.recorded())
				assert.Len(t, accepted, test.expected.finals)
				assert.Equal(t, 3, len(model.requests))
				assert.Equal(t, 1, toolCalls)
				assert.Equal(t, 2, validations)
				assert.Equal(t, int64(18), execCtx.Stats().GetCounter(gent.SCTotalTokens))
				assert.Equal(t, int64(1), execCtx.Stats().GetCounter(gent.SCOutputRejectedTotal))
				terminal := acceptedContent(store.terminal.Events)
				if !assert.Len(t, terminal, 1) {
					return
				}
				final := terminal[0]
				assert.Equal(
					t,
					[]gent.ContentPart{
						&gent.AssistantContent{
							Type:      gent.AssistantContentFinalAnswer,
							MessageId: execCtx.TurnId() + ":answer",
							Content: []gent.ContentPart{
								gent.TextPart(
									`{"status":"done"}`,
								),
							},
						},
					},
					final.Content,
				)
				for _, event := range store.terminal.Events {
					if event.Type == "accepted_answer" {
						assert.GreaterOrEqual(t, event.Ts.UnixNano(), started.UnixNano())
					}
				}
				assert.Len(t, final.SourceMessageIds, 1)
				freshModel := &contentModel{}
				if test.expected.resumedCalls > 0 {
					freshModel.replies = []contentReply{{choice: recoveryAnswerChoice(` { "status" : "done" } `)}}
				}
				// A lost acknowledgment still has a completed saved turn. A rejected save does not.
				store.err = nil
				freshRunner, fresh := restoreTestExecution(t, react.NewWithOutput(freshModel, out),
					&gent.Config{Codecs: store.codecs, Recorder: store, ToolChain: chain}, store.runStateSnapshot)
				value, result, err := freshRunner.GetLastTurnResult(fresh)
				if test.expected.resumedCalls > 0 {
					value, result, err = freshRunner.Continue(fresh)
				}
				if !assert.NoError(t, err) {
					return
				}
				assert.Equal(t, contentAnswer{Status: "done"}, value)
				assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionFinished, TurnId: fresh.TurnId(),
					TerminationReason: gent.TerminationSuccess,
					Output:            []gent.ContentPart{gent.TextPart(`{"status":"done"}`)}}, result)
				assert.Len(t, freshModel.requests, test.expected.resumedCalls)
				assert.Equal(t, 1, toolCalls)
				assert.Equal(t, 2+test.expected.resumedCalls, validations)
				var historyText []string
				for _, entry := range store.view.History.Entries {
					for _, message := range entry.Messages {
						if message.Role == gent.ChatMessageTypeAI {
							historyText = append(historyText, gent.ContentText(message.Parts))
						}
					}
				}
				assert.Equal(
					t,
					[]string{
						"Looking up.",
						`{"status":"pending"}`,
						` { "status" : "done" } `,
					},
					historyText,
				)
				replay, err := trace.RestoreSequencer(store.view, trace.Config{Codecs: store.codecs,
					IncludeChunkText: true, IncludeRunOutput: true})
				if !assert.NoError(t, err) {
					return
				}
				defer replay.Close()
				assert.Equal(t, result.Output, gent.AssistantParts(
					replay.Snapshot().Contexts[0].Content, gent.AssistantContentFinalAnswer))
				assert.Equal(t, fresh.TurnId()+":answer", replay.Snapshot().Contexts[0].MessageId)
				// Replaying the same accepted evidence twice does not publish a second answer.
				before := replay.Snapshot()
				for _, event := range store.view.Activity {
					assert.NoError(t, replay.OnEvent(context.Background(), event))
				}
				assert.Equal(t, before, replay.Snapshot())
			},
		)
	}
}
