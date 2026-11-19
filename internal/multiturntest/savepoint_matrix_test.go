package multiturntest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/internal/tt"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

type matrixData struct {
	Name      string
	Visits    int
	Resets    int
	Compacted bool
	Tags      []string
	Counts    map[string]int
	Nested    struct {
		Enabled bool
		Count   int
	}
}

func (*matrixData) CheckpointType() string { return "test:savepoint-data" }
func (*matrixData) CheckpointVersion() int { return 1 }
func (data *matrixData) ResetTurn()        { data.Resets++; data.Compacted = false }
func (data *matrixData) SaveCheckpointState() (*gent.EncodedValue, error) {
	encoded, err := json.Marshal(data)
	return &gent.EncodedValue{Type: data.CheckpointType(), Version: 1, Data: encoded}, err
}
func (data *matrixData) RestoreCheckpointState(encoded *gent.EncodedValue) error {
	return json.Unmarshal(encoded.Data, data)
}

// This wrapper has no saved step enum. Its repeated local work is allowed by D72. The reference
// Loop supplies its own managed frame while application data remains independent and complete.
type matrixLoop struct {
	*react.Agent[string]
	custom   bool
	external *matrixExternal
}

func (loop *matrixLoop) Next(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
	loop.external.mu.Lock()
	pause := execCtx.Data().(*matrixData).Compacted && !loop.external.paused
	if pause {
		loop.external.paused = true
	}
	loop.external.mu.Unlock()
	if pause {
		execCtx.Stop()
		return &gent.AgentLoopResult{Action: gent.LAContinue}, nil
	}
	if loop.custom {
		data := execCtx.Data().(*matrixData)
		data.Visits++
		data.Counts["local"]++
	}
	return loop.Agent.Next(execCtx)
}

type matrixCompactor struct{}

func (matrixCompactor) ShouldCompact(execCtx *gent.ExecutionContext) (bool, error) {
	return execCtx.Stats().GetCounter(gent.SCToolCalls) > 0 &&
		!execCtx.Data().(*matrixData).Compacted, nil
}
func (matrixCompactor) NotifyCompacted(execCtx *gent.ExecutionContext) error {
	execCtx.Data().(*matrixData).Compacted = true
	return nil
}
func (matrixCompactor) Compact(execCtx *gent.ExecutionContext, input *gent.CompactionInput) (
	*gent.HistoryUpdate, error,
) {
	summary, err := execCtx.NewHistoryEntry(&gent.HistoryEntry{Kind: gent.HistorySummary,
		Origin: gent.RecordCompactedSynthetic, Replaces: input.EntryIds(),
		Messages: []*gent.MessageContent{{Role: gent.ChatMessageTypeHuman,
			Parts: []gent.ContentPart{gent.TextPart("The saved tool evidence is available.")}}}})
	if err != nil {
		return nil, err
	}
	active := append(input.EntryIds(), summary.Id)
	return &gent.HistoryUpdate{ExpectedDraftRevision: input.Revision,
		AppendedEntries: []*gent.HistoryEntry{summary}, Active: active}, nil
}

type matrixFault string

const (
	// matrixBefore rejects the save before storage accepts it.
	matrixBefore matrixFault = "before storage"
	// matrixBlocked holds the save while the caller checks that work cannot advance.
	matrixBlocked matrixFault = "blocked storage"
	// matrixLostAck stores the batch but reports a failed acknowledgment.
	matrixLostAck matrixFault = "lost acknowledgment"
	// matrixAfter ends the owner just after storage accepts the save.
	matrixAfter matrixFault = "after acceptance"
)

type matrixReplay string

const (
	// matrixBatches restores only complete encoded batches, without requesting captures.
	matrixBatches matrixReplay = "batches only"
	// matrixTail restores a stale valid capture and pages later batches.
	matrixTail matrixReplay = "capture and paged tail"
	// matrixRetry reconciles the exact attempted batch before restoring its accepted head.
	matrixRetry matrixReplay = "exact retry and head capture"
)

