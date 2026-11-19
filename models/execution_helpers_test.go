package models

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/output"
	"github.com/stretchr/testify/assert"
)

type modelTestStore struct {
	codecs    *gent.CodecRegistry
	view      gent.RunView
	saved     []byte
	replay    *gent.RunState
	afterSave func(*gent.SaveBatch)
}

func (store *modelTestStore) Save(_ context.Context, batch *gent.SaveBatch,
	capture func() (*gent.RunState, error),
) error {
	if store.replay == nil && store.view.RunId == "" {
		store.view = *gent.NewRunView(batch.RunId)
	}
	encoded, err := store.codecs.Marshal(batch)
	if err != nil {
		return err
	}
	var owned gent.SaveBatch
	if err := store.codecs.Unmarshal(encoded, &owned); err != nil {
		return err
	}
	var result *gent.ApplyStateResult
	if store.replay != nil {
		result, err = store.replay.Apply(&owned, store.codecs)
	} else {
		result, err = store.view.Apply(&owned, store.codecs)
	}
	if err != nil {
		return err
	}
	if result.Status != gent.ApplyApplied {
		return fmt.Errorf("test archive: %s", result.Detail)
	}
	state, err := capture()
	if err != nil {
		return err
	}
	store.saved, err = store.codecs.MarshalRunState(state)
	if err != nil {
		return err
	}
	if store.afterSave != nil {
		store.afterSave(batch)
	}
	return nil
}

type modelTestSubscriber struct {
	mu     sync.Mutex
	events []*gent.RecordedEvent
}

func (subscriber *modelTestSubscriber) OnEvent(_ context.Context, event *gent.RecordedEvent) error {
	subscriber.mu.Lock()
	defer subscriber.mu.Unlock()
	subscriber.events = append(subscriber.events, event)
	return nil
}
func (*modelTestSubscriber) OnStop(context.Context, *gent.SubscriberStop) {}
func providerRecordedEvents(execCtx *gent.ExecutionContext) []*gent.RecordedEvent {
	for _, registration := range execCtx.Config().Subscribers {
		if subscriber, ok := registration.Subscriber.(*modelTestSubscriber); ok {
			subscriber.mu.Lock()
			defer subscriber.mu.Unlock()
			return append([]*gent.RecordedEvent(nil), subscriber.events...)
		}
	}
	panic("provider test needs a configured Subscriber")
}
func providerEvents(execCtx *gent.ExecutionContext) []any {
	var payloads []any
	for _, event := range providerRecordedEvents(execCtx) {
		payloads = append(payloads, event.Payload)
	}
	return payloads
}

func modelTestConfig[T any](t testing.TB, loop gent.Loop[T], config *gent.Config) *gent.Config {
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
	if config.Recorder == nil {
		config.Recorder = &modelTestStore{codecs: config.Codecs}
	}
	config.Subscribers = append(config.Subscribers, &gent.SubscriberRegistration{
		Subscriber: &modelTestSubscriber{}, Delivery: gent.SubscriberLive})
	return config
}
func newModelExecution[T any](
	t testing.TB,
	ctx context.Context,
	loop gent.Loop[T],
	config *gent.Config,
) (
	*executor.Runner[T],
	*gent.ExecutionContext,
) {
	t.Helper()
	runner := executor.NewRunner(loop, modelTestConfig(t, loop, config))
	execCtx, err := runner.NewExecutionContext(ctx, t.Name())
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	t.Cleanup(execCtx.Release)
	return runner, execCtx
}
func restoreModelExecution[T any](
	t testing.TB,
	ctx context.Context,
	loop gent.Loop[T],
	config *gent.Config,
	saved []byte,
) (*executor.Runner[T], *gent.ExecutionContext) {
	t.Helper()
	config = modelTestConfig(t, loop, config)
	loaded, err := config.Codecs.UnmarshalRunState(saved)
	if !assert.NoError(t, err) || !assert.Equal(t, gent.RunStateReady, loaded.Status, loaded.Detail) {
		t.FailNow()
	}
	if store, ok := config.Recorder.(*modelTestStore); ok && store.view.RunId == "" {
		// The restored test store checks only the tail. It needs its own replay target because
		// successful Restore transfers the other decoded state to the execution context.
		tail, err := config.Codecs.UnmarshalRunState(saved)
		if !assert.NoError(t, err) || !assert.Equal(t, gent.RunStateReady, tail.Status) {
			t.FailNow()
		}
		store.replay = tail.State
	}
	runner := executor.NewRunner(loop, config)
	restored, err := runner.Restore(ctx, loaded.State)
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

type modelFixtureLoop func(*gent.ExecutionContext) (*gent.AgentLoopResult, error)

func (loop modelFixtureLoop) Next(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
	return loop(execCtx)
}
func (modelFixtureLoop) Output() gent.Output[string] { return output.NewTextOutput() }
func modelFixtureDone(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
	return gent.AcceptOutput(execCtx, &gent.OutputResult[string]{Accepted: true, Value: "Done.",
		Content: []gent.ContentPart{gent.TextPart("Done.")}}, nil)
}
