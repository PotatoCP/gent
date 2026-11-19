package multiturntest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/internal/tt"
	"github.com/rickchristie/gent/output"
	"github.com/stretchr/testify/assert"
)

func TestAssistantCandidateSelectionAndValidation(t *testing.T) {
	type input struct {
		parts  []gent.ContentPart
		source *gent.ContentSource
	}
	type expected struct {
		candidate []gent.ContentPart
		reasoning []gent.ContentPart
		errorText string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	item := func(kind gent.AssistantContentType, id, text string) *gent.AssistantContent {
		return &gent.AssistantContent{Type: kind, MessageId: id,
			Content: []gent.ContentPart{gent.TextPart(text)}}
	}
	parts := []gent.ContentPart{
		item(gent.AssistantContentReasoning, "r", "Visible reasoning."),
		item(gent.AssistantContentFinalAnswerCandidate, "a", "First."),
		item(gent.AssistantContentCommentary, "c", "Commentary."),
		item(gent.AssistantContentFinalAnswerCandidate, "b", "Second."),
	}
	cases := []testCase{

		{
			"selection uses response order",
			input{
				parts,
				&gent.ContentSource{
					MessageIds: []string{
						"b",
						"a",
					},
				},
			},
			expected{[]gent.ContentPart{gent.TextPart("First."), gent.TextPart("Second.")},
				[]gent.ContentPart{gent.TextPart("Visible reasoning.")}, ""},
		},
		{"no implicit candidate", input{parts, nil}, expected{nil,
			[]gent.ContentPart{gent.TextPart("Visible reasoning.")}, ""}},
		{"commentary is not a candidate", input{parts, &gent.ContentSource{MessageIds: []string{"c"}}},
			expected{[]gent.ContentPart{gent.TextPart("Commentary.")},
				[]gent.ContentPart{gent.TextPart("Visible reasoning.")},
				`gent: invalid candidate source "c": missing, repeated, or not a candidate`}},
		{"missing source", input{parts, &gent.ContentSource{MessageIds: []string{"missing"}}},
			expected{nil, []gent.ContentPart{gent.TextPart("Visible reasoning.")},
				`gent: invalid candidate source "missing": missing, repeated, or not a candidate`}},
		{"provider cannot accept", input{[]gent.ContentPart{
			item(gent.AssistantContentFinalAnswer, "a", "Cannot accept.")}, nil}, expected{nil, nil,
			`gent: provider item "a" cannot establish an accepted answer`}},
		{"nested group", input{[]gent.ContentPart{&gent.AssistantContent{MessageId: "a",
			Content: []gent.ContentPart{item(gent.AssistantContentReasoning, "r", "Nested.")}}}, nil},
			expected{nil, nil, `gent: assistant item "a": nested assistant content or embedded tool part ` +
				`*gent.AssistantContent is not allowed`}},
		{"embedded tool", input{[]gent.ContentPart{&gent.AssistantContent{MessageId: "a",
			Content: []gent.ContentPart{&gent.ModelToolCallRequest{ID: "call"}}}}, nil},
			expected{nil, nil, `gent: assistant item "a": nested assistant content or embedded tool part ` +
				`*gent.ModelToolCallRequest is not allowed`}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			choice := &gent.ContentChoice{Parts: test.input.parts, CandidateSource: test.input.source,
				Completion: gent.ModelCompletion{Status: gent.ModelStatusAnswer}}
			errText := ""
			if err := choice.ValidateCompletion(); err != nil {
				errText = err.Error()
			}
			assert.Equal(t, test.expected, expected{choice.CandidateContent(),
				gent.AssistantParts(choice.Parts, gent.AssistantContentReasoning), errText})
			codecs := gent.NewCodecRegistry()
			encoded, err := codecs.Marshal(choice)
			if !assert.NoError(t, err) {
				return
			}
			var restored gent.ContentChoice
			assert.NoError(t, codecs.Unmarshal(encoded, &restored))
			assert.Equal(t, choice, &restored)
		})
	}
}

type transformedOutput struct{ output.TextOutput }

func (out *transformedOutput) Process(execCtx *gent.ExecutionContext, parts []gent.ContentPart) (
	*gent.OutputResult[string], error,
) {
	processed, err := out.TextOutput.Process(execCtx, parts)
	if err != nil || !processed.Accepted {
		return processed, err
	}
	processed.Value = "Processed: " + processed.Value
	processed.Content = []gent.ContentPart{gent.TextPart(processed.Value)}
	return processed, nil
}

type contentObserver struct {
	mu      sync.Mutex
	events  []*gent.RecordedEvent
	preview chan struct{}
	once    sync.Once
}

