package gent_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/stretchr/testify/assert"
)

type checkpointStore struct {
	mu               sync.Mutex
	view             gent.RunView
	batches          []*gent.SaveBatch
	before, after    func(*gent.SaveBatch) error
	codecs           *gent.CodecRegistry
	runStateSnapshot []byte
}

func newCheckpointStore() *checkpointStore {
	return &checkpointStore{
		codecs: gent.NewCodecRegistry(),
	}
}
func (store *checkpointStore) Save(_ context.Context, batch *gent.SaveBatch,
	capture func() (*gent.RunState, error),
) error {
	if store.before != nil {
		if err := store.before(batch); err != nil {
			return err
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	data, err := store.codecs.Marshal(batch)
	if err != nil {
		return err
	}
	var owned gent.SaveBatch
	if err := store.codecs.Unmarshal(data, &owned); err != nil {
		return err
	}
	if store.view.RunId == "" {
		store.view = *gent.NewRunView(batch.RunId)
	}
	applied, err := store.view.Apply(&owned, store.codecs)
	if err != nil {
		return err
	}
	if applied.Status != gent.ApplyApplied {
		panic(applied.Detail)
	}
	store.batches = append(store.batches, &owned)
	state, err := capture()
	if err != nil {
		return err
	}
	store.runStateSnapshot, err = store.codecs.MarshalRunState(state)
	if err != nil {
		return err
	}
	if store.after != nil {
		return store.after(batch)
	}
	return nil
}

func TestCheckpointModelChunksGrouped(t *testing.T) {
	type input struct{ chunks int }
	type expected struct {
		phases []gent.ExecutionPhase
		chunks int
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	phases := []gent.ExecutionPhase{gent.PhaseInitial, gent.PhaseIterationReady, gent.PhaseTerminal}
	for _, test := range []testCase{
		{"small stream", input{4}, expected{phases, 4}},
		{"large stream", input{512}, expected{phases, 512}},
	} {
		t.Run(
			test.name,
			func(t *testing.T) {
				store := newCheckpointStore()
				entered, release := make(chan struct{}), make(chan struct{})
				unblock := sync.OnceFunc(func() { close(release) })
				defer unblock()
				store.before = func(batch *gent.SaveBatch) error {
					if batch.Checkpoint.Phase == gent.PhaseTerminal {
						close(entered)
						<-release
					}
					return nil
				}
				model := checkpointModel(func(context.Context, *gent.ModelCallRequest) (gent.Stream, error) {
					stream := gent.NewStreamWithDuration()
					for range test.input.chunks {
						stream.Send(gent.StreamChunk{Content: []gent.ContentPart{&gent.AssistantContent{
							Type: gent.AssistantContentCommentary, MessageId: "progress",
							Content: []gent.ContentPart{gent.TextPart("Progress.")}}}})
					}
					stream.Complete(&gent.ModelCallResponse{Choices: []*gent.ContentChoice{
						recoveryAnswerChoice("Done.")}}, nil)
					return stream, nil
				})
				runner, execCtx := newTestExecution(t, context.Background(), react.NewAgent(model),
					&gent.Config{Codecs: store.codecs, Recorder: store})
				done := make(chan error, 1)
				go func() {
					_, _, err := runner.NewTurn(
						execCtx,
						gent.NewTask(
							gent.TextPart(
								"Answer.",
							),
						),
					)
					done <- err
				}()
				checkpointSignal(t, entered)
				// Stream size does not create request, response, or threshold saves.
				store.mu.Lock()
				assert.Len(t, store.batches, 2)
				store.mu.Unlock()
				unblock()
				assert.NoError(t, checkpointReceive(t, done))
				actual := expected{}
				for _, batch := range store.batches {
					actual.phases = append(actual.phases, batch.Checkpoint.Phase)
					for _, event := range batch.Events {
						if event.Type == "model_stream_chunk" {
							actual.chunks++
						}
					}
				}
				assert.Equal(t, test.expected, actual)
				assert.Equal(t, uint64(len(store.view.Activity)), store.view.LastAppliedEventSequenceNumber)
			},
		)
	}
}

func TestCheckpointIterationSavePreventsModelWork(t *testing.T) {
	failure := errors.New("before iteration save failed")
	type input struct{ fail bool }
	type expected struct {
		calls int
		err   error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"accepted", input{}, expected{1, nil}},
		{"rejected", input{true}, expected{0, failure}},
	} {
		t.Run(
			test.name,
			func(t *testing.T) {
				store := newCheckpointStore()
				entered, release := make(chan struct{}), make(chan struct{})
				unblock := sync.OnceFunc(func() { close(release) })
				defer unblock()
				store.before = func(batch *gent.SaveBatch) error {
					if batch.Checkpoint.Phase != gent.PhaseIterationReady {
						return nil
					}
					close(entered)
					<-release
					if test.input.fail {
						return failure
					}
					return nil
				}
				model := &contentModel{replies: correctionReplies("Done.")}
				runner, execCtx := newTestExecution(t, context.Background(), react.NewAgent(model),
					&gent.Config{Codecs: store.codecs, Recorder: store})
				done := make(chan error, 1)
				go func() {
					_, _, err := runner.NewTurn(
						execCtx,
						gent.NewTask(
							gent.TextPart(
								"Answer.",
							),
						),
					)
					done <- err
				}()
				checkpointSignal(t, entered)
				assert.Empty(t, model.requests)
				unblock()
				err := checkpointReceive(t, done)
				assert.ErrorIs(t, err, test.expected.err)
				assert.Len(t, model.requests, test.expected.calls)
			},
		)
	}
}

func TestCheckpointToolSaveBarriers(t *testing.T) {
	type input struct{ phase gent.ExecutionPhase }
	type expected struct{ effects int }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"before tool", input{gent.PhaseToolReady}, expected{0}},
		{"after tool", input{gent.PhaseToolCompleted}, expected{1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newCheckpointStore()
			entered, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			defer unblock()
			failure := errors.New("save blocked by writer fence")
			store.before = func(batch *gent.SaveBatch) error {
				if batch.Checkpoint.Phase != test.input.phase {
					return nil
				}
				close(entered)
				<-release
				return failure
			}
			var calls []gent.ToolIdentity
			agent, config := newRecoveryAgent(&recoveryModel{}, &calls)
			config.Codecs, config.Recorder = store.codecs, store
			runner, execCtx := newTestExecution(t, context.Background(), agent, config)
			done := make(chan error, 1)
			go func() {
				_, _, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read policies.")))
				done <- err
			}()
			checkpointSignal(t, entered)
			assert.Len(t, calls, test.expected.effects)
			unblock()
			assert.ErrorIs(t, checkpointReceive(t, done), failure)
			assert.Len(t, calls, test.expected.effects)
		})
	}
}

