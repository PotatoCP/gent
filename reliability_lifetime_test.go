package gent_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/executor"
	"github.com/stretchr/testify/assert"
)

type failedSetupLoop struct {
	reviewLoop
	configured *gent.ExecutionContext
	failure    error
}

func (loop *failedSetupLoop) ConfigureExecution(execCtx *gent.ExecutionContext) error {
	loop.configured = execCtx
	return loop.failure
}

func TestReliabilitySetupFailureReleasesContext(t *testing.T) {
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	failure := errors.New("setup failed")
	for range 32 {
		loop := &failedSetupLoop{failure: failure}
		runner := executor.NewRunner[string](loop, &gent.Config{})
		execCtx, err := runner.NewExecutionContext(owner, "test")
		assert.Nil(t, execCtx)
		assert.ErrorIs(t, err, failure)
		if !assert.NotNil(t, loop.configured) {
			return
		}
		// A setup callback can retain its input. The failed handle is permanently disposed.
		_, result, err := runner.NewTurn(loop.configured, gent.NewTask(gent.TextPart("Run.")))
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.NoError(t, owner.Err())
	}
}

func TestReliabilityRestoreFailureKeepsOwnedState(t *testing.T) {
	store := newCheckpointStore()
	loop := contentLoop(func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
		execCtx.Stop()
		return nil, execCtx.Context().Err()
	})
	runner, execCtx := newTestExecution(t, context.Background(), loop, &gent.Config{
		Recorder: store, Codecs: store.codecs})
	_, _, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Pause.")))
	if !assert.NoError(t, err) {
		return
	}
	loaded, err := store.codecs.UnmarshalRunState(store.runStateSnapshot)
	if !assert.NoError(t, err) {
		return
	}
	before, err := store.codecs.MarshalRunState(loaded.State)
	assert.NoError(t, err)
	failure := errors.New("restore setup failed")
	failed := &failedSetupLoop{failure: failure}
	freshRunner := executor.NewRunner[string](failed, &gent.Config{Codecs: store.codecs})
	restored, err := freshRunner.Restore(context.Background(), loaded.State)
	assert.Nil(t, restored)
	assert.ErrorIs(t, err, failure)
	after, err := store.codecs.MarshalRunState(loaded.State)
	assert.NoError(t, err)
	assert.Equal(t, before, after, "failed Restore leaves the caller's state usable")
	_, result, err := freshRunner.Continue(failed.configured)
	assert.Error(t, err)
	assert.Nil(t, result)
}

type captureLifetimeStore struct {
	codecs  *gent.CodecRegistry
	states  []*gent.RunState
	encoded [][]byte
	first   chan *gent.RunState
	release chan struct{}
}

func (store *captureLifetimeStore) Save(_ context.Context, _ *gent.SaveBatch,
	capture func() (*gent.RunState, error),
) error {
	state, err := capture()
	if err != nil {
		return err
	}
	encoded, err := store.codecs.MarshalRunState(state)
	if err != nil {
		return err
	}
	store.states = append(store.states, state)
	store.encoded = append(store.encoded, encoded)
	if len(store.states) == 1 {
		store.first <- state
		<-store.release
	}
	return nil
}

func TestReliabilityRunStateSnapshotsStayStableDuringAcceptance(t *testing.T) {
	store := &captureLifetimeStore{
		codecs: gent.NewCodecRegistry(),
		first: make(
			chan *gent.RunState,
			1,
		),
		release: make(chan struct{}),
	}
	release := sync.OnceFunc(func() { close(store.release) })
	defer release()
	loop := contentLoop(func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
		if err := execCtx.EmitChunk(checkpointChunk("Progress.")); err != nil {
			return nil, err
		}
		if execCtx.Iteration() < 32 {
			return &gent.AgentLoopResult{Action: gent.LAContinue}, nil
		}
		return acceptRootOutput(execCtx, "Done.")
	})
	runner, execCtx := newTestExecution(t, context.Background(), loop, &gent.Config{
		Codecs: store.codecs, Recorder: store})
	done := make(chan error, 1)
	go func() {
		_, _, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Continue work.")))
		done <- err
	}()
	first := checkpointReceive(t, store.first)
	firstEncoded, err := store.codecs.MarshalRunState(first)
	assert.NoError(t, err)
	release()
	for range 32 {
		encoded, err := store.codecs.MarshalRunState(first)
		assert.NoError(t, err)
		assert.Equal(t, firstEncoded, encoded)
	}
	assert.NoError(t, checkpointReceive(t, done))
	assert.Len(t, store.states, 65)
	var previous uint64
	for i, state := range store.states {
		encoded, err := store.codecs.MarshalRunState(state)
		assert.NoError(t, err)
		assert.Equal(t, store.encoded[i], encoded)
		loaded, err := store.codecs.UnmarshalRunState(encoded)
		assert.NoError(t, err)
		assert.Equal(t, gent.RunStateReady, loaded.Status)
		assert.Greater(t, state.LastAppliedEventSequenceNumber(), previous)
		previous = state.LastAppliedEventSequenceNumber()
	}
}