type matrixExternal struct {
	mu       sync.Mutex
	calls    map[string]int
	models   int
	recovery int
	crashed  bool
	paused   bool
}

type matrixWork struct {
	Calls    map[string]int
	Models   int
	Recovery int
}

func (external *matrixExternal) work() matrixWork {
	external.mu.Lock()
	defer external.mu.Unlock()
	calls := map[string]int{}
	for id, count := range external.calls {
		calls[id] = count
	}
	return matrixWork{calls, external.models, external.recovery}
}

type matrixStore struct {
	codecs    *gent.CodecRegistry
	batches   [][]byte
	states    [][]byte
	runId     string
	phase     gent.ExecutionPhase
	fault     matrixFault
	replay    matrixReplay
	failed    bool
	failure   error
	cancel    context.CancelFunc
	attempted []byte
	candidate []byte
	entered   chan struct{}
	unblock   chan struct{}
}

func (store *matrixStore) Save(ctx context.Context, batch *gent.SaveBatch,
	capture func() (*gent.RunState, error),
) error {
	data, err := store.codecs.Marshal(batch)
	if err != nil {
		return err
	}
	var saved []byte
	if store.replay != matrixBatches {
		state, err := capture()
		if err != nil {
			return err
		}
		saved, err = store.codecs.MarshalRunState(state)
		if err != nil {
			return err
		}
	}
	store.runId = batch.RunId
	if !store.failed && batch.Checkpoint.Phase == store.phase {
		store.failed = true
		store.attempted, store.candidate = data, saved
		switch store.fault {
		case matrixBefore:
			return store.failure
		case matrixBlocked:
			close(store.entered)
			select {
			case <-store.unblock:
			case <-ctx.Done():
				return ctx.Err()
			}
			return store.failure
		case matrixLostAck:
			store.put(data, saved)
			return store.failure
		case matrixAfter:
			store.put(data, saved)
			store.cancel()
			return nil
		}
	}
	store.put(data, saved)
	return nil
}

func (store *matrixStore) put(data, saved []byte) {
	// The fixture models a serialized transaction with exact-retry receipts. It does not keep
	// live execution objects as persistence and cannot publish a capture before its batch.
	if len(store.batches) > 0 && string(store.batches[len(store.batches)-1]) == string(data) {
		return
	}
	store.batches = append(store.batches, data)
	store.states = append(store.states, saved)
}

func matrixRunner(
	t testing.TB,
	store *matrixStore,
	external *matrixExternal,
	custom bool,
) *executor.Runner[string] {
	model := &matrixModel{external: external}
	chain := toolchain.New().RegisterTool(gent.DefineTool("write", func(ctx context.Context,
		input struct{ Number int },
	) (*gent.ToolExecutionResponse, error) {
		identity, _ := gent.ToolIdentityFromContext(ctx)
		external.mu.Lock()
		external.calls[identity.ToolCallId]++
		crash := !external.crashed
		external.crashed = true
		external.mu.Unlock()
		if crash {
			store.cancel()
			return nil, context.Canceled
		}
		return &gent.ToolExecutionResponse{Status: gent.ToolExecuted,
			Parts: []gent.ContentPart{gent.TextPart(fmt.Sprintf("Saved %d", input.Number))}}, nil
	}).Recovery(func(_ context.Context, request *gent.ToolRecoveryRequest[struct{ Number int }]) (
		*gent.ToolRecoveryResponse, error,
	) {
		external.mu.Lock()
		defer external.mu.Unlock()
		external.recovery++
		if external.calls[request.Identity().ToolCallId] == 0 {
			return &gent.ToolRecoveryResponse{Status: gent.ToolRecoveryNotStarted}, nil
		}
		return &gent.ToolRecoveryResponse{Status: gent.ToolRecoveryCompleted,
			Execution: &gent.ToolExecutionResponse{Status: gent.ToolExecuted,
				Parts: []gent.ContentPart{gent.TextPart(fmt.Sprintf("Saved %d", request.Input().Number))}}}, nil
	}).MustBuild())
	assert.NoError(t, chain.RegisterCodecs(store.codecs))
	loop := &matrixLoop{Agent: react.NewAgent(model), custom: custom, external: external}
	return executor.NewRunner[string](loop, &gent.Config{Codecs: store.codecs, Recorder: store,
		ToolChain: chain, CompactionTrigger: matrixCompactor{}, CompactionStrategy: matrixCompactor{},
		NewLoopData: func() gent.LoopData {
			data := &matrixData{Name: "original", Tags: []string{"first", "second"},
				Counts: map[string]int{"kept": 7, "zero": 0}}
			data.Nested.Enabled = true
			return data
		}})
}

