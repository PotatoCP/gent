package gent_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/internal/tt"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

type chainFailureRecorder struct {
	*checkpointStore
	phases []gent.ExecutionPhase
}

func (recorder *chainFailureRecorder) Save(ctx context.Context, batch *gent.SaveBatch,
	capture func() (*gent.RunState, error),
) error {
	if err := recorder.checkpointStore.Save(ctx, batch, capture); err != nil {
		return err
	}
	recorder.phases = append(recorder.phases, batch.Checkpoint.Phase)
	return nil
}

type failingOperationChain struct {
	gent.ToolChain
	prepareFailure  bool
	stop            bool
	failure         error
	operations      int
	successfulCalls int
}

func (chain *failingOperationChain) PrepareCalls(execCtx *gent.ExecutionContext,
	calls []*gent.ModelToolCallRequest,
) (*gent.ToolPlan, error) {
	if chain.prepareFailure {
		chain.operations++
		return nil, chain.failure
	}
	return chain.ToolChain.PrepareCalls(execCtx, calls)
}

func (chain *failingOperationChain) ExecuteNext(execCtx *gent.ExecutionContext,
	plan *gent.ToolPlan, format gent.TextFormat,
) (*gent.ToolCallRecord, error) {
	chain.operations++
	if chain.successfulCalls > 0 {
		chain.successfulCalls--
		return chain.ToolChain.ExecuteNext(execCtx, plan, format)
	}
	if chain.stop {
		execCtx.Stop()
		return nil, errors.Join(execCtx.Context().Err(), chain.failure)
	}
	return nil, chain.failure
}

// A ToolChain operation error is a framework failure. It must not become model guidance or
// an accepted pause merely because cancellation occurs during the same operation.
func TestReActPreservesToolChainFailures(t *testing.T) {
	type input struct {
		prepareFailure bool
		stop           bool
	}
	type expected struct {
		value      string
		result     *gent.ExecutionResult
		causeFound bool
		modelCalls int
		phases     []gent.ExecutionPhase
		steering   string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	want := expected{causeFound: true, modelCalls: 1,
		phases: []gent.ExecutionPhase{gent.PhaseInitial, gent.PhaseIterationReady}}
	cases := []testCase{
		{"prepare failure", input{prepareFailure: true}, want},
		{"execute failure", input{}, want},
		{"execute failure during Stop", input{stop: true}, want},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			failure := errors.New("selected tool chain failed")
			base := toolchain.New().RegisterTool(gent.DefineTool("read",
				func(context.Context, struct{}) (*gent.ToolExecutionResponse, error) {
					return &gent.ToolExecutionResponse{Status: gent.ToolExecuted}, nil
				}).MustBuild())
			chain := &failingOperationChain{ToolChain: base,
				prepareFailure: test.input.prepareFailure, stop: test.input.stop, failure: failure}
			response := &gent.ModelCallResponse{
				Choices: []*gent.ContentChoice{{
					Completion: gent.ModelCompletion{Status: gent.ModelStatusToolCalls},
					Parts: []gent.ContentPart{&gent.ModelToolCallRequest{
						ID: "read-1", Type: "function",
						FunctionCall: &gent.FunctionCall{Name: "read", Arguments: `{}`},
					}},
				}},
			}
			model := tt.NewMockModel().AddRawResponse(response).AddResponse("Done", 0, 0)
			recorder := &chainFailureRecorder{checkpointStore: newCheckpointStore()}
			runner := executor.NewRunner(react.NewAgent(model), &gent.Config{
				ToolChain: chain, Recorder: recorder, Codecs: recorder.codecs,
			})
			execCtx, err := runner.NewExecutionContext(context.Background(), test.name)
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read.")))
			var steering string
			for _, messages := range model.CapturedMessages {
				for _, message := range messages {
					if text := gent.ContentText(message.Parts); text == failure.Error() {
						steering = text
					}
				}
			}
			assert.Equal(t, test.expected, expected{value, result, errors.Is(err, failure),
				model.CallCount(), recorder.phases, steering})
			assert.Equal(t, 1, chain.operations)
			assertChainFailureRecovery(t, recorder, func() *executor.Runner[string] {
				fixedModel := tt.NewMockModel().AddRawResponse(response).
					AddResponse("Recovered", 0, 0)
				return executor.NewRunner(react.NewAgent(fixedModel), &gent.Config{
					Codecs: recorder.codecs, ToolChain: base})
			})
		})
	}
}

type innerFailingOperationChain struct{ *failingOperationChain }

func (chain *innerFailingOperationChain) PrepareCalls(execCtx *gent.ExecutionContext,
	calls []*gent.ModelToolCallRequest,
) (*gent.ToolPlan, error) {
	if calls[0].FunctionCall.Name == "run_code" {
		return chain.ToolChain.PrepareCalls(execCtx, calls)
	}
	return chain.failingOperationChain.PrepareCalls(execCtx, calls)
}

