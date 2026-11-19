package gent_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/internal/tt"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

type checkpointHoldStage string

const (
	// checkpointHoldCapture stops local custom-data capture before it reads the value.
	checkpointHoldCapture checkpointHoldStage = "capture"
	// checkpointHoldEncoding stops batch encoding after local custom-data capture.
	checkpointHoldEncoding checkpointHoldStage = "encoding"
	// checkpointHoldStorage stops an admitted save before storage commits its fixed batch.
	checkpointHoldStorage checkpointHoldStage = "storage"
)

type checkpointAdmissionGate struct {
	armed   atomic.Bool
	entered chan struct{}
	ended   <-chan struct{}
}

func (gate *checkpointAdmissionGate) wait() bool {
	if !gate.armed.Swap(false) {
		return false
	}
	close(gate.entered)
	<-gate.ended
	return true
}

type checkpointAdmissionData struct {
	gent.BasicLoopData
	value atomic.Int64
	armed atomic.Bool
	stage checkpointHoldStage
	gate  *checkpointAdmissionGate
}

func (*checkpointAdmissionData) CheckpointType() string { return "test:checkpoint-admission" }

func (data *checkpointAdmissionData) SaveCheckpointState() (*gent.EncodedValue, error) {
	if data.armed.Swap(false) {
		data.gate.armed.Store(true)
		if data.stage == checkpointHoldCapture {
			// This barrier models slow local capture. It performs no external operation.
			data.gate.wait()
		}
	}
	encoded, err := json.Marshal(data.value.Load())
	return &gent.EncodedValue{Type: data.CheckpointType(), Version: 1, Data: encoded}, err
}

func (data *checkpointAdmissionData) RestoreCheckpointState(state *gent.EncodedValue) error {
	var value int64
	if err := json.Unmarshal(state.Data, &value); err != nil {
		return err
	}
	data.value.Store(value)
	return nil
}

type checkpointAdmissionMarker int
type checkpointAdmissionCodec struct{ gate *checkpointAdmissionGate }

func (*checkpointAdmissionCodec) Type() string { return "test:checkpoint-admission-marker" }
func (*checkpointAdmissionCodec) Version() int { return 1 }
func (codec *checkpointAdmissionCodec) Encode(value any) (json.RawMessage, error) {
	codec.gate.wait()
	return json.Marshal(value.(checkpointAdmissionMarker))
}
func (*checkpointAdmissionCodec) Decode(data json.RawMessage) (any, error) {
	var value checkpointAdmissionMarker
	err := json.Unmarshal(data, &value)
	return value, err
}

type checkpointAdmissionSaveError struct {
	inspect func()
}

func (*checkpointAdmissionSaveError) Error() string { return "storage acknowledgment failed" }
func (failure *checkpointAdmissionSaveError) Is(error) bool {
	// Application error methods can inspect the run. Gent must not hold its state lock here.
	failure.inspect()
	return false
}

type checkpointAdmissionRecorder struct {
	*chainFailureRecorder
	gate        *checkpointAdmissionGate
	snapshots   [][]byte
	failure     error
	joinFailure bool
	readFailure func() error
	commit      bool
	contextErr  error
	attempted   *gent.SaveBatch
}

func (recorder *checkpointAdmissionRecorder) Save(ctx context.Context, batch *gent.SaveBatch,
	capture func() (*gent.RunState, error),
) error {
	held := recorder.gate.wait()
	if held {
		recorder.attempted = batch
		recorder.contextErr = ctx.Err()
		if !recorder.commit {
			return recorder.saveFailure()
		}
	}
	if err := recorder.chainFailureRecorder.Save(ctx, batch, capture); err != nil {
		return err
	}
	recorder.snapshots = append(recorder.snapshots, recorder.runStateSnapshot)
	if held {
		return recorder.saveFailure()
	}
	return nil
}

func (recorder *checkpointAdmissionRecorder) saveFailure() error {
	if recorder.joinFailure {
		return errors.Join(recorder.readFailure(), recorder.failure)
	}
	return recorder.failure
}

type checkpointAdmissionChain struct {
	*toolchain.Registry
	data        *checkpointAdmissionData
	failure     error
	stop        bool
	interrupt   bool
	cancelOwner context.CancelFunc
}

