package models

import (
	"context"
	"crypto/rand"
	"fmt"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/output"
	"github.com/stretchr/testify/assert"
)

// Keep real encoded batches as well as captures. The live check must restore from storage bytes,
// not retain a model session or use the old context as the recovered representation.
type liveConversationStore struct {
	modelTestStore
	batches [][]byte
	states  [][]byte
	stop    func(*gent.SaveBatch)
}

func (store *liveConversationStore) Save(ctx context.Context, batch *gent.SaveBatch,
	capture func() (*gent.RunState, error),
) error {
	data, err := store.codecs.Marshal(batch)
	if err != nil {
		return err
	}
	if err := store.modelTestStore.Save(ctx, batch, capture); err != nil {
		return err
	}
	store.batches = append(store.batches, data)
	store.states = append(store.states, store.saved)
	if store.stop != nil {
		store.stop(batch)
	}
	return nil
}

func (store *liveConversationStore) load(runId string, useCapture bool) (*gent.RunState, error) {
	state := gent.NewRunState(runId)
	start := 0
	if useCapture {
		// Leave a nonempty accepted tail so this path verifies both loading and later Apply.
		index := len(store.states) / 2
		loaded, err := store.codecs.UnmarshalRunState(store.states[index])
		if err != nil {
			return nil, err
		}
		if loaded.Status != gent.RunStateReady {
			return nil, fmt.Errorf("live capture load: %s: %s", loaded.Status, loaded.Detail)
		}
		state, start = loaded.State, index+1
	}
	for _, data := range store.batches[start:] {
		var batch gent.SaveBatch
		if err := store.codecs.Unmarshal(data, &batch); err != nil {
			return nil, err
		}
		applied, err := state.Apply(&batch, store.codecs)
		if err != nil {
			return nil, err
		}
		if applied.Status != gent.ApplyApplied {
			return nil, fmt.Errorf("live batch apply: %s: %s", applied.Status, applied.Detail)
		}
	}
	return state, nil
}

// A live provider can split text at different stream boundaries. Check the complete saved native
// candidate rather than inventing a one-part response. The caller also checks its full text.
func (store *liveConversationStore) lastCandidate() []gent.ContentPart {
	for i := len(store.view.Activity) - 1; i >= 0; i-- {
		if event, ok := store.view.Activity[i].Payload.(*gent.AfterModelCallEvent); ok {
			return event.Response.Choices[0].CandidateContent()
		}
	}
	panic("live conversation has no saved model response")
}

// TestLiveProviderMultiTurn verifies remote acceptance of native answer history after repeated
// rejection, an interrupted turn, and a completed turn. Fixtures elsewhere check exact wire data.
// These checks prove that the real endpoint can continue each saved native representation.
func TestLiveProviderMultiTurn(t *testing.T) {
	type input struct {
		provider   liveProvider
		useCapture bool
	}
	type expected struct {
		answers, rejected int
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	var cases []testCase
	for _, provider := range liveProviders() {
		cases = append(cases,
			testCase{provider.name + "/batches", input{provider, false}, expected{2, 2}},
			testCase{provider.name + "/capture and tail", input{provider, true}, expected{2, 2}})
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			settings := liveConfig(t, test.input.provider, false)
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			codecs := gent.NewCodecRegistry()
			store := &liveConversationStore{modelTestStore: modelTestStore{codecs: codecs}}
			build := func() *executor.Runner[string] {
				out := output.NewTextOutput().SetCustomValidator("repeat-validation",
					func(execCtx *gent.ExecutionContext, _ string) (*gent.OutputValidationResult, error) {
						if execCtx.Stats().GetCounter(gent.SCOutputCustomRejectedTotal) < 2 {
							return &gent.OutputValidationResult{Steering: []gent.ContentPart{
								gent.TextPart("Confirm the saved code. Return only that code again."),
							}}, nil
						}
						return &gent.OutputValidationResult{Accepted: true}, nil
					})
				loop := react.NewWithOutput(newLiveModel(t, test.input.provider, &settings), out).
					WithCallOptions(gent.WithMaxTokens(2048))
				return executor.NewRunner(loop, &gent.Config{
					Codecs: codecs, Recorder: store, Limits: liveLimits()})
			}
			runner := build()
			execCtx, err := runner.NewExecutionContext(ctx, "live multi-turn")
			if !assert.NoError(t, err) {
				return
			}
			t.Cleanup(execCtx.Release)
			runId := execCtx.RunId()
			store.stop = func(batch *gent.SaveBatch) {
				if batch.Checkpoint.Phase == gent.PhaseIterationCompleted &&
					execCtx.Stats().GetCounter(gent.SCOutputCustomRejectedTotal) == 2 {
					execCtx.Stop()
				}
			}
			code := "gent-" + rand.Text()
			value, result, err := runner.NewTurn(execCtx, liveTask(
				"Follow the user instructions. Return exact text with no added formatting.",
				"Remember this code for later: "+code+". Return only that code."))
			if !assert.NoError(t, err) || !assert.NotNil(t, result) {
				return
			}
			turnId := execCtx.TurnId()
			assert.Equal(t, "", value)
			assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionPaused, TurnId: turnId,
				TerminationReason: gent.TerminationStopped}, result)
			store.stop = nil
			restore := func() bool {
				execCtx.Release()
				state, err := store.load(runId, test.input.useCapture)
				if !assert.NoError(t, err) {
					return false
				}
				runner = build()
				restored, err := runner.Restore(ctx, state)
				if !assert.NoError(t, err) || !assert.Equal(t, gent.RestoreReady, restored.Status) {
					return false
				}
				execCtx = restored.Context
				t.Cleanup(execCtx.Release)
				return true
			}
			if !restore() {
				return
			}
			value, result, err = runner.Continue(execCtx)
			if !assert.NoError(t, err) || !assert.NotNil(t, result) {
				return
			}
			assert.Equal(t, code, value)
			assert.Equal(t, code, gent.ContentText(store.lastCandidate()))
			assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionFinished, TurnId: turnId,
				TerminationReason: gent.TerminationSuccess,
				Output:            store.lastCandidate()}, result)
			if !restore() {
				return
			}
			value, result, err = runner.NewTurn(execCtx,
				gent.NewTask(gent.TextPart("What code did I ask you to remember? Return only that code.")))
			if !assert.NoError(t, err) || !assert.NotNil(t, result) {
				return
			}
			assert.Equal(t, code, value)
			assert.Equal(t, code, gent.ContentText(store.lastCandidate()))
			assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionFinished,
				TurnId: execCtx.TurnId(), TerminationReason: gent.TerminationSuccess,
				Output: store.lastCandidate()}, result)
			assert.Equal(t, runId, execCtx.RunId())
			assert.NotEqual(t, turnId, execCtx.TurnId())
			actual := expected{}
			for _, event := range store.view.Activity {
				if event.Type == "accepted_answer" {
					actual.answers++
				}
			}
			actual.rejected = int(execCtx.Stats().GetCounter(gent.SCOutputCustomRejectedTotal))
			assert.Equal(t, test.expected, actual)
		})
	}
}
