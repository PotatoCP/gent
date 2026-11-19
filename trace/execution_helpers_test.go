package trace

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/output"
	"github.com/stretchr/testify/require"
)

type traceTestLoop struct {
	next func(*gent.ExecutionContext) (*gent.AgentLoopResult, error)
}

func (*traceTestLoop) Output() gent.Output[string] { return output.NewTextOutput() }
func (loop *traceTestLoop) Next(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
	return loop.next(execCtx)
}
func traceTestDone(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
	return gent.AcceptOutput(execCtx, &gent.OutputResult[string]{Accepted: true, Value: "Done",
		Content: []gent.ContentPart{gent.TextPart("Done")}}, nil)
}

type traceRecorder struct {
	codecs           *gent.CodecRegistry
	view             *gent.RunView
	batches          [][]byte
	runStateSnapshot []byte
}

func (store *traceRecorder) Save(_ context.Context, batch *gent.SaveBatch,
	capture func() (*gent.RunState, error),
) error {
	data, err := store.codecs.Marshal(batch)
	if err != nil {
		return err
	}
	var owned gent.SaveBatch
	if err := store.codecs.Unmarshal(data, &owned); err != nil {
		return err
	}
	if store.view == nil {
		store.view = gent.NewRunView(batch.RunId)
	}
	applied, err := store.view.Apply(&owned, store.codecs)
	if err != nil {
		return err
	}
	if applied.Status != gent.ApplyApplied {
		panic(applied.Detail)
	}
	state, err := capture()
	if err != nil {
		return err
	}
	store.runStateSnapshot, err = store.codecs.MarshalRunState(state)
	if err != nil {
		return err
	}
	store.batches = append(store.batches, data)
	return nil
}

func newTraceRun(t testing.TB, ctx context.Context, loop gent.Loop[string], config *gent.Config) (
	*executor.Runner[string], *gent.ExecutionContext, *traceRecorder,
) {
	t.Helper()
	if config.Codecs == nil {
		config.Codecs = gent.NewCodecRegistry()
	}
	store := &traceRecorder{codecs: config.Codecs}
	config.Recorder = store
	runner := executor.NewRunner[string](loop, config)
	execCtx, err := runner.NewExecutionContext(ctx, t.Name())
	require.NoError(t, err)
	t.Cleanup(execCtx.Release)
	return runner, execCtx, store
}

type traceModel struct {
	chunks   []gent.StreamChunk
	response *gent.ModelCallResponse
}

func (model *traceModel) GenerateContentStream(context.Context, *gent.ModelCallRequest) (
	gent.Stream, error,
) {
	stream := gent.NewStreamWithDuration()
	go func() {
		for _, chunk := range model.chunks {
			if !stream.Send(chunk) {
				break
			}
		}
		stream.Complete(model.response, nil)
	}()
	return stream, nil
}
func (*traceModel) PrepareRequest(execCtx *gent.ExecutionContext, request *gent.ModelCallRequest,
	chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}