func (chain *checkpointAdmissionChain) prepareCapture(execCtx *gent.ExecutionContext) {
	if chain.data.stage == checkpointHoldEncoding {
		// This value reaches the codec again during BatchDigest, after custom-data capture.
		execCtx.PublishCommonEvent("test:checkpoint", "Prepare checkpoint.",
			checkpointAdmissionMarker(0))
	}
	chain.data.gate.ended = execCtx.Context().Done()
	chain.data.armed.Store(true)
}

func (chain *checkpointAdmissionChain) fail(execCtx *gent.ExecutionContext) error {
	select {
	case <-chain.data.gate.entered:
	case <-execCtx.Context().Done():
		return execCtx.Context().Err()
	}
	chain.data.value.Store(1)
	if chain.stop {
		execCtx.Stop()
		return execCtx.Context().Err()
	}
	if chain.interrupt {
		chain.cancelOwner()
		return execCtx.Context().Err()
	}
	return chain.failure
}

func (chain *checkpointAdmissionChain) ExecuteNext(execCtx *gent.ExecutionContext,
	plan *gent.ToolPlan, format gent.TextFormat,
) (*gent.ToolCallRecord, error) {
	if plan.Calls[plan.NextCall].Name == "fail" {
		return nil, chain.fail(execCtx)
	}
	chain.prepareCapture(execCtx)
	return chain.Registry.ExecuteNext(execCtx, plan, format)
}

