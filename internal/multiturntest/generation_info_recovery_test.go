package multiturntest

import (
	"context"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/internal/tt"
	"github.com/stretchr/testify/assert"
)

func TestGenerationInfoArchiveAndRecovery(t *testing.T) {
	type input struct{ fromRunStateSnapshot bool }
	type expected struct {
		info  gent.GenerationInfo
		value string
		err   error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	want := expected{info: gent.GenerationInfo{
		InputTokens: 20, OutputTokens: 7, TotalTokens: 27,
		CachedInputTokens: 5, CacheWriteInputTokens: 3, ReasoningTokens: 2,
		ProviderRequestId: "request-42", ProviderResponseId: "response-42",
		ResponseModel: "returned-model-v2", ResponseServiceTier: "priority",
		Duration: 5 * time.Second, TimeToFirstContent: 150 * time.Millisecond,
		RawGenerationInfo: map[string]any{"cache_creation_input_tokens": 3,
			"native": map[string]any{"request": "request-42"}},
	}, value: "Done."}
	cases := []testCase{
		{"full batches", input{false}, want},
		{"RunStateSnapshot plus tail", input{true}, want},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			model := tt.NewMockModel().AddRawResponse(&gent.ModelCallResponse{
				Info: &test.expected.info,
				Choices: []*gent.ContentChoice{{
					Parts: []gent.ContentPart{&gent.AssistantContent{
						Type: gent.AssistantContentFinalAnswerCandidate, MessageId: "answer",
						Content: []gent.ContentPart{gent.TextPart(test.expected.value)},
					}},
					CandidateSource: &gent.ContentSource{MessageIds: []string{"answer"}},
					Completion:      gent.ModelCompletion{Status: gent.ModelStatusAnswer},
				}},
			})
			codecs := gent.NewCodecRegistry()
			recorder := &memoryRecorder{codecs: codecs}
			runner := executor.NewRunner(react.NewAgent(model), &gent.Config{
				Codecs: codecs, Recorder: recorder,
			})
			execCtx, err := runner.NewExecutionContext(context.Background(), "generation metadata")
			if !assert.Equal(t, test.expected.err, err) {
				return
			}
			defer execCtx.Release()
			value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Finish.")))
			if !assert.Equal(t, test.expected.err, err) || !assert.NotNil(t, result) {
				return
			}
			assert.Equal(t, test.expected.value, value)
			assert.Equal(t, gent.ExecutionFinished, result.Status)
			state := gent.NewRunState(execCtx.RunId())
			start := 0
			if test.input.fromRunStateSnapshot {
				loaded, err := codecs.UnmarshalRunState(recorder.states[0])
				if !assert.NoError(t, err) || !assert.Equal(t, gent.RunStateReady, loaded.Status) {
					return
				}
				state, start = loaded.State, 1
			}
			view := gent.NewRunView(execCtx.RunId())
			for index, data := range recorder.batches {
				var batch gent.SaveBatch
				if !assert.NoError(t, codecs.Unmarshal(data, &batch)) {
					return
				}
				applied, err := view.Apply(&batch, codecs)
				assert.NoError(t, err)
				assert.Equal(t, &gent.ApplyStateResult{Status: gent.ApplyApplied}, applied)
				if index < start {
					continue
				}
				// Each reducer takes ownership. Decode a separate input for execution replay.
				var stateBatch gent.SaveBatch
				if !assert.NoError(t, codecs.Unmarshal(data, &stateBatch)) {
					return
				}
				applied, err = state.Apply(&stateBatch, codecs)
				assert.NoError(t, err)
				assert.Equal(t, &gent.ApplyStateResult{Status: gent.ApplyApplied}, applied)
			}
			encoded, err := codecs.MarshalRunState(state)
			assert.NoError(t, err)
			assert.Equal(t, recorder.states[len(recorder.states)-1], encoded)
			archive, err := codecs.Marshal(view)
			assert.NoError(t, err)
			var decoded *gent.RunView
			if !assert.NoError(t, codecs.Unmarshal(archive, &decoded)) {
				return
			}
			assert.Equal(t, view.Activity, decoded.Activity)
			var infos []gent.GenerationInfo
			for _, event := range decoded.Activity {
				if after, ok := event.Payload.(*gent.AfterModelCallEvent); ok {
					infos = append(infos, *after.Response.Info)
				}
			}
			assert.Equal(t, []gent.GenerationInfo{test.expected.info}, infos)
			restored, err := runner.Restore(context.Background(), state)
			if !assert.NoError(t, err) || !assert.Equal(t, gent.RestoreReady, restored.Status) {
				return
			}
			defer restored.Context.Release()
			saved, savedResult, err := runner.GetLastTurnResult(restored.Context)
			assert.Equal(t, test.expected.err, err)
			assert.Equal(t, test.expected.value, saved)
			assert.Equal(t, result, savedResult)
			assert.Equal(t, 1, model.CallCount())
			assert.Equal(t, int64(20), restored.Context.Stats().GetTotalInputTokens())
			assert.Equal(t, int64(7), restored.Context.Stats().GetTotalOutputTokens())
		})
	}
}
