package multiturntest

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/internal/tt"
	"github.com/stretchr/testify/assert"
)

type memoryRecorder struct {
	codecs    *gent.CodecRegistry
	batches   [][]byte
	states    [][]byte
	phases    []gent.ExecutionPhase
	afterSave func(*gent.SaveBatch)
}

func (recorder *memoryRecorder) Save(ctx context.Context, batch *gent.SaveBatch,
	capture func() (*gent.RunState, error),
) error {
	data, err := recorder.codecs.Marshal(batch)
	if err != nil {
		return err
	}
	state, err := capture()
	if err != nil {
		return err
	}
	saved, err := recorder.codecs.MarshalRunState(state)
	if err != nil {
		return err
	}
	recorder.batches = append(recorder.batches, data)
	recorder.states = append(recorder.states, saved)
	recorder.phases = append(recorder.phases, batch.Checkpoint.Phase)
	if recorder.afterSave != nil {
		recorder.afterSave(batch)
	}
	return nil
}

func TestReActConversationKeepsUserInputAndPreviousAnswers(t *testing.T) {
	model := tt.NewMockModel().AddResponse("First answer", 10, 2).AddResponse("Second answer", 20, 2)
	codecs := gent.NewCodecRegistry()
	recorder := &memoryRecorder{codecs: codecs}
	runner := executor.NewRunner(
		react.NewAgent(
			model,
		),
		&gent.Config{
			Recorder: recorder,
			Codecs:   codecs,
		},
	)
	execCtx, err := runner.NewExecutionContext(context.Background(), "conversation")
	if !assert.NoError(t, err) {
		return
	}
	defer execCtx.Release()
	prefix := gent.TextParts(gent.ChatMessageTypeSystem, "Follow the user's tasks.")
	first := gent.TextParts(gent.ChatMessageTypeHuman, "First task")
	value, result, err := runner.NewTurn(execCtx, &gent.Task{
		Instructions: []*gent.ModelMessage{prefix}, Messages: []*gent.ModelMessage{first},
	})
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, "First answer", value)
	assert.Equal(t, gent.ExecutionFinished, result.Status)
	loaded, err := codecs.UnmarshalRunState(recorder.states[len(recorder.states)-1])
	if !assert.NoError(t, err) || !assert.Equal(t, gent.RunStateReady, loaded.Status) {
		return
	}
	restored, err := runner.Restore(context.Background(), loaded.State)
	if !assert.NoError(t, err) || !assert.Equal(t, gent.RestoreReady, restored.Status) {
		return
	}
	defer restored.Context.Release()
	second := gent.TextParts(gent.ChatMessageTypeHuman, "Second task")
	value, result, err = runner.NewTurn(
		restored.Context,
		&gent.Task{
			Messages: []*gent.ModelMessage{
				second,
			},
		},
	)
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, "Second answer", value)
	assert.Equal(t, gent.ExecutionFinished, result.Status)
	assert.Equal(t, [][]*gent.ModelMessage{
		{prefix, first},
		{prefix, first, &gent.ModelMessage{Role: gent.ChatMessageTypeAI, Parts: []gent.ContentPart{
			&gent.AssistantContent{Type: gent.AssistantContentFinalAnswerCandidate,
				MessageId: firstModelItemId(t, recorder),
				Content:   []gent.ContentPart{gent.TextPart("First answer")}},
		}}, second},
	}, model.CapturedMessages)
	assert.Equal(t, []gent.ExecutionPhase{gent.PhaseInitial, gent.PhaseIterationReady,
		gent.PhaseTerminal, gent.PhaseInitial, gent.PhaseIterationReady, gent.PhaseTerminal},
		recorder.phases)
	full := gent.NewRunState(execCtx.RunId())
	for i, data := range recorder.batches {
		var batch gent.SaveBatch
		if !assert.NoError(t, codecs.Unmarshal(data, &batch)) {
			return
		}
		applied, err := full.Apply(&batch, codecs)
		if !assert.NoError(t, err) || !assert.Equal(t, &gent.ApplyStateResult{
			Status: gent.ApplyApplied}, applied) {
			return
		}
		encoded, err := codecs.MarshalRunState(full)
		assert.NoError(t, err)
		assert.Equal(t, recorder.states[i], encoded)
	}
}

func firstModelItemId(t *testing.T, recorder *memoryRecorder) string {
	t.Helper()
	for _, data := range recorder.batches {
		var batch gent.SaveBatch
		if !assert.NoError(t, recorder.codecs.Unmarshal(data, &batch)) {
			t.FailNow()
		}
		for _, event := range batch.Events {
			if response, ok := event.Payload.(*gent.AfterModelCallEvent); ok {
				return response.Response.Choices[0].Parts[0].(*gent.AssistantContent).MessageId
			}
		}
	}
	t.Fatal("No accepted model response")
	return ""
}