// Failure must win before admission, including failure during capture or batch encoding. Once
// storage has a fixed batch, its acceptance and uncertain-save rules still apply. Stop and owner
// cancellation can save observed evidence without accepting failed provisional work.
func TestCheckpointAdmissionFailureOrdering(t *testing.T) {
	type input struct {
		stage       checkpointHoldStage
		hook        bool
		stop        bool
		interrupt   bool
		saveFailure bool
		joinFailure bool
		commit      bool
	}
	type expected struct {
		value      string
		status     gent.ExecutionStatus
		reason     gent.TerminationReason
		failed     bool
		causeFound bool
		saveError  bool
		phases     []gent.ExecutionPhase
		states     []int64
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	before := expected{failed: true, causeFound: true,
		phases: []gent.ExecutionPhase{gent.PhaseInitial, gent.PhaseIterationReady,
			gent.PhaseToolReady}, states: []int64{0, 0, 0}}
	during := expected{failed: true, causeFound: true,
		phases: []gent.ExecutionPhase{gent.PhaseInitial, gent.PhaseIterationReady,
			gent.PhaseToolReady, gent.PhaseToolReady}, states: []int64{0, 0, 0, 0}}
	paused := expected{status: gent.ExecutionPaused, reason: gent.TerminationStopped,
		phases: []gent.ExecutionPhase{gent.PhaseInitial, gent.PhaseIterationReady,
			gent.PhaseToolReady, gent.PhaseToolReady, gent.PhasePaused}, states: []int64{0, 0, 0, 1, 1}}
	pausedFixed := paused
	pausedFixed.states = []int64{0, 0, 0, 0, 1}
	rejectedSave, uncertainSave := before, during
	rejectedSave.saveError, uncertainSave.saveError = true, true
	interruptedCapture := during
	interruptedCapture.states = []int64{0, 0, 0, 1}
	cases := []testCase{
		{"operation failure during capture", input{stage: checkpointHoldCapture}, before},
		{"Hook failure during capture", input{stage: checkpointHoldCapture, hook: true}, before},
		{"operation failure during encoding", input{stage: checkpointHoldEncoding}, before},
		{"Hook failure during encoding", input{stage: checkpointHoldEncoding, hook: true}, before},
		{"operation failure during save", input{stage: checkpointHoldStorage, commit: true}, during},
		{"Hook failure during save", input{stage: checkpointHoldStorage, hook: true, commit: true},
			during},
		{"rejected save after operation failure", input{stage: checkpointHoldStorage,
			saveFailure: true}, rejectedSave},
		{"uncertain save after operation failure", input{stage: checkpointHoldStorage,
			saveFailure: true, commit: true}, uncertainSave},
		{"joined rejected save after operation failure", input{stage: checkpointHoldStorage,
			saveFailure: true, joinFailure: true}, rejectedSave},
		{"joined uncertain save after operation failure", input{stage: checkpointHoldStorage,
			saveFailure: true, joinFailure: true, commit: true}, uncertainSave},
		{"Stop during capture", input{stage: checkpointHoldCapture, stop: true}, paused},
		{"Stop during encoding", input{stage: checkpointHoldEncoding, stop: true}, pausedFixed},
		{"Stop during save", input{stage: checkpointHoldStorage, stop: true, commit: true},
			pausedFixed},
		{"owner cancellation during capture", input{stage: checkpointHoldCapture, interrupt: true},
			interruptedCapture},
		{"owner cancellation during save", input{stage: checkpointHoldStorage, interrupt: true,
			commit: true}, during},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			gate := &checkpointAdmissionGate{entered: make(chan struct{})}
			data := &checkpointAdmissionData{stage: test.input.stage, gate: gate}
			base := toolchain.New()
			for _, name := range []string{"save", "fail"} {
				base.RegisterTool(gent.DefineTool(name, func(context.Context, struct{}) (
					*gent.ToolExecutionResponse, error,
				) {
					t.Error("a canceled call must not start its effect")
					return &gent.ToolExecutionResponse{Status: gent.ToolExecuted}, nil
				}).MustBuild())
			}
			failure := errors.New("parallel operation failed during checkpoint")
			chain := &checkpointAdmissionChain{Registry: base, data: data, failure: failure,
				stop: test.input.stop, interrupt: test.input.interrupt, cancelOwner: cancel}
			var inner gent.ToolChain = chain
			var execCtx *gent.ExecutionContext
			var hooks *gent.Hooks
			if test.input.hook {
				inner = base
				hooks = &gent.Hooks{BeforeToolCall: func(_ context.Context,
					event *gent.BeforeToolCallEvent,
				) error {
					switch event.ToolName {
					case "save":
						chain.prepareCapture(execCtx)
					case "fail":
						return chain.fail(execCtx)
					}
					return nil
				}}
			}
			model := tt.NewMockModel().AddRawResponse(&gent.ModelCallResponse{
				Choices: []*gent.ContentChoice{{
					Completion: gent.ModelCompletion{Status: gent.ModelStatusToolCalls},
					Parts: []gent.ContentPart{&gent.ModelToolCallRequest{
						ID: "program-1", Type: "function",
						FunctionCall: &gent.FunctionCall{Name: "run_code", Arguments: `{
  "code": "tool.parallelCall([{tool:\"save\",args:{}},{tool:\"fail\",args:{}}]);"
}`},
					}},
				}},
			})
			recorder := &checkpointAdmissionRecorder{chainFailureRecorder: &chainFailureRecorder{
				checkpointStore: newCheckpointStore()}, gate: &checkpointAdmissionGate{},
				commit: test.input.commit, joinFailure: test.input.joinFailure,
				readFailure: func() error { return execCtx.RecordingError() }}
			encodeGate := &checkpointAdmissionGate{}
			if test.input.stage == checkpointHoldEncoding {
				encodeGate = gate
			}
			if !assert.NoError(t, recorder.codecs.Register(checkpointAdmissionMarker(0),
				&checkpointAdmissionCodec{gate: encodeGate})) {
				return
			}
			if test.input.stage == checkpointHoldStorage {
				recorder.gate = gate
			}
			saveFailure := &checkpointAdmissionSaveError{inspect: func() {
				assert.Error(t, execCtx.RecordingError())
			}}
			if test.input.saveFailure {
				recorder.failure = saveFailure
			}
			runner := executor.NewRunner(react.NewAgent(model), &gent.Config{
				ToolChain: toolchain.NewJsToolChainWrapper(inner), Recorder: recorder,
				Codecs: recorder.codecs, NewLoopData: func() gent.LoopData { return data }, Hooks: hooks,
			})
			execCtx, err := runner.NewExecutionContext(ctx, test.name)
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Run both.")))
			cause := failure
			if test.input.interrupt {
				cause = context.Canceled
			}
			actual := expected{value: value, failed: err != nil, causeFound: errors.Is(err, cause),
				saveError: errors.Is(err, saveFailure), phases: recorder.phases}
			if result != nil {
				actual.status, actual.reason = result.Status, result.TerminationReason
			}
			for _, batch := range recorder.batches {
				for _, extension := range batch.Checkpoint.Contexts[0].Extensions {
					if extension.Type != data.CheckpointType() {
						continue
					}
					var saved int64
					if assert.NoError(t, json.Unmarshal(extension.Value.Data, &saved)) {
						actual.states = append(actual.states, saved)
					}
				}
			}
			assert.Equal(t, test.expected, actual)
			assert.NoError(t, recorder.contextErr)
			var persistence *gent.PersistenceError
			if assert.Equal(t, test.expected.saveError, errors.As(err, &persistence)) &&
				test.expected.saveError {
				assert.Equal(t, recorder.attempted, persistence.Batch)
				assert.ErrorIs(t, persistence, saveFailure)
			}
			if test.expected.failed {
				assert.Nil(t, result)
			} else {
				assert.Equal(t, &gent.ExecutionResult{Status: test.expected.status,
					TurnId: execCtx.TurnId(), TerminationReason: test.expected.reason}, result)
			}
			want := test.expected.states[len(test.expected.states)-1]
			assertCheckpointAdmissionRecovery(t, recorder, base, want)
		})
	}
}