func TestCheckpointPausePublicationWaitsForSave(t *testing.T) {
	failure := errors.New("pause save failed")
	type input struct{ fail bool }
	type expected struct {
		published int
		err       error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"accepted", input{}, expected{1, nil}}, {"rejected", input{true}, expected{0, failure}},
	} {
		t.Run(
			test.name,
			func(t *testing.T) {
				store := newCheckpointStore()
				entered, release := make(chan struct{}), make(chan struct{})
				unblock := sync.OnceFunc(func() { close(release) })
				defer unblock()
				store.before = func(batch *gent.SaveBatch) error {
					if batch.Checkpoint.Phase != gent.PhasePaused {
						return nil
					}
					close(entered)
					<-release
					if test.input.fail {
						return failure
					}
					return nil
				}
				observer := &contentObserver{}
				loop := contentLoop(func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
					execCtx.Stop()
					return nil, execCtx.Context().Err()
				})
				runner, execCtx := newTestExecution(t, context.Background(), loop, &gent.Config{
					Codecs: store.codecs, Recorder: store, Subscribers: []*gent.SubscriberRegistration{
						{Subscriber: observer, Delivery: gent.SubscriberLive}}})
				countPauses := func() int {
					count := 0
					for _, event := range observer.recorded() {
						if event.Type == "execution_paused" {
							count++
						}
					}
					return count
				}
				done := make(chan error, 1)
				go func() {
					_, _, err := runner.NewTurn(
						execCtx,
						gent.NewTask(
							gent.TextPart(
								"Pause.",
							),
						),
					)
					done <- err
				}()
				checkpointSignal(t, entered)
				assert.Equal(t, 0, countPauses())
				unblock()
				assert.ErrorIs(t, checkpointReceive(t, done), test.expected.err)
				assert.Equal(t, test.expected.published, countPauses())
			},
		)
	}
}

func TestCheckpointSaveTimeout(t *testing.T) {
	reached := false
	recorder := checkpointRecorder(func(ctx context.Context, _ *gent.SaveBatch) error {
		reached = true
		<-ctx.Done()
		return ctx.Err()
	})
	loop := contentLoop(func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
		t.Error("initial save must precede Loop.Next")
		return acceptRootOutput(execCtx, "Done")
	})
	runner, execCtx := newTestExecution(t, context.Background(), loop, &gent.Config{Recorder: recorder,
		Recording: &gent.RecordingConfig{SaveTimeout: time.Millisecond}})
	value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Answer.")))
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Empty(t, value)
	assert.Nil(t, result)
	assert.True(t, reached)
}

func TestCheckpointInvalidRecordingConfig(t *testing.T) {
	type input struct{ recording *gent.RecordingConfig }
	type expected struct{ message string }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	message := "gent: prepare execution configuration: gent: Recording bounds must not be negative"
	for _, test := range []testCase{
		{"events", input{&gent.RecordingConfig{MaxPendingEvents: -1}}, expected{message}},
		{"bytes", input{&gent.RecordingConfig{MaxPendingBytes: -1}}, expected{message}},
		{"timeout", input{&gent.RecordingConfig{SaveTimeout: -1}}, expected{message}},
	} {
		t.Run(
			test.name,
			func(t *testing.T) {
				runner := executor.NewRunner(
					contentLoop(
						func(
							*gent.ExecutionContext,
						) (
							*gent.AgentLoopResult,
							error,
						) {
							panic("invalid configuration cannot execute")
						},
					),
					&gent.Config{Recording: test.input.recording},
				)
				execCtx, err := runner.NewExecutionContext(context.Background(), "invalid")
				assert.Nil(t, execCtx)
				assert.EqualError(t, err, test.expected.message)
			},
		)
	}
}
