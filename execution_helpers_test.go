package gent_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/output"
	"github.com/stretchr/testify/assert"
)

// Register the same concrete values in each process before decoding its stored bytes.
func testConfig[T any](t testing.TB, loop gent.Loop[T], config *gent.Config) *gent.Config {
	t.Helper()
	if config == nil {
		config = &gent.Config{}
	}
	if config.Codecs == nil {
		config.Codecs = gent.NewCodecRegistry()
	}
	if !assert.NoError(t, config.Codecs.RegisterValueType(reflect.TypeFor[T]())) {
		t.FailNow()
	}
	if component, ok := loop.(interface {
		RegisterCodecs(*gent.CodecRegistry) error
	}); ok {
		if !assert.NoError(t, component.RegisterCodecs(config.Codecs)) {
			t.FailNow()
		}
	}
	if config.ToolChain != nil {
		if !assert.NoError(t, config.ToolChain.RegisterCodecs(config.Codecs)) {
			t.FailNow()
		}
	}
	return config
}

func newTestExecution[T any](t testing.TB, ctx context.Context, loop gent.Loop[T],
	config *gent.Config,
) (*executor.Runner[T], *gent.ExecutionContext) {
	t.Helper()
	runner := executor.NewRunner(loop, testConfig(t, loop, config))
	execCtx, err := runner.NewExecutionContext(ctx, t.Name())
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	t.Cleanup(execCtx.Release)
	return runner, execCtx
}

func restoreTestExecution[T any](t testing.TB, loop gent.Loop[T], config *gent.Config,
	saved []byte,
) (*executor.Runner[T], *gent.ExecutionContext) {
	t.Helper()
	config = testConfig(t, loop, config)
	loaded, err := config.Codecs.UnmarshalRunState(saved)
	if !assert.NoError(t, err) || !assert.Equal(t, gent.RunStateReady, loaded.Status, loaded.Detail) {
		t.FailNow()
	}
	runner := executor.NewRunner(loop, config)
	restored, err := runner.Restore(context.Background(), loaded.State)
	if !assert.NoError(
		t,
		err,
	) || !assert.Equal(
		t,
		gent.RestoreReady,
		restored.Status,
		restored.Detail,
	) {
		t.FailNow()
	}
	t.Cleanup(restored.Context.Release)
	return runner, restored.Context
}

type checkpointModel func(context.Context, *gent.ModelCallRequest) (gent.Stream, error)

func (m checkpointModel) GenerateContentStream(
	ctx context.Context, request *gent.ModelCallRequest,
) (gent.Stream, error) {
	return m(ctx, request)
}

func checkpointReceive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before expected value")
		}
		return value
	case <-time.After(10 * time.Second):
		t.Fatal("operation did not complete within ten seconds")
		var zero T
		return zero
	}
}

func checkpointChunk(text string) gent.StreamChunk {
	return gent.StreamChunk{
		Content: []gent.ContentPart{
			&gent.AssistantContent{

				Type:      gent.AssistantContentIntermediary,
				MessageId: "progress",
				Content: []gent.ContentPart{
					gent.TextContent{
						Text: text,
					},
				},
			},
		},
	}
}

type checkpointRecorder func(context.Context, *gent.SaveBatch) error

func (save checkpointRecorder) Save(
	ctx context.Context, batch *gent.SaveBatch,
	captureRunStateSnapshot func() (*gent.RunState, error),
) error {
	return save(ctx, batch)
}

func (m checkpointModel) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}

// The function field keeps fixture configuration outside the saved application data.
type contentLoopValue struct {
	next func(*gent.ExecutionContext) (*gent.AgentLoopResult, error)
}

func (loop *contentLoopValue) Output() gent.Output[string] { return output.NewTextOutput() }
func (loop *contentLoopValue) Next(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
	return loop.next(execCtx)
}
func acceptRootOutput(execCtx *gent.ExecutionContext, value string) (*gent.AgentLoopResult, error) {
	return gent.AcceptOutput(execCtx, &gent.OutputResult[string]{Accepted: true, Value: value,
		Content: []gent.ContentPart{gent.TextPart(value)}}, nil)
}

func checkpointSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		t.Fatal("signal did not arrive within ten seconds")
	}
}

type savedReplyModel struct {
	responses    []string
	calls        int
	responseOnly bool
}

func (model *savedReplyModel) GenerateContentStream(
	context.Context,
	*gent.ModelCallRequest,
) (
	gent.Stream,
	error,
) {
	if model.calls >= len(model.responses) {
		return nil, fmt.Errorf("unexpected model call %d", model.calls+1)
	}
	choice := recoveryAnswerChoice(model.responses[model.calls])
	model.calls++
	stream := gent.NewStreamWithDuration()
	if !model.responseOnly {
		stream.Send(gent.StreamChunk{Content: choice.Parts})
	}
	stream.Complete(&gent.ModelCallResponse{Choices: []*gent.ContentChoice{choice}}, nil)
	return stream, nil
}
func (model *savedReplyModel) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}
