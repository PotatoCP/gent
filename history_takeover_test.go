package gent_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

var errExpiredLease = errors.New("worker lease expired")

type takeoverStore struct {
	mu               sync.Mutex
	lease            int
	view             gent.RunView
	codecs           *gent.CodecRegistry
	runStateSnapshot []byte
}

type takeoverRecorder struct {
	store *takeoverStore
	lease int
}

func (r *takeoverRecorder) Save(
	_ context.Context, batch *gent.SaveBatch,
	captureRunStateSnapshot func() (*gent.RunState, error),
) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	if r.lease != r.store.lease {
		return errExpiredLease
	}
	if r.store.view.RunId == "" {
		r.store.view = *gent.NewRunView(batch.RunId)
	}
	if err := applyTestBatch(&r.store.view, batch, r.store.codecs); err != nil {
		return err
	}
	state, err := captureRunStateSnapshot()
	if err != nil {
		return err
	}
	r.store.runStateSnapshot, err = r.store.codecs.MarshalRunState(state)
	return err
}

func (s *takeoverStore) takeover() ([]byte, *takeoverRecorder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lease++
	return s.runStateSnapshot, &takeoverRecorder{store: s, lease: s.lease}
}

func TestNativeTakeoverStopsOldWorker(t *testing.T) {
	type input struct{ program bool }
	type expected struct{ effects int64 }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"native batch", input{}, expected{1}},
		{"JavaScript", input{true}, expected{1}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			var effects atomic.Int64
			build := func(finish bool) (*react.Agent[string], *gent.Config) {
				chain := toolchain.New()
				for _, name := range []string{"lookup", "book", "later"} {
					chain.RegisterTool(gent.DefineTool(name,
						func(ctx context.Context, _ *bookingInput) (*gent.ToolExecutionResponse, error) {
							effects.Add(1)
							if name == "lookup" {
								close(started)
								select {
								case <-release:
								case <-ctx.Done():
									return capturedToolResponse("", ctx.Err())
								}
							}
							return capturedToolResponse(name, nil)
						}).Description("Operation").ParameterSchema(nil).MustBuild())
				}
				var selected gent.ToolChain = chain
				if test.input.program {
					selected = toolchain.NewJsToolChainWrapper(chain)
				}
				return react.NewAgent(&guidanceModel{program: test.input.program, finish: finish}),
					&gent.Config{ToolChain: selected}
			}
			store := &takeoverStore{lease: 1, codecs: gent.NewCodecRegistry()}
			worker, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			agent, config := build(false)
			config.Codecs = store.codecs
			config.Recorder = &takeoverRecorder{store: store, lease: 1}
			runner, execCtx := newTestExecution(t, worker, agent, config)
			oldDone := make(chan error, 1)
			go func() {
				_, _, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Run the operations.")))
				oldDone <- err
			}()
			select {
			case <-started:
			case err := <-oldDone:
				t.Fatalf("old worker stopped before the tool: %v", err)
			case <-worker.Done():
				t.Fatal("old worker did not start the tool")
			}
			saved, recorder := store.takeover()
			fresh, freshConfig := build(true)
			freshConfig.Recorder = recorder
			_, result, err := resumeRecovery(t, fresh, saved, freshConfig)
			if !assert.NoError(t, err) {
				return
			}
			assert.Equal(t, gent.TerminationSuccess, result.TerminationReason)
			unblock()
			select {
			case err := <-oldDone:
				assert.ErrorIs(t, err, errExpiredLease)
			case <-worker.Done():
				t.Fatal("old worker did not stop after takeover")
			}
			assert.Equal(t, test.expected, expected{effects.Load()})
		})
	}
}
