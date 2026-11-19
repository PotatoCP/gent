package toolchain

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgramParallelOverlapAndOrder(t *testing.T) {
	base, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var started, active, peak atomic.Int32
	ready := make(chan struct{})
	tool := gent.DefineTool("read", func(ctx context.Context, input validationInput) (
		*gent.ToolExecutionResponse, error,
	) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		if started.Add(1) == 2 {
			close(ready)
		}
		select {
		case <-ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return capturedToolResponse(input.ID, nil)
	}).MustBuild()
	wrapper := NewJsToolChainWrapper(New().RegisterTool(tool))
	var records []*gent.ToolCallRecord
	loop := &toolTestLoop{next: func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
		var err error
		records, err = executeTestCode(wrapper, execCtx,
			`const results=tool.parallelCall([{tool:"read",args:{id:"first"}},
{tool:"read",args:{id:"second"}}]);
console.log(results.map(r=>r.parts[0].text).join(","));`, testFormat())
		if err != nil {
			return nil, err
		}
		return toolTestDone(execCtx)
	}}
	runner, ctx, _ := newToolTestRun(t, base, loop, &gent.Config{ToolChain: wrapper})
	_, _, err := runner.NewTurn(ctx, gent.NewTask(gent.TextPart("Read two records in parallel.")))
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, int32(2), peak.Load())
	assert.Equal(t, int32(0), active.Load())
	parts := records[0].Response().Parts()
	assert.Equal(t, []gent.ContentPart{gent.TextPart("first"), parts[2], gent.TextPart("second"),
		gent.TextPart("<output>\nfirst,second\n</output>")}, parts[1:])
	assert.Equal(t, int64(2), ctx.Stats().GetCounter(gent.SCToolCallsFor+"read"))
}

func TestInterruptedProgramNeverRestarts(t *testing.T) {
	type input struct{ parallel bool }
	type expected struct {
		started int32
		checks  int32
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"sequential", input{}, expected{1, 1}},
		{"parallel", input{true}, expected{2, 2}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			var started, active, checks atomic.Int32
			build := func() *JsToolChainWrapper {
				tool := gent.DefineTool("book", func(callCtx context.Context, input validationInput) (
					*gent.ToolExecutionResponse, error,
				) {
					active.Add(1)
					defer active.Add(-1)
					if started.Add(1) == test.expected.started {
						cancel()
					}
					<-callCtx.Done()
					return nil, callCtx.Err()
				}).Recovery(func(_ context.Context, request *gent.ToolRecoveryRequest[validationInput]) (
					*gent.ToolRecoveryResponse, error,
				) {
					checks.Add(1)
					return &gent.ToolRecoveryResponse{Status: gent.ToolRecoveryUnresolved,
						Instructions: "Check booking " + request.Input().ID + " before another action."}, nil
				}).MustBuild()
				return NewJsToolChainWrapper(New().RegisterTool(tool))
			}
			code := `tool.call({tool:"book",args:{id:"first"}});tool.call({tool:"book",args:{id:"later"}});`
			if test.input.parallel {
				code = `tool.parallelCall([{tool:"book",args:{id:"first"}},
{tool:"book",args:{id:"second"}}]);tool.call({tool:"book",args:{id:"later"}});`
			}
			chain := build()
			loop := &toolTestLoop{next: func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
				if _, err := executeTestCode(chain, execCtx, code, testFormat()); err != nil {
					return nil, err
				}
				return toolTestDone(execCtx)
			}}
			runner, ctx, store := newToolTestRun(t, base, loop, &gent.Config{ToolChain: chain})
			_, _, err := runner.NewTurn(ctx, gent.NewTask(gent.TextPart("Book the requested items.")))
			assert.ErrorIs(t, err, context.Canceled)
			assert.Equal(t, int32(0), active.Load())
			freshChain := build()
			var plan *gent.ToolPlan
			freshLoop := planTestLoop(freshChain, nil, &plan)
			freshRunner, fresh := restoreToolTest(t, freshLoop, &gent.Config{
				Codecs: gent.NewCodecRegistry(), ToolChain: freshChain}, store.runStateSnapshot)
			_, completed, err := freshRunner.Continue(fresh)
			require.NoError(t, err)
			require.Equal(t, gent.ExecutionFinished, completed.Status)
			result := plan.Results[0]
			assert.Equal(t, test.expected, expected{started.Load(), checks.Load()})
			assert.Equal(t, gent.ToolResponseRecovery, result.Kind())
			assert.Equal(t, gent.ToolRecoveryUnresolved, result.Recovery().Status)
			assert.Equal(t, "JavaScript execution was interrupted. The script was not restarted.\nScript:\n"+
				code+"\nReview the results and make a new plan.", result.Recovery().Instructions)
			assertProgramStateCleared(t, fresh)
		})
	}
}