type matrixModel struct{ external *matrixExternal }

func (*matrixModel) Name() string { return "savepoint-matrix" }
func (model *matrixModel) GenerateContentStream(
	ctx context.Context,
	request *gent.ModelCallRequest,
) (
	gent.Stream,
	error,
) {
	model.external.mu.Lock()
	model.external.models++
	model.external.mu.Unlock()
	for _, message := range request.Messages {
		for _, part := range message.Parts {
			if _, ok := part.(*gent.ModelToolCallResponse); ok {
				return tt.NewMockModel().AddResponse("Done", 3, 1).GenerateContentStream(ctx, request)
			}
		}
	}
	return tt.NewMockModel().AddRawResponse(&gent.ModelCallResponse{Choices: []*gent.ContentChoice{{
		Completion: gent.ModelCompletion{Status: gent.ModelStatusToolCalls}, Parts: []gent.ContentPart{
			&gent.ModelToolCallRequest{ID: "one", Type: "function", FunctionCall: &gent.FunctionCall{
				Name: "write", Arguments: `{"Number":1}`}},
			&gent.ModelToolCallRequest{ID: "two", Type: "function", FunctionCall: &gent.FunctionCall{
				Name: "write", Arguments: `{"Number":2}`}},
		},
	}}}).GenerateContentStream(ctx, request)
}

func (model *matrixModel) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}

type matrixExecution struct {
	value  string
	result *gent.ExecutionResult
	err    error
}

