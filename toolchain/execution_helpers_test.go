package toolchain

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/internal/tt"
	"github.com/rickchristie/gent/output"
	"github.com/stretchr/testify/require"
)

// newExecCtx supplies services for component unit tests. Recovery tests execute a real Loop.
func newExecCtx(t testing.TB, configs ...*gent.Config) *gent.ExecutionContext {
	t.Helper()
	config := &gent.Config{}
	if len(configs) > 0 {
		config = configs[0]
	}
	if config.Codecs == nil {
		config.Codecs = gent.NewCodecRegistry()
	}
	if config.Recorder == nil {
		config.Recorder = &toolTestRecorder{codecs: config.Codecs}
	}
	return tt.NewContext(t, config)
}

type toolTestRecorder struct {
	codecs           *gent.CodecRegistry
	batches          [][]byte
	runStateSnapshot []byte
	capture          bool
}

func (store *toolTestRecorder) Save(_ context.Context, batch *gent.SaveBatch,
	capture func() (*gent.RunState, error),
) error {
	data, err := store.codecs.Marshal(batch)
	if err != nil {
		return err
	}
	if store.capture {
		state, err := capture()
		if err != nil {
			return err
		}
		store.runStateSnapshot, err = store.codecs.MarshalRunState(state)
		if err != nil {
			return err
		}
	}
	store.batches = append(store.batches, data)
	return nil
}

func toolArchive(t testing.TB, execCtx *gent.ExecutionContext) *gent.RunView {
	t.Helper()
	store := execCtx.Config().Recorder.(*toolTestRecorder)
	view := gent.NewRunView(execCtx.RunId())
	for _, data := range store.batches {
		var batch gent.SaveBatch
		require.NoError(t, store.codecs.Unmarshal(data, &batch))
		applied, err := view.Apply(&batch, store.codecs)
		require.NoError(t, err)
		require.Equal(t, gent.ApplyApplied, applied.Status)
	}
	return view
}

func toolEvents(t testing.TB, execCtx *gent.ExecutionContext) []*gent.RecordedEvent {
	t.Helper()
	return toolArchive(t, execCtx).Activity
}

type toolTestLoop struct {
	next func(*gent.ExecutionContext) (*gent.AgentLoopResult, error)
}

func (*toolTestLoop) Output() gent.Output[string] { return output.NewTextOutput() }
func (loop *toolTestLoop) Next(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
	return loop.next(execCtx)
}

func toolTestDone(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
	return gent.AcceptOutput(execCtx, &gent.OutputResult[string]{Accepted: true, Value: "Done",
		Content: []gent.ContentPart{gent.TextPart("Done")}}, nil)
}

func restoreToolTest(t testing.TB, loop *toolTestLoop, config *gent.Config, saved []byte) (
	*executor.Runner[string], *gent.ExecutionContext,
) {
	t.Helper()
	runner := executor.NewRunner[string](loop, config)
	loaded, err := config.Codecs.UnmarshalRunState(saved)
	require.NoError(t, err)
	require.Equal(t, gent.RunStateReady, loaded.Status)
	restored, err := runner.Restore(context.Background(), loaded.State)
	require.NoError(t, err)
	require.Equal(t, gent.RestoreReady, restored.Status)
	t.Cleanup(restored.Context.Release)
	return runner, restored.Context
}

func newToolTestRun(t testing.TB, ctx context.Context, loop *toolTestLoop, config *gent.Config) (
	*executor.Runner[string], *gent.ExecutionContext, *toolTestRecorder,
) {
	t.Helper()
	if config.Codecs == nil {
		config.Codecs = gent.NewCodecRegistry()
	}
	store := &toolTestRecorder{codecs: config.Codecs, capture: true}
	config.Recorder = store
	runner := executor.NewRunner[string](loop, config)
	execCtx, err := runner.NewExecutionContext(ctx, t.Name())
	require.NoError(t, err)
	t.Cleanup(execCtx.Release)
	return runner, execCtx, store
}

// planTestLoop keeps the managed operation identity when Next restarts after restoration.
// It has no custom step enum. Gent supplies accepted results and prepared recovery evidence.
func planTestLoop(chain gent.ToolChain, calls []*gent.ModelToolCallRequest,
	completed **gent.ToolPlan,
) *toolTestLoop {
	return &toolTestLoop{next: func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
		plan, found, err := execCtx.SavedToolPlan()
		if err != nil {
			return nil, err
		}
		if !found {
			plan, err = chain.PrepareCalls(execCtx, calls)
			if err != nil {
				return nil, err
			}
		}
		*completed = plan
		for plan.NextCall < len(plan.Calls) {
			if _, err := chain.ExecuteNext(execCtx, plan, testFormat()); err != nil {
				return nil, err
			}
		}
		return toolTestDone(execCtx)
	}}
}

// runToolScenario executes component assertions inside an admitted turn. Managed tools must save
// their input and results under a real task, even when the fixture needs no model call.
func runToolScenario(t testing.TB, chain gent.ToolChain, config *gent.Config,
	action func(*gent.ExecutionContext),
) *gent.ExecutionContext {
	t.Helper()
	config.ToolChain = chain
	loop := &toolTestLoop{next: func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
		action(execCtx)
		return toolTestDone(execCtx)
	}}
	runner, execCtx, _ := newToolTestRun(t, context.Background(), loop, config)
	_, _, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Run the tool scenario.")))
	require.NoError(t, err)
	return execCtx
}

func runNativeTestCall(t *testing.T, chain gent.ToolChain, config *gent.Config,
	call *gent.ModelToolCallRequest,
) (*gent.ToolCallRecord, *gent.ExecutionContext) {
	t.Helper()
	var record *gent.ToolCallRecord
	execCtx := runToolScenario(t, chain, config, func(execCtx *gent.ExecutionContext) {
		record = executeNativeTestCall(t, chain, execCtx, call)
	})
	return record, execCtx
}