func (observer *contentObserver) OnEvent(ctx context.Context, event *gent.RecordedEvent) error {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.events = append(observer.events, event)
	if event.Type == "model_stream_chunk" {
		observer.once.Do(func() { close(observer.preview) })
	}
	return nil
}
func (*contentObserver) OnStop(context.Context, *gent.SubscriberStop) {}

func TestAcceptedAnswerWaitsForSaveAndPreservesOriginal(t *testing.T) {
	codecs := gent.NewCodecRegistry()
	recorder := &memoryRecorder{codecs: codecs}
	entered, allowSave := make(chan struct{}), make(chan struct{})
	recorder.afterSave = func(batch *gent.SaveBatch) {
		if batch.Checkpoint.Phase == gent.PhaseTerminal {
			close(entered)
			<-allowSave
		}
	}
	observer := &contentObserver{preview: make(chan struct{})}
	model := tt.NewMockModel().AddResponse("Original.", 10, 3)
	runner := executor.NewRunner(react.NewWithOutput(model, &transformedOutput{}), &gent.Config{
		Recorder: recorder, Codecs: codecs, Subscribers: []*gent.SubscriberRegistration{{
			Subscriber: observer, Delivery: gent.SubscriberLive}}})
	execCtx, err := runner.NewExecutionContext(context.Background(), "accepted content")
	if !assert.NoError(t, err) {
		return
	}
	defer execCtx.Release()
	type result struct {
		value string
		state *gent.ExecutionResult
		err   error
	}
	done := make(chan result, 1)
	go func() {
		value, state, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Return the answer.")))
		done <- result{value, state, err}
	}()
	// Always release a blocked save before deferred Release joins the attempt.
	defer func() {
		select {
		case <-allowSave:
		default:
			close(allowSave)
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("terminal save did not start")
	}
	select {
	case <-observer.preview:
	case <-time.After(5 * time.Second):
		t.Fatal("preview was not delivered")
	}
	observer.mu.Lock()
	var beforeSave []string
	for _, event := range observer.events {
		if event.Type == "accepted_answer" {
			beforeSave = append(beforeSave, event.Type)
		}
	}
	observer.mu.Unlock()
	assert.Nil(t, beforeSave)
	close(allowSave)
	completed := <-done
	if !assert.NoError(t, completed.err) {
		return
	}
	assert.Equal(t, "Processed: Original.", completed.value)
	assert.Equal(t, gent.ExecutionFinished, completed.state.Status)
	var previewId string
	var publications []*gent.RecordedEvent
	for _, event := range observer.events {
		switch event.Type {
		case "model_stream_chunk":
			chunk := event.Payload.(*gent.StreamChunk)
			item := chunk.Content[0].(*gent.AssistantContent)
			previewId = item.MessageId
			assert.Equal(t, &gent.StreamChunk{Content: []gent.ContentPart{&gent.AssistantContent{
				Type: gent.AssistantContentFinalAnswerCandidate, MessageId: previewId,
				Content: []gent.ContentPart{gent.TextPart("Original.")}}}}, chunk)
			assert.Equal(t, execCtx.RunId(), event.RunId)
			assert.Equal(t, execCtx.TurnId(), event.TurnId)
		case "model_call_finished":
			choice := event.Payload.(*gent.AfterModelCallEvent).Response.Choices[0]
			assert.Equal(t, []gent.ContentPart{gent.TextPart("Original.")}, choice.CandidateContent())
			assert.Equal(t, []string{previewId}, choice.CandidateSource.MessageIds)
		case "accepted_answer":
			publications = append(publications, event)
		}
	}
	if !assert.Len(t, publications, 1) {
		return
	}
	assert.Equal(t, &gent.StreamChunk{Content: []gent.ContentPart{&gent.AssistantContent{
		Type: gent.AssistantContentFinalAnswer, MessageId: execCtx.TurnId() + ":answer",
		Content: []gent.ContentPart{gent.TextPart("Processed: Original.")}}},
		SourceMessageIds: []string{previewId}}, publications[0].Payload)
	var batch gent.SaveBatch
	assert.NoError(t, codecs.Unmarshal(recorder.batches[len(recorder.batches)-1], &batch))
	assert.Equal(t, publications[0], batch.Events[len(batch.Events)-1])
	state, err := codecs.UnmarshalRunState(recorder.states[len(recorder.states)-1])
	if !assert.NoError(t, err) {
		return
	}
	restored, err := runner.Restore(context.Background(), state.State)
	if !assert.NoError(t, err) || !assert.Equal(t, gent.RestoreReady, restored.Status) {
		return
	}
	defer restored.Context.Release()
	last, lastResult, err := runner.GetLastTurnResult(restored.Context)
	assert.NoError(t, err)
	assert.Equal(t, completed.value, last)
	assert.Equal(t, completed.state, lastResult)
	assert.Equal(t, 1, model.CallCount())
}
