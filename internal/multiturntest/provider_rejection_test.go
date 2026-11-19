package multiturntest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/output"
	"github.com/stretchr/testify/assert"
)

// A repeated rejection keeps the original native answer, including final_answer phase. Both
// storage paths must resume this same task with the same corrections and accepted counters.
func TestProvidersContinueRepeatedRejectionsAfterRestore(t *testing.T) {
	type input struct{ provider, restore string }
	type expected struct {
		field, messages string
		parts           []gent.ContentPart
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	var cases []testCase
	for _, provider := range testProviders {
		field := "messages"
		parts := []gent.ContentPart{gent.TextPart("<answer>Done.</answer>")}
		prompt := `{"role":"user","content":"First task."}`
		answer := `{"role":"assistant","content":"<answer>Done.</answer>"}`
		correction := `{"role":"user","content":"Revise the answer."}`
		switch provider {
		case "openai", "xai":
			field = "input"
			prompt = `{"role":"user","content":[{"type":"input_text","text":"First task."}]}`
			answer = `{"type":"message","role":"assistant","phase":"final_answer","content":[
{"type":"output_text","text":"<answer>Done.</answer>"}]}`
			correction = `{"role":"user","content":[
{"type":"input_text","text":"Revise the answer."}]}`
		case "anthropic":
			prompt = `{"role":"user","content":[{"type":"text","text":"First task."}]}`
			answer = `{"role":"assistant","content":[{"type":"text","text":"<answer>Done.</answer>"}]}`
			correction = `{"role":"user","content":[{"type":"text","text":"Revise the answer."}]}`
		case "gemini", "vertexai":
			parts = []gent.ContentPart{gent.TextPart("<answer>Done."), gent.TextPart("</answer>")}
			field = "contents"
			prompt = `{"role":"user","parts":[{"text":"First task."}]}`
			answer = `{"role":"model","parts":[{"text":"<answer>Done."},{"text":"</answer>"}]}`
			correction = `{"role":"user","parts":[{"text":"Revise the answer."}]}`
		}
		want := expected{field, fmt.Sprintf("[%s,%s,%s,%s,%s]",
			prompt, answer, correction, answer, correction), parts}
		for _, restore := range []string{"same context", "full batches", "RunStateSnapshot plus tail"} {
			cases = append(cases, testCase{provider + "/" + restore, input{provider, restore}, want})
		}
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			answer, mime := streamFixture(t, test.input.provider, "answer")
			var requests []map[string]json.RawMessage
			client := &http.Client{Transport: responseTransport(func(request *http.Request) (
				*http.Response, error,
			) {
				if request.Method == http.MethodGet {
					return fixtureHTTP(routerFixture, "application/json"), nil
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					return nil, err
				}
				requests = append(requests, body)
				return fixtureHTTP(answer, mime), nil
			})}
			newOutput := func() *output.TextOutput {
				return output.NewTextOutput().SetCustomValidator("two-rejections",
					func(execCtx *gent.ExecutionContext, _ string) (*gent.OutputValidationResult, error) {
						if execCtx.Stats().GetCounter(gent.SCOutputRejectedTotal) < 2 {
							return &gent.OutputValidationResult{Steering: []gent.ContentPart{
								gent.TextPart("Revise the answer.")}}, nil
						}
						return &gent.OutputValidationResult{Accepted: true}, nil
					})
			}
			codecs := gent.NewCodecRegistry()
			recorder := &memoryRecorder{codecs: codecs}
			observer := &contentObserver{preview: make(chan struct{})}
			newRunner := func() *executor.Runner[string] {
				return executor.NewRunner(react.NewWithOutput(
					fixtureModel(test.input.provider, client), newOutput()), &gent.Config{
					Codecs: codecs, Recorder: recorder, Subscribers: []*gent.SubscriberRegistration{
						{Subscriber: observer, Delivery: gent.SubscriberLive}}})
			}
			runner := newRunner()
			execCtx, err := runner.NewExecutionContext(context.Background(), "repeated rejection")
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			runId := execCtx.RunId()
			recorder.afterSave = func(batch *gent.SaveBatch) {
				if batch.Checkpoint.Phase == gent.PhaseIterationCompleted && len(requests) == 2 {
					execCtx.Stop()
				}
			}
			value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("First task.")))
			if !assert.NoError(t, err) {
				return
			}
			turnId := result.TurnId
			assert.Equal(t, "", value)
			assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionPaused, TurnId: turnId,
				TerminationReason: gent.TerminationStopped}, result)
			assert.Len(t, requests, 2)
			observer.mu.Lock()
			for _, event := range observer.events {
				assert.NotEqual(t, "accepted_answer", event.Type)
			}
			observer.mu.Unlock()
			recorder.afterSave = nil
			if test.input.restore != "same context" {
				execCtx.Release()
				codecs = gent.NewCodecRegistry()
				recorder.codecs = codecs
				state := gent.NewRunState(runId)
				start := 0
				if test.input.restore == "RunStateSnapshot plus tail" {
					loaded, err := codecs.UnmarshalRunState(recorder.states[0])
					if !assert.NoError(t, err) || !assert.Equal(t, gent.RunStateReady, loaded.Status) {
						return
					}
					state, start = loaded.State, 1
				}
				for _, data := range recorder.batches[start:] {
					var batch gent.SaveBatch
					assert.NoError(t, codecs.Unmarshal(data, &batch))
					applied, err := state.Apply(&batch, codecs)
					assert.NoError(t, err)
					assert.Equal(t, &gent.ApplyStateResult{Status: gent.ApplyApplied}, applied)
				}
				runner = newRunner()
				restored, err := runner.Restore(context.Background(), state)
				if !assert.NoError(t, err) || !assert.Equal(t, gent.RestoreReady, restored.Status) {
					return
				}
				execCtx = restored.Context
				defer execCtx.Release()
				assert.Len(t, requests, 2, "Restore must not repeat accepted model work")
			}
			assert.Equal(t, int64(2), execCtx.Stats().GetCounter(gent.SCOutputRejectedTotal))
			value, result, err = runner.Continue(execCtx)
			if !assert.NoError(t, err) || !assert.Len(t, requests, 3) {
				return
			}
			assert.Equal(t, "<answer>Done.</answer>", value)
			assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionFinished, TurnId: turnId,
				TerminationReason: gent.TerminationSuccess,
				Output:            test.expected.parts}, result)
			assert.Equal(t, runId, execCtx.RunId())
			assert.JSONEq(t, test.expected.messages, string(requests[2][test.expected.field]))
			wantRequest := requests[0]
			wantRequest[test.expected.field] = requests[2][test.expected.field]
			assert.Equal(t, wantRequest, requests[2])
			assertRejectedArchive(t, codecs, recorder, runId)
		})
	}
}