func TestEverySavepointRestoresAcceptedState(t *testing.T) {
	type input struct {
		phase  gent.ExecutionPhase
		fault  matrixFault
		replay matrixReplay
		custom bool
	}
	type expected struct {
		value   string
		status  gent.ExecutionStatus
		resets  int
		failure error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	phases := []gent.ExecutionPhase{gent.PhaseInitial, gent.PhaseIterationReady,
		gent.PhaseToolReady, gent.PhaseToolCompleted, gent.PhaseIterationCompleted,
		gent.PhaseCompactionCompleted, gent.PhaseRecoveryCompleted, gent.PhasePaused, gent.PhaseTerminal}
	failure := errors.New("injected storage interruption")
	var tests []testCase
	for _, custom := range []bool{false, true} {
		for _, phase := range phases {
			for _, fault := range []matrixFault{matrixBefore, matrixBlocked, matrixLostAck, matrixAfter} {
				for _, replay := range []matrixReplay{matrixBatches, matrixTail, matrixRetry} {
					tests = append(tests, testCase{fmt.Sprintf("custom=%v/%s/%s/%s", custom,
						phase, fault, replay), input{phase, fault, replay, custom},
						expected{"Done", gent.ExecutionFinished, 1, failure}})
				}
			}
		}
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := &matrixStore{phase: test.input.phase, fault: test.input.fault,
				replay: test.input.replay, failure: test.expected.failure,
				entered: make(chan struct{}), unblock: make(chan struct{})}
			external := &matrixExternal{calls: map[string]int{}}
			var completed matrixExecution
			var finalData matrixData
			entered := store.entered
			for attempt := 0; attempt < 8; attempt++ {
				ctx, cancel := context.WithCancel(context.Background())
				store.cancel, store.codecs = cancel, gent.NewCodecRegistry()
				runner := matrixRunner(t, store, external, test.input.custom)
				fresh := len(store.batches) == 0
				var execCtx *gent.ExecutionContext
				if fresh {
					var err error
					execCtx, err = runner.NewExecutionContext(ctx, "matrix")
					if !assert.NoError(t, err) {
						cancel()
						return
					}
				} else {
					state, data := loadMatrixState(t, store)
					before := external.work()
					restored, err := runner.Restore(ctx, state)
					if !assert.NoError(t, err) || !assert.Equal(t, gent.RestoreReady, restored.Status) {
						cancel()
						return
					}
					execCtx = restored.Context
					assert.Equal(t, data, *execCtx.Data().(*matrixData))
					assert.Equal(t, before, external.work(), "Restore must not start external work")
				}
				done := make(chan matrixExecution, 1)
				go func() {
					var value string
					var result *gent.ExecutionResult
					var err error
					if fresh {
						value, result, err = runner.NewTurn(execCtx,
							gent.NewTask(gent.TextPart("Save two records and report.")))
					} else {
						value, result, err = runner.Continue(execCtx)
					}
					done <- matrixExecution{value, result, err}
				}()
				var got matrixExecution
				select {
				case got = <-done:
				case <-entered:
					entered = nil
					before := external.work()
					select {
					case got = <-done:
						t.Error("execution ended while its save was blocked")
					case <-time.After(2 * time.Millisecond):
					}
					assert.Equal(t, before, external.work(), "blocked Save must prevent dependent work")
					close(store.unblock)
					got = <-done
				case <-time.After(10 * time.Second):
					cancel()
					t.Fatal("matrix attempt did not finish")
				}
				if got.err != nil {
					assert.Nil(t, got.result)
					assert.Equal(t, "", got.value)
					assert.True(t, errors.Is(got.err, failure) || errors.Is(got.err, context.Canceled),
						"unexpected failure: %v", got.err)
					if errors.Is(got.err, failure) && test.input.replay == matrixRetry {
						var failed *gent.PersistenceError
						if assert.ErrorAs(t, got.err, &failed) {
							data, err := store.codecs.Marshal(failed.Batch)
							assert.NoError(t, err)
							assert.Equal(t, store.attempted, data)
							store.put(data, store.candidate)
							count := len(store.batches)
							store.put(data, store.candidate)
							assert.Len(t, store.batches, count, "exact retry cannot admit input twice")
						}
					}
				} else if got.result.Status == gent.ExecutionNoActiveTurn {
					value, result, err := runner.GetLastTurnResult(execCtx)
					got = matrixExecution{value, result, err}
				}
				if got.err == nil && got.result.Status == gent.ExecutionFinished {
					completed, finalData = got, *execCtx.Data().(*matrixData)
				}
				execCtx.Release()
				cancel()
				if completed.result != nil {
					break
				}
			}
			assert.True(t, store.failed, "the requested boundary must be reached")
			if !assert.NotNil(t, completed.result) {
				return
			}
			assert.NoError(t, completed.err)
			assert.Equal(t, test.expected.value, completed.value)
			assert.Equal(t, &gent.ExecutionResult{Status: test.expected.status,
				TurnId: completed.result.TurnId, TerminationReason: gent.TerminationSuccess,
				Output: []gent.ContentPart{gent.TextPart("Done")}}, completed.result)
			assert.Equal(t, test.expected.resets, finalData.Resets)
			assert.Equal(t, "original", finalData.Name)
			assert.Equal(t, []string{"first", "second"}, finalData.Tags)
			assert.Equal(t, 7, finalData.Counts["kept"])
			assert.Equal(t, 0, finalData.Counts["zero"])
			assert.True(t, finalData.Nested.Enabled)
			assert.Equal(t, 0, finalData.Nested.Count)
			assert.True(t, finalData.Compacted)
			if test.input.custom {
				assert.Greater(t, finalData.Visits, 0)
				assert.Equal(t, finalData.Visits, finalData.Counts["local"])
			} else {
				assert.Equal(t, 0, finalData.Visits)
				assert.Equal(t, map[string]int{"kept": 7, "zero": 0}, finalData.Counts)
			}
			for _, count := range external.work().Calls {
				assert.Equal(t, 1, count, "an accepted or recovered effect cannot run again")
			}
			_, data := loadMatrixState(t, store)
			assert.Equal(t, finalData, data)
			verifyMatrixCaptures(t, store)
		})
	}
}

