package multiturntest

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/stretchr/testify/assert"
)

// These are native transport requests, including the original answer and its native phase. The
// processed answer is for delivery; substituting it into history would fail these comparisons.
func TestProviderNewTurnRetainsNativeAnswerAfterRestore(t *testing.T) {
	type input struct{ provider, restore string }
	type expected struct{ field, messages string }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	var cases []testCase
	for _, provider := range testProviders {
		want := expected{"messages", `[{"role":"user","content":"First task."},
{"role":"assistant","content":"<answer>Done.</answer>"},
{"role":"user","content":"Next task."}]`}
		switch provider {
		case "openai", "xai":
			want = expected{"input", `[{"role":"user","content":[
{"type":"input_text","text":"First task."}]},
{"type":"message","role":"assistant","phase":"final_answer","content":[
{"type":"output_text","text":"<answer>Done.</answer>"}]},
{"role":"user","content":[{"type":"input_text","text":"Next task."}]}]`}
		case "anthropic":
			want.messages = `[{"role":"user","content":[{"type":"text","text":"First task."}]},
{"role":"assistant","content":[{"type":"text","text":"<answer>Done.</answer>"}]},
{"role":"user","content":[{"type":"text","text":"Next task."}]}]`
		case "gemini", "vertexai":
			want = expected{"contents", `[{"role":"user","parts":[{"text":"First task."}]},
{"role":"model","parts":[{"text":"<answer>Done."},{"text":"</answer>"}]},
{"role":"user","parts":[{"text":"Next task."}]}]`}
		}
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
			codecs := gent.NewCodecRegistry()
			recorder := &memoryRecorder{codecs: codecs}
			config := &gent.Config{Codecs: codecs, Recorder: recorder}
			runner := executor.NewRunner(react.NewWithOutput(fixtureModel(test.input.provider, client),
				&transformedOutput{}), config)
			execCtx, err := runner.NewExecutionContext(context.Background(), "provider conversation")
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			runId := execCtx.RunId()
			value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("First task.")))
			if !assert.NoError(t, err) {
				return
			}
			assert.Equal(t, "Processed: <answer>Done.</answer>", value)
			assert.Equal(t, gent.ExecutionFinished, result.Status)
			firstTurnId := result.TurnId
			if test.input.restore != "same context" {
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
					if !assert.NoError(t, codecs.Unmarshal(data, &batch)) {
						return
					}
					applied, err := state.Apply(&batch, codecs)
					if !assert.NoError(t, err) || !assert.Equal(t,
						&gent.ApplyStateResult{Status: gent.ApplyApplied}, applied) {
						return
					}
				}
				execCtx.Release()
				// No provider object, Loop, or context from the earlier process is reused.
				runner = executor.NewRunner(react.NewWithOutput(fixtureModel(test.input.provider, client),
					&transformedOutput{}), config)
				restored, err := runner.Restore(context.Background(), state)
				if !assert.NoError(t, err) || !assert.Equal(t, gent.RestoreReady, restored.Status) {
					return
				}
				execCtx = restored.Context
				defer execCtx.Release()
				assert.Len(t, requests, 1, "Restore must not call the provider")
			}
			value, result, err = runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Next task.")))
			if !assert.NoError(t, err) || !assert.Len(t, requests, 2) {
				return
			}
			assert.Equal(t, runId, execCtx.RunId())
			assert.NotEqual(t, firstTurnId, result.TurnId)
			assert.Equal(t, "Processed: <answer>Done.</answer>", value)
			assert.Equal(t, gent.ExecutionFinished, result.Status)
			assert.JSONEq(t, test.expected.messages, string(requests[1][test.expected.field]))
			// Only the conversation changes. Check every other native request field too.
			wantRequest := requests[0]
			wantRequest[test.expected.field] = requests[1][test.expected.field]
			assert.Equal(t, wantRequest, requests[1])
			full := gent.NewRunState(runId)
			for i, data := range recorder.batches {
				var batch gent.SaveBatch
				assert.NoError(t, codecs.Unmarshal(data, &batch))
				applied, err := full.Apply(&batch, codecs)
				assert.NoError(t, err)
				assert.Equal(t, &gent.ApplyStateResult{Status: gent.ApplyApplied}, applied)
				encoded, err := codecs.MarshalRunState(full)
				assert.NoError(t, err)
				assert.Equal(t, recorder.states[i], encoded)
			}
		})
	}
}