func assertCheckpointAdmissionRecovery(t *testing.T, recorder *checkpointAdmissionRecorder,
	base *toolchain.Registry, want int64,
) {
	t.Helper()
	type input struct{ snapshot int }
	type expected struct{ value int64 }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"full batch replay", input{snapshot: -1}, expected{want}},
		{"RunStateSnapshot plus tail", input{snapshot: 1}, expected{want}},
	} {
		t.Run(test.name, func(t *testing.T) {
			codecs := gent.NewCodecRegistry()
			if !assert.NoError(t, codecs.Register(checkpointAdmissionMarker(0),
				&checkpointAdmissionCodec{gate: &checkpointAdmissionGate{}})) {
				return
			}
			loop := react.NewAgent(tt.NewMockModel().AddResponse("Recovered", 0, 0))
			runner := executor.NewRunner(loop, &gent.Config{Codecs: codecs,
				ToolChain:   toolchain.NewJsToolChainWrapper(base),
				NewLoopData: func() gent.LoopData { return &checkpointAdmissionData{} },
			})
			// Bind current codecs before decoding persisted evidence into fresh objects.
			setup, err := runner.NewExecutionContext(context.Background(), "codec setup")
			if !assert.NoError(t, err) {
				return
			}
			setup.Release()
			state := gent.NewRunState(recorder.view.RunId)
			if test.input.snapshot >= 0 {
				loaded, err := codecs.UnmarshalRunState(recorder.snapshots[test.input.snapshot])
				if !assert.NoError(t, err) || !assert.Equal(t, gent.RunStateReady, loaded.Status) {
					return
				}
				state = loaded.State
			}
			tail := recorder.batches[test.input.snapshot+1:]
			assert.NotEmpty(t, tail)
			for _, batch := range tail {
				encoded, err := recorder.codecs.Marshal(batch)
				if !assert.NoError(t, err) {
					return
				}
				var owned gent.SaveBatch
				if !assert.NoError(t, codecs.Unmarshal(encoded, &owned)) {
					return
				}
				applied, err := state.Apply(&owned, codecs)
				if !assert.NoError(t, err) || !assert.Equal(t, gent.ApplyApplied, applied.Status) {
					return
				}
			}
			restored, err := runner.Restore(context.Background(), state)
			if !assert.NoError(t, err) || !assert.Equal(t, gent.RestoreReady, restored.Status) {
				return
			}
			defer restored.Context.Release()
			assert.Equal(t, test.expected, expected{
				value: restored.Context.Data().(*checkpointAdmissionData).value.Load(),
			})
			value, result, err := runner.Continue(restored.Context)
			assert.NoError(t, err)
			assert.Equal(t, "Recovered", value)
			assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionFinished,
				TurnId: restored.Context.TurnId(), TerminationReason: gent.TerminationSuccess,
				Output: []gent.ContentPart{gent.TextPart("Recovered")}}, result)
			assert.Equal(t, test.expected, expected{
				value: restored.Context.Data().(*checkpointAdmissionData).value.Load(),
			})
		})
	}
}