func loadMatrixState(t *testing.T, store *matrixStore) (*gent.RunState, matrixData) {
	t.Helper()
	codecs := store.codecs
	var last gent.SaveBatch
	assert.NoError(t, codecs.Unmarshal(store.batches[len(store.batches)-1], &last))
	var expected matrixData
	assert.NoError(t, json.Unmarshal(last.Checkpoint.Contexts[0].Extensions["data"].Value.Data,
		&expected))
	state, index := gent.NewRunState(store.runId), -1
	if store.replay != matrixBatches {
		index = len(store.states) - 1
		if store.replay == matrixTail {
			// An incomplete newer publication must never become a usable empty or partial state.
			data := store.states[index]
			broken, err := codecs.UnmarshalRunState(data[:len(data)/2])
			assert.NoError(t, err)
			assert.Equal(t, gent.RunStateInvalid, broken.Status)
			assert.Nil(t, broken.State)
			index = 0
		}
		loaded, err := codecs.UnmarshalRunState(store.states[index])
		if !assert.NoError(t, err) || !assert.Equal(t, gent.RunStateReady, loaded.Status) {
			t.FailNow()
		}
		state = loaded.State
	}
	// Page complete encoded batches. The reader does not need preceding archive records.
	for start := index + 1; start < len(store.batches); start += 2 {
		page := store.batches[start:min(start+2, len(store.batches))]
		for _, encoded := range page {
			var batch gent.SaveBatch
			assert.NoError(t, codecs.Unmarshal(encoded, &batch))
			result, err := state.Apply(&batch, codecs)
			assert.NoError(t, err)
			assert.Equal(t, &gent.ApplyStateResult{Status: gent.ApplyApplied}, result)
		}
	}
	if store.replay != matrixBatches {
		got, err := codecs.MarshalRunState(state)
		assert.NoError(t, err)
		assert.Equal(t, store.states[len(store.states)-1], got)
	}
	return state, expected
}

func verifyMatrixCaptures(t *testing.T, store *matrixStore) {
	t.Helper()
	// Verify every accepted capture against batch-only replay once. The immutable stored prefix
	// is the same after each interruption; replaying and encoding all its earlier savepoints on
	// every restore adds repeated work without checking a different state. loadMatrixState still
	// restores fresh objects on every attempt and compares each restored head with its capture.
	if store.replay == matrixBatches {
		for _, state := range store.states {
			assert.Nil(t, state, "batch-only storage must not depend on capture")
		}
		return
	}
	full := gent.NewRunState(store.runId)
	for i, encoded := range store.batches {
		var batch gent.SaveBatch
		assert.NoError(t, store.codecs.Unmarshal(encoded, &batch))
		result, err := full.Apply(&batch, store.codecs)
		if !assert.NoError(t, err) || !assert.Equal(t,
			&gent.ApplyStateResult{Status: gent.ApplyApplied}, result) {
			t.FailNow()
		}
		data, err := store.codecs.MarshalRunState(full)
		assert.NoError(t, err)
		assert.Equal(t, store.states[i], data, "capture must equal full replay at this savepoint")
	}
}
