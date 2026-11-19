package react

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

func TestRecoveryErrorLimitsAfterWorkerReplacement(t *testing.T) {
	type input struct {
		key       gent.StatKey
		limitType gent.LimitType
		max       float64
	}
	type expected struct {
		checks, alpha, beta int64
		matched             gent.StatKey
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"first recovery error", input{gent.SCToolRecoveryErrorTotal, gent.LimitExactKey, 0},
			expected{1, 1, 0, gent.SCToolRecoveryErrorTotal}},
		{"third recovery error", input{gent.SCToolRecoveryErrorTotal, gent.LimitExactKey, 2},
			expected{3, 1, 2, gent.SCToolRecoveryErrorTotal}},
		{"first named error", input{gent.SCToolRecoveryErrorFor, gent.LimitKeyPrefix, 0},
			expected{1, 1, 0, gent.SCToolRecoveryErrorFor + "alpha"}},
		{"third named error", input{gent.SCToolRecoveryErrorFor, gent.LimitKeyPrefix, 2},
			expected{4, 1, 3, gent.SCToolRecoveryErrorFor + "beta"}},
	}
	for _, test := range cases {
		t.Run(
			test.name,
			func(t *testing.T) {
				worker, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				var starts, checks atomic.Int64
				build := func() gent.ToolChain {
					chain := toolchain.New()
					for _, name := range []string{"alpha", "beta"} {
						chain.RegisterTool(gent.DefineTool(name, func(ctx context.Context, _ struct{}) (
							*gent.ToolExecutionResponse, error,
						) {
							if starts.Add(1) == 4 {
								cancel()
							}
							<-ctx.Done()
							return nil, ctx.Err()
						}).Recovery(func(context.Context, *gent.ToolRecoveryRequest[struct{}]) (
							*gent.ToolRecoveryResponse, error,
						) {
							checks.Add(1)
							return nil, errors.New("private recovery backend failure")
						}).MustBuild())
					}
					return toolchain.NewJsToolChainWrapper(chain)
				}
				first := newMockModel(
					nativeTestResponse(
						&gent.ContentChoice{

							Completion: gent.ModelCompletion{
								Status: gent.ModelStatusToolCalls,
							},
							Parts: []gent.ContentPart{

								nativeTestCall(
									"batch",
									"run_code",
									`{"code": "tool.parallelCall([1,2,3,4].map(n => `+
										`({tool:n===1?'alpha':'beta',args:{}})));"}`,
								),
							},
						},
					),
				)
				limit := gent.Limit{Type: test.input.limitType, Key: test.input.key, MaxValue: test.input.max}
				codecs := gent.NewCodecRegistry()
				recorder := &testRecorder{codecs: codecs}
				config := &gent.Config{ToolChain: build(), Codecs: codecs,
					Recorder: recorder, Limits: []gent.Limit{limit}}
				runner := executor.NewRunner(NewAgent(first), config)
				execCtx, err := runner.NewExecutionContext(worker, "limits")
				if !assert.NoError(t, err) {
					return
				}
				defer execCtx.Release()
				_, result, err := runner.NewTurn(execCtx,
					gent.NewTask(gent.TextPart("Run the independent checks.")))
				if !assert.ErrorIs(t, err, context.Canceled) {
					return
				}
				assert.Nil(t, result)
				assert.Equal(t, int64(4), starts.Load())
				data, err := codecs.MarshalRunState(recorder.state)
				if !assert.NoError(t, err) {
					return
				}
				loaded, err := codecs.UnmarshalRunState(data)
				if !assert.NoError(t, err) || !assert.Equal(t, gent.RunStateReady, loaded.Status) {
					return
				}
				config.ToolChain = build()
				replacement := executor.NewRunner(NewAgent(newMockModel()), config)
				restoredResult, err := replacement.Restore(context.Background(), loaded.State)
				if !assert.NoError(t, err) || !assert.Equal(t, gent.RestoreReady, restoredResult.Status) {
					return
				}
				restored := restoredResult.Context
				defer restored.Release()
				_, result, err = replacement.Continue(restored)
				if !assert.NoError(t, err) || !assert.NotNil(t, result) {
					return
				}
				assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionPaused,
					TurnId: restored.TurnId(), TerminationReason: gent.TerminationLimitExceeded,
					ExceededLimit: &gent.LimitExceeded{Limit: &limit, MatchedKey: test.expected.matched,
						CurrentValue: test.input.max + 1}}, result)
				var matched []gent.StatKey
				for _, event := range recorder.view.Activity {
					if exceeded, ok := event.Payload.(*gent.LimitExceededEvent); ok {
						matched = append(matched, exceeded.MatchedKey)
					}
				}
				assert.Equal(t, []gent.StatKey{test.expected.matched}, matched)
				assert.Equal(t, test.expected.checks, checks.Load())
				assert.Equal(t, test.expected.alpha,
					restored.Stats().GetCounter(gent.SCToolRecoveryErrorFor+"alpha"))
				assert.Equal(t, test.expected.beta,
					restored.Stats().GetCounter(gent.SCToolRecoveryErrorFor+"beta"))
				assert.Equal(t, int64(0), restored.Stats().GetCounter(gent.SCToolCallsErrorTotal))
			},
		)
	}
}