func assertProgramStateCleared(t *testing.T, execCtx *gent.ExecutionContext) {
	t.Helper()
	for _, key := range []string{"gent:program", "gent:program_calls"} {
		value, found, err := execCtx.CheckpointState(key)
		assert.NoError(t, err)
		assert.False(t, found)
		assert.Nil(t, value)
	}
}

type sequentialProgramChain struct{ gent.ToolChain }
type disabledParallelProgramChain struct{ gent.ToolChain }

func (*disabledParallelProgramChain) SupportsParallelCalls() bool { return false }

func TestParallelRequiresExplicitSupport(t *testing.T) {
	type input struct {
		wrap func(gent.ToolChain) gent.ToolChain
	}
	type expected struct {
		calls  int
		output string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	output := `<output>
TypeError: tool.parallelCall requires a ParallelToolChain

</output>`
	cases := []testCase{
		{"no interface",
			input{func(c gent.ToolChain) gent.ToolChain { return &sequentialProgramChain{c} }},
			expected{0, output}},
		{"disabled",
			input{func(c gent.ToolChain) gent.ToolChain { return &disabledParallelProgramChain{c} }},
			expected{0, output}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			chain := New().RegisterTool(gent.DefineTool("read", func(context.Context, struct{}) (
				*gent.ToolExecutionResponse, error,
			) {
				calls++
				return capturedToolResponse("read", nil)
			}).MustBuild())
			wrapper := NewJsToolChainWrapper(test.input.wrap(chain))
			runToolScenario(t, wrapper, &gent.Config{}, func(execCtx *gent.ExecutionContext) {
				records, err := executeTestCode(wrapper, execCtx,
					`tool.parallelCall([{tool:"read",args:{}},{tool:"read",args:{}}]);`, testFormat())
				if !assert.NoError(t, err) {
					return
				}
				assert.Equal(t, gent.ToolResponseExecution, records[0].Kind())
				assert.Equal(t, &gent.ToolExecutionResponse{Status: gent.ToolExecuted,
					Parts: []gent.ContentPart{gent.TextPart(test.expected.output)}}, records[0].Execution())
				assert.Equal(t, test.expected.calls, calls)
			})
		})
	}
}

func TestParallelPanicStopsAndJoinsWorkers(t *testing.T) {
	base, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready := make(chan struct{})
	var active atomic.Int32
	chain := New()
	chain.RegisterTool(gent.DefineTool("wait", func(callCtx context.Context, _ struct{}) (
		*gent.ToolExecutionResponse, error,
	) {
		active.Add(1)
		defer active.Add(-1)
		close(ready)
		<-callCtx.Done()
		return nil, callCtx.Err()
	}).MustBuild())
	chain.RegisterTool(gent.DefineTool("fail", func(callCtx context.Context, _ struct{}) (
		*gent.ToolExecutionResponse, error,
	) {
		select {
		case <-ready:
		case <-callCtx.Done():
			return nil, callCtx.Err()
		}
		panic("tool failed")
	}).MustBuild())
	wrapper := NewJsToolChainWrapper(chain)
	loop := &toolTestLoop{next: func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
		_, err := executeTestCode(wrapper, execCtx,
			`tool.parallelCall([{tool:"wait",args:{}},{tool:"fail",args:{}}]);`, testFormat())
		return nil, err
	}}
	runner, ctx, _ := newToolTestRun(t, base, loop, &gent.Config{ToolChain: wrapper})
	assert.PanicsWithValue(t, "tool failed", func() {
		_, _, err := runner.NewTurn(ctx, gent.NewTask(gent.TextPart("Run parallel tools.")))
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	})
	assert.Equal(t, int32(0), active.Load())
	assert.ErrorIs(t, ctx.Context().Err(), context.Canceled)
	assert.EqualError(t, context.Cause(ctx.Context()), "gent: execution pause requested")
}
