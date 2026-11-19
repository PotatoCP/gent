package gent_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/internal/tt"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

type parallelFailureChain struct {
	*toolchain.Registry
	waitStarted chan struct{}
	failed      chan struct{}
	failure     error
	stop        bool
	handled     bool
}

func (chain *parallelFailureChain) ExecuteNext(execCtx *gent.ExecutionContext,
	plan *gent.ToolPlan, format gent.TextFormat,
) (*gent.ToolCallRecord, error) {
	if plan.Calls[plan.NextCall].Name != "fail" {
		return chain.Registry.ExecuteNext(execCtx, plan, format)
	}
	select {
	case <-chain.waitStarted:
	case <-execCtx.Context().Done():
		return nil, execCtx.Context().Err()
	}
	defer close(chain.failed)
	if chain.handled {
		return chain.Registry.ExecuteNext(execCtx, plan, format)
	}
	if chain.stop {
		execCtx.Stop()
	}
	return nil, chain.failure
}

// A framework failure must cancel and join its peers without owner intervention. A handled
// author-tool error still permits those peers to finish, and Stop still produces a normal pause.
func TestParallelOperationFailureCancelsPeers(t *testing.T) {
	type input struct {
		failure error
		stop    bool
		handled bool
	}
	type expected struct {
		peerExitedBeforeOwnerCancel bool
		value                       string
		status                      gent.ExecutionStatus
		reason                      gent.TerminationReason
		failed                      bool
		causeFound                  bool
		peerCanceled                bool
		modelCalls                  int
		phases                      []gent.ExecutionPhase
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	failure := errors.New("parallel chain operation failed")
	want := expected{peerExitedBeforeOwnerCancel: true, failed: true, causeFound: true,
		peerCanceled: true, modelCalls: 1, phases: []gent.ExecutionPhase{
			gent.PhaseInitial, gent.PhaseIterationReady, gent.PhaseToolReady, gent.PhaseToolReady}}
	cases := []testCase{
		{"operation failure", input{failure: failure}, want},
		{"joined failure", input{failure: errors.Join(context.Canceled, failure)}, want},
		{"cancellation returned before context ends", input{failure: context.Canceled}, want},
		{"failure during Stop", input{failure: failure, stop: true}, want},
		{"Stop only", input{failure: context.Canceled, stop: true}, expected{
			peerExitedBeforeOwnerCancel: true, status: gent.ExecutionPaused,
			reason: gent.TerminationStopped, peerCanceled: true, modelCalls: 1,
			phases: []gent.ExecutionPhase{gent.PhaseInitial, gent.PhaseIterationReady,
				gent.PhaseToolReady, gent.PhaseToolReady, gent.PhasePaused}}},
		{"handled Tool.Call failure", input{failure: failure, handled: true}, expected{
			peerExitedBeforeOwnerCancel: true, value: "Done", status: gent.ExecutionFinished,
			reason: gent.TerminationSuccess, modelCalls: 2,
			phases: []gent.ExecutionPhase{gent.PhaseInitial, gent.PhaseIterationReady,
				gent.PhaseToolReady, gent.PhaseToolReady, gent.PhaseToolReady,
				gent.PhaseToolCompleted, gent.PhaseToolCompleted, gent.PhaseToolCompleted,
				gent.PhaseToolCompleted, gent.PhaseIterationCompleted,
				gent.PhaseIterationReady, gent.PhaseTerminal}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			waitStarted, failed := make(chan struct{}), make(chan struct{})
			allowFinish := make(chan struct{})
			peerDone := make(chan bool, 1)
			base := toolchain.New()
			base.RegisterTool(gent.DefineTool("wait", func(ctx context.Context, _ struct{}) (
				*gent.ToolExecutionResponse, error,
			) {
				close(waitStarted)
				select {
				case <-ctx.Done():
					peerDone <- true
					return nil, ctx.Err()
				case <-allowFinish:
					peerDone <- false
					return &gent.ToolExecutionResponse{Status: gent.ToolExecuted}, nil
				}
			}).MustBuild())
			base.RegisterTool(gent.DefineTool("fail", func(context.Context, struct{}) (
				*gent.ToolExecutionResponse, error,
			) {
				return nil, test.input.failure
			}).MustBuild())
			chain := toolchain.NewJsToolChainWrapper(&parallelFailureChain{
				Registry: base, waitStarted: waitStarted, failed: failed,
				failure: test.input.failure, stop: test.input.stop, handled: test.input.handled,
			})
			model := tt.NewMockModel().AddRawResponse(&gent.ModelCallResponse{
				Choices: []*gent.ContentChoice{{
					Completion: gent.ModelCompletion{Status: gent.ModelStatusToolCalls},
					Parts: []gent.ContentPart{&gent.ModelToolCallRequest{
						ID: "program-1", Type: "function",
						FunctionCall: &gent.FunctionCall{Name: "run_code", Arguments: `{
  "code": "tool.parallelCall([{tool:\"wait\",args:{}},{tool:\"fail\",args:{}}]);"
}`},
					}},
				}},
			}).AddResponse("Done", 0, 0)
			recorder := &chainFailureRecorder{checkpointStore: newCheckpointStore()}
			runner := executor.NewRunner(react.NewAgent(model), &gent.Config{
				ToolChain: chain, Recorder: recorder, Codecs: recorder.codecs,
			})
			execCtx, err := runner.NewExecutionContext(ctx, test.name)
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			type outcome struct {
				value  string
				result *gent.ExecutionResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Run both.")))
				done <- outcome{value, result, err}
			}()
			select {
			case <-failed:
			case <-time.After(5 * time.Second):
				cancel()
				<-done
				t.Fatal("the parallel operation did not reach its result")
			}
			if test.input.handled {
				close(allowFinish)
			}
			var returned outcome
			actual := expected{}
			// Bound the peer's cooperative exit, not later checkpoint encoding and model work.
			// The full execution result below still proves that the Runner joined all workers.
			select {
			case actual.peerCanceled = <-peerDone:
				actual.peerExitedBeforeOwnerCancel = true
			case <-time.After(time.Second):
				cancel()
				actual.peerCanceled = <-peerDone
			}
			returned = <-done
			actual.value, actual.failed = returned.value, returned.err != nil
			actual.causeFound = errors.Is(returned.err, test.input.failure)
			actual.modelCalls, actual.phases = model.CallCount(), recorder.phases
			if returned.result != nil {
				actual.status, actual.reason = returned.result.Status, returned.result.TerminationReason
			}
			assert.Equal(t, test.expected, actual)
			if !test.expected.failed {
				wantResult := &gent.ExecutionResult{Status: test.expected.status,
					TurnId: execCtx.TurnId(), TerminationReason: test.expected.reason}
				if test.expected.status == gent.ExecutionFinished {
					wantResult.Output = []gent.ContentPart{gent.TextPart("Done")}
				}
				assert.Equal(t, wantResult, returned.result)
			}
			if test.expected.failed {
				assert.Nil(t, returned.result)
				assertChainFailureRecovery(t, recorder, func() *executor.Runner[string] {
					return executor.NewRunner(react.NewAgent(tt.NewMockModel().
						AddResponse("Recovered", 0, 0)), &gent.Config{
						Codecs: recorder.codecs, ToolChain: toolchain.NewJsToolChainWrapper(base)})
				})
			}
		})
	}
}
