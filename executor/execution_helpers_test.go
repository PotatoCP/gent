package executor_test

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/output"
	"github.com/stretchr/testify/require"
)

type functionLoop struct {
	next func(*gent.ExecutionContext) (*gent.AgentLoopResult, error)
}

func (*functionLoop) Output() gent.Output[string] { return output.NewTextOutput() }
func (loop *functionLoop) Next(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
	return loop.next(execCtx)
}

func acceptTestOutput(execCtx *gent.ExecutionContext, text string) (*gent.AgentLoopResult, error) {
	return gent.AcceptOutput(execCtx, &gent.OutputResult[string]{Accepted: true, Value: text,
		Content: []gent.ContentPart{gent.TextPart(text)}}, nil)
}

type executionRecorder struct {
	codecs            *gent.CodecRegistry
	view              *gent.RunView
	batches           [][]byte
	runStateSnapshots [][]byte
	afterSave         func(*gent.SaveBatch)
}

func (store *executionRecorder) Save(_ context.Context, batch *gent.SaveBatch,
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
	result, err := store.view.Apply(&owned, store.codecs)
	if err != nil {
		return err
	}
	if result.Status != gent.ApplyApplied {
		panic(result.Detail)
	}
	state, err := capture()
	if err != nil {
		return err
	}
	saved, err := store.codecs.MarshalRunState(state)
	if err != nil {
		return err
	}
	store.runStateSnapshots = append(store.runStateSnapshots, saved)
	store.batches = append(store.batches, data)
	if store.afterSave != nil {
		store.afterSave(batch)
	}
	return nil
}

func (store *executionRecorder) latest(t testing.TB, codecs *gent.CodecRegistry) *gent.RunState {
	t.Helper()
	loaded, err := codecs.UnmarshalRunState(store.runStateSnapshots[len(store.runStateSnapshots)-1])
	require.NoError(t, err)
	require.Equal(t, gent.RunStateReady, loaded.Status)
	return loaded.State
}