// A generated script can have its own errors. A failed Go tool-chain operation has a separate
// contract and must return to the application, including when run_code owns the outer call.
func TestProgramPreservesToolChainFailures(t *testing.T) {
	type input struct {
		prepareFailure  bool
		hookFailure     bool
		stop            bool
		catch           bool
		successfulCalls int
	}
	type expected struct {
		value      string
		result     *gent.ExecutionResult
		causeFound bool
		modelCalls int
		phases     []gent.ExecutionPhase
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	want := expected{causeFound: true, modelCalls: 1,
		phases: []gent.ExecutionPhase{gent.PhaseInitial, gent.PhaseIterationReady, gent.PhaseToolReady}}
	cases := []testCase{
		{"prepare failure", input{prepareFailure: true}, want},
		{"execute failure", input{}, want},
		{"inner Hook failure", input{hookFailure: true}, want},
		{"execute failure during Stop", input{stop: true}, want},
		{"script cannot catch operation failure", input{catch: true}, want},
		{"failure after accepted inner result", input{successfulCalls: 1}, expected{
			causeFound: true, modelCalls: 1, phases: []gent.ExecutionPhase{
				gent.PhaseInitial, gent.PhaseIterationReady, gent.PhaseToolReady,
				gent.PhaseToolReady, gent.PhaseToolCompleted, gent.PhaseToolCompleted}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			failure := errors.New("inner tool chain failed")
			toolCalls := 0
			base := toolchain.New().RegisterTool(gent.DefineTool("read",
				func(context.Context, struct{}) (*gent.ToolExecutionResponse, error) {
					toolCalls++
					return &gent.ToolExecutionResponse{Status: gent.ToolExecuted}, nil
				}).MustBuild())
			inner := &innerFailingOperationChain{&failingOperationChain{ToolChain: base,
				prepareFailure: test.input.prepareFailure, stop: test.input.stop, failure: failure,
				successfulCalls: test.input.successfulCalls}}
			chain := toolchain.NewJsToolChainWrapper(inner)
			var hooks *gent.Hooks
			if test.input.hookFailure {
				chain = toolchain.NewJsToolChainWrapper(base)
				hooks = &gent.Hooks{BeforeToolCall: func(_ context.Context,
					event *gent.BeforeToolCallEvent,
				) error {
					if event.ToolName == "read" {
						return failure
					}
					return nil
				}}
			}
			code := strings.Repeat(`tool.call({tool:"read",args:{}});`, test.input.successfulCalls+1)
			if test.input.catch {
				code = `try { tool.call({tool:"read",args:{}}); } catch (e) {}
tool.call({tool:"read",args:{}});`
			}
			arguments, err := json.Marshal(codeInputForFailure{Code: code})
			if !assert.NoError(t, err) {
				return
			}
			model := tt.NewMockModel().AddRawResponse(&gent.ModelCallResponse{
				Choices: []*gent.ContentChoice{{
					Completion: gent.ModelCompletion{Status: gent.ModelStatusToolCalls},
					Parts: []gent.ContentPart{&gent.ModelToolCallRequest{
						ID: "program-1", Type: "function",
						FunctionCall: &gent.FunctionCall{Name: "run_code",
							Arguments: string(arguments)},
					}},
				}},
			}).AddResponse("Done", 0, 0)
			recorder := &chainFailureRecorder{checkpointStore: newCheckpointStore()}
			runner := executor.NewRunner(react.NewAgent(model), &gent.Config{
				ToolChain: chain, Recorder: recorder, Codecs: recorder.codecs, Hooks: hooks,
			})
			execCtx, err := runner.NewExecutionContext(context.Background(), test.name)
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read.")))
			assert.Equal(t, test.expected, expected{value, result, errors.Is(err, failure),
				model.CallCount(), recorder.phases})
			if !test.input.hookFailure {
				assert.Equal(t, test.input.successfulCalls+1, inner.operations)
			}
			assert.Equal(t, test.input.successfulCalls, toolCalls)
			assertChainFailureRecovery(t, recorder, func() *executor.Runner[string] {
				return executor.NewRunner(react.NewAgent(tt.NewMockModel().
					AddResponse("Recovered", 0, 0)), &gent.Config{Codecs: recorder.codecs,
					ToolChain: toolchain.NewJsToolChainWrapper(base)})
			})
			assert.Equal(t, test.input.successfulCalls, toolCalls,
				"restoration must not repeat accepted script effects")
		})
	}
}

type codeInputForFailure struct {
	Code string `json:"code"`
}

// Every failure leaves the same accepted state in full batches and the captured RunStateSnapshot.
// The fixed Runner continues that task. It does not admit another task or restart the saved script.
func assertChainFailureRecovery(t *testing.T, store *chainFailureRecorder,
	newRunner func() *executor.Runner[string],
) {
	t.Helper()
	state := gent.NewRunState(store.view.RunId)
	for _, batch := range store.batches {
		data, err := store.codecs.Marshal(batch)
		if !assert.NoError(t, err) {
			return
		}
		var owned gent.SaveBatch
		if !assert.NoError(t, store.codecs.Unmarshal(data, &owned)) {
			return
		}
		applied, err := state.Apply(&owned, store.codecs)
		if !assert.NoError(t, err) || !assert.Equal(t, gent.ApplyApplied, applied.Status) {
			return
		}
	}
	full, err := store.codecs.MarshalRunState(state)
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, store.runStateSnapshot, full)
	for _, data := range [][]byte{full, store.runStateSnapshot} {
		loaded, err := store.codecs.UnmarshalRunState(data)
		if !assert.NoError(t, err) || !assert.Equal(t, gent.RunStateReady, loaded.Status) {
			return
		}
		runner := newRunner()
		restored, err := runner.Restore(context.Background(), loaded.State)
		if !assert.NoError(t, err) || !assert.Equal(t, gent.RestoreReady, restored.Status) {
			return
		}
		t.Cleanup(restored.Context.Release)
		turn := restored.Context.TurnId()
		value, result, err := runner.Continue(restored.Context)
		assert.NoError(t, err)
		assert.Equal(t, "Recovered", value)
		assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionFinished, TurnId: turn,
			TerminationReason: gent.TerminationSuccess,
			Output:            []gent.ContentPart{gent.TextPart("Recovered")}}, result)
	}
}