func assertRejectedArchive(t *testing.T, codecs *gent.CodecRegistry, recorder *memoryRecorder,
	runId string,
) {
	t.Helper()
	view := gent.NewRunView(runId)
	var candidates, corrections []string
	var decisions []bool
	accepted := 0
	for _, data := range recorder.batches {
		var batch gent.SaveBatch
		assert.NoError(t, codecs.Unmarshal(data, &batch))
		result, err := view.Apply(&batch, codecs)
		assert.NoError(t, err)
		assert.Equal(t, &gent.ApplyStateResult{Status: gent.ApplyApplied}, result)
	}
	for _, entry := range view.History.Entries {
		for _, message := range entry.Messages {
			switch message.Role {
			case gent.ChatMessageTypeAI:
				candidates = append(candidates, gent.ContentText(gent.AssistantParts(message.Parts,
					gent.AssistantContentFinalAnswerCandidate)))
			case gent.ChatMessageTypeHuman:
				if entry.Origin != gent.RecordUserInput {
					corrections = append(corrections, gent.ContentText(message.Parts))
				}
			}
		}
	}
	for _, event := range view.Activity {
		if decision, ok := event.Payload.(*gent.OutputValidationEvent); ok &&
			decision.Stage == gent.OutputValidationProcess {
			decisions = append(decisions, decision.Accepted)
		}
		if event.Type == "accepted_answer" {
			accepted++
		}
	}
	assert.Equal(t, []string{"<answer>Done.</answer>", "<answer>Done.</answer>",
		"<answer>Done.</answer>"}, candidates)
	assert.Equal(t, []string{"Revise the answer.", "Revise the answer."}, corrections)
	assert.Equal(t, []bool{false, false, true}, decisions)
	assert.Equal(t, 1, accepted)
}
