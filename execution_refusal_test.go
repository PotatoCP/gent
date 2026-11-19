package gent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/output"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

type refusalRecorder struct {
	recoveryStore
	failure     error
	failedBatch string
}

func (store *refusalRecorder) Save(ctx context.Context, batch *gent.SaveBatch,
	capture func() (*gent.RunState, error),
) error {
	if batch.Checkpoint.Phase == gent.PhaseTerminal && store.failure != nil {
		store.failedBatch = batch.BatchId
		return store.failure
	}
	return store.recoveryStore.Save(ctx, batch, capture)
}

func TestNativeRefusalSaveRestoreAndFailures(t *testing.T) {
	type input struct {
		refusal                  *gent.ModelRefusal
		calls                    bool
		providerError, saveError error
	}
	type expected struct {
		refusal *gent.ModelRefusal
		err     error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	blocked := &gent.ModelRefusal{Code: "provider-code", Content: []gent.ContentPart{
		gent.TextPart("The provider declined this request.")}}
	streamErr, saveErr := errors.New("stream failed"), errors.New("save failed")
	cases := []testCase{
		{"no explanation", input{refusal: &gent.ModelRefusal{}}, expected{refusal: &gent.ModelRefusal{}}},
		{"partial calls", input{refusal: blocked, calls: true}, expected{refusal: blocked}},
		{"producer failure", input{refusal: blocked, providerError: streamErr}, expected{err: streamErr}},
		{"save failure", input{refusal: blocked, saveError: saveErr}, expected{err: saveErr}},
	}
	for _, test := range cases {
		t.Run(
			test.name,
			func(t *testing.T) {
				choice := &gent.ContentChoice{Completion: gent.ModelCompletion{Status: gent.ModelStatusRefused,
					Refusal: test.input.refusal}, Parts: []gent.ContentPart{&gent.AssistantContent{
					Type: gent.AssistantContentIntermediary, MessageId: "fragment",
					Content: []gent.ContentPart{gent.TextPart("Provisional fragment.")}}}}
				if test.input.calls {
					choice.Parts = append(choice.Parts, &gent.ModelToolCallRequest{ID: "call", Type: "function",
						FunctionCall: &gent.FunctionCall{Name: "effect", Arguments: "{}"}})
				}
				model := &contentModel{replies: []contentReply{{choice: choice,
					deltas: []string{"Provisional fragment."}, err: test.input.providerError}}}
				validationCalls, toolCalls := 0, 0
				out := output.NewTextOutput().SetCustomValidator("refusal-check", func(
					*gent.ExecutionContext, string,
				) (*gent.OutputValidationResult, error) {
					validationCalls++
					return &gent.OutputValidationResult{Accepted: true}, nil
				})
				chain := toolchain.New().RegisterTool(
					gent.DefineTool(
						"effect",
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
				store := &refusalRecorder{recoveryStore: recoveryStore{codecs: gent.NewCodecRegistry()},
					failure: test.input.saveError}
				observer := &contentObserver{}
				runner, execCtx := newTestExecution(
					t,
					context.Background(),
					react.NewWithOutput(model, out),
					&gent.Config{
						Codecs:    store.codecs,
						Recorder:  store,
						ToolChain: chain,
						Subscribers: []*gent.SubscriberRegistration{
							{
								Subscriber: observer,
								Delivery:   gent.SubscriberLive,
							},
						},
					},
				)
				value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Complete the task.")))
				assert.ErrorIs(t, err, test.expected.err)
				assert.Empty(t, value)
				assert.Len(t, model.requests, 1)
				assert.Equal(t, 0, validationCalls)
				assert.Equal(t, 0, toolCalls)
				assert.Equal(t, int64(0), execCtx.Stats().GetCounter(gent.SCOutputRejectedTotal))
				assert.Empty(t, acceptedContent(observer.recorded()))
				if test.expected.err != nil {
					assert.Nil(t, result)
					assert.Nil(t, store.view.Checkpoint.Outcome)
					return
				}
				expectedResult := &gent.ExecutionResult{
					Status:            gent.ExecutionFinished,
					TurnId:            execCtx.TurnId(),
					TerminationReason: gent.TerminationRefused,
					Refusal:           test.expected.refusal,
				}
				assert.Equal(t, expectedResult, result)
				assert.Equal(t, test.expected.refusal, store.view.Checkpoint.Outcome.Result.Refusal)
				freshModel := &contentModel{}
				freshRunner, fresh := restoreTestExecution(t, react.NewWithOutput(freshModel, out),
					&gent.Config{ToolChain: chain}, store.runStateSnapshot)
				_, restored, err := freshRunner.GetLastTurnResult(fresh)
				assert.NoError(t, err)
				assert.Equal(t, expectedResult, restored)
				assert.Empty(t, freshModel.requests)
				assert.Equal(t, 0, validationCalls)
				assert.Equal(t, 0, toolCalls)
			},
		)
	}
}
