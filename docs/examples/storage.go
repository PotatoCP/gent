package examples

import (
	"bytes"
	"context"
	"fmt"
	"sync"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/executor"
)

// Commit is an application storage operation. It accepts bytes, position, and receipt atomically.
type ByteStore interface {
	Commit(ctx context.Context, batch *gent.SaveBatch, data, state []byte) error
}

type BatchRecorder struct {
	Codecs *gent.CodecRegistry
	Store  ByteStore
}

// docs:start recorder-save
func (recorder *BatchRecorder) Save(ctx context.Context, batch *gent.SaveBatch,
	captureRunStateSnapshot func() (*gent.RunState, error),
) error {
	data, err := recorder.Codecs.Marshal(batch)
	if err != nil {
		return err
	}
	// Commit checks the writer, previous position, and exact retry receipt together.
	// Return nil only after the complete write succeeds.
	return recorder.Store.Commit(ctx, batch, data, nil)
}

// docs:end recorder-save

type SnapshotRecorder struct {
	BatchRecorder
	Every uint64
}

func (recorder *SnapshotRecorder) Save(ctx context.Context, batch *gent.SaveBatch,
	captureRunStateSnapshot func() (*gent.RunState, error),
) error {
	data, err := recorder.Codecs.Marshal(batch)
	if err != nil {
		return err
	}
	// docs:start recorder-capture
	var stateBytes []byte
	// Capture at selected accepted boundaries. Earlier batches remain the archive.
	if batch.Events[len(batch.Events)-1].EventSequenceNumber/recorder.Every >
		batch.PreviousEventSequenceNumber/recorder.Every {
		state, err := captureRunStateSnapshot()
		if err != nil {
			return err
		}
		stateBytes, err = recorder.Codecs.MarshalRunState(state)
		if err != nil {
			return err
		}
	}
	// Store the RunStateSnapshot with its batch, never ahead of that batch.
	return recorder.Store.Commit(ctx, batch, data, stateBytes)
	// docs:end recorder-capture
}

// MemoryStore is for tests and local examples. It does not survive a process exit.
// One process holds the writer; a distributed database must also check a writer lease.
type MemoryStore struct {
	mu   sync.Mutex
	runs map[string]*StoredRun
}

type StoredRun struct {
	Batches  [][]byte
	State    []byte
	Head     uint64
	Receipts map[string][]byte
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{runs: map[string]*StoredRun{}} }

func (store *MemoryStore) Commit(ctx context.Context, batch *gent.SaveBatch,
	data, state []byte,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	run := store.runs[batch.RunId]
	if run == nil {
		run = &StoredRun{Receipts: map[string][]byte{}}
		store.runs[batch.RunId] = run
	}
	if previous, found := run.Receipts[batch.BatchId]; found {
		if !bytes.Equal(previous, data) {
			return fmt.Errorf("receipt %s has different bytes", batch.BatchId)
		}
		return nil
	}
	if run.Head != batch.PreviousEventSequenceNumber {
		return fmt.Errorf("stored head %d does not match %d", run.Head,
			batch.PreviousEventSequenceNumber)
	}
	run.Batches = append(run.Batches, data)
	run.Receipts[batch.BatchId] = data
	run.Head = batch.Events[len(batch.Events)-1].EventSequenceNumber
	if state != nil {
		run.State = state
	}
	return nil
}

// Read returns a stable container. Stored byte buffers are read-only.
func (store *MemoryStore) Read(runId string) StoredRun {
	store.mu.Lock()
	defer store.mu.Unlock()
	run := store.runs[runId]
	return StoredRun{Batches: append([][]byte(nil), run.Batches...),
		State: run.State, Head: run.Head}
}

func replayBatches(runId string, saved [][]byte, codecs *gent.CodecRegistry) (
	*gent.RunState, error,
) {
	// docs:start restore-batches
	state := gent.NewRunState(runId)
	for _, data := range saved {
		var batch gent.SaveBatch
		if err := codecs.Unmarshal(data, &batch); err != nil {
			return nil, err
		}
		result, err := state.Apply(&batch, codecs)
		if err != nil {
			return nil, err
		}
		if result.Status != gent.ApplyApplied && result.Status != gent.ApplyDuplicate {
			return nil, fmt.Errorf("replay needs attention: %s: %s", result.Status, result.Detail)
		}
	}
	// docs:end restore-batches
	return state, nil
}

func replayTail(data []byte, tail [][]byte, codecs *gent.CodecRegistry) (*gent.RunState, error) {
	// docs:start restore-state
	loaded, err := codecs.UnmarshalRunState(data)
	if err != nil {
		return nil, err
	}
	if loaded.Status != gent.RunStateReady {
		return nil, fmt.Errorf("state needs attention: %s: %s", loaded.Status, loaded.Detail)
	}
	state := loaded.State
	// Query storage for batches after state.LastAppliedEventSequenceNumber().
	// docs:end restore-state
	// docs:start restore-tail
	for _, data := range tail {
		var batch gent.SaveBatch
		if err := codecs.Unmarshal(data, &batch); err != nil {
			return nil, err
		}
		result, err := state.Apply(&batch, codecs)
		if err != nil {
			return nil, err
		}
		if result.Status != gent.ApplyApplied && result.Status != gent.ApplyDuplicate {
			return nil, fmt.Errorf("tail needs attention: %s: %s", result.Status, result.Detail)
		}
	}
	// docs:end restore-tail
	return state, nil
}

func restoreContext(ctx context.Context, runner *executor.Runner[string], state *gent.RunState) (
	*gent.ExecutionContext, error,
) {
	// docs:start restore-context
	restored, err := runner.Restore(ctx, state)
	if err != nil {
		return nil, err
	}
	if restored.Status != gent.RestoreReady {
		return nil, fmt.Errorf("restore needs attention: %s: %s", restored.Status, restored.Detail)
	}
	execCtx := restored.Context
	// A successful Restore transfers state ownership to execCtx. No work has run yet.
	// docs:end restore-context
	return execCtx, nil
}

func continueTask(runner *executor.Runner[string], execCtx *gent.ExecutionContext) (
	string, *gent.ExecutionResult, error,
) {
	// docs:start restore-continue
	answer, result, err := runner.Continue(execCtx)
	if err != nil {
		return "", nil, err
	}
	// NoActiveTurn means there is no unfinished task. Do not submit the old task again.
	return answer, result, nil
	// docs:end restore-continue
}

func replayArchive(runId string, saved [][]byte, codecs *gent.CodecRegistry) (
	*gent.RunView, error,
) {
	// docs:start archive-replay
	view := gent.NewRunView(runId)
	for _, data := range saved {
		var batch gent.SaveBatch
		if err := codecs.Unmarshal(data, &batch); err != nil {
			return nil, err
		}
		result, err := view.Apply(&batch, codecs)
		if err != nil {
			return nil, err
		}
		if result.Status != gent.ApplyApplied && result.Status != gent.ApplyDuplicate {
			return nil, fmt.Errorf("archive needs attention: %s", result.Status)
		}
	}
	// Decode separately for RunState. Successful Apply transfers retained values.
	// docs:end archive-replay
	return view, nil
}

func codecExample() (*gent.CodecRegistry, error) {
	// docs:start codec-register
	codecs := gent.NewCodecRegistry()
	if err := codecs.RegisterType("example:answer", 1, Answer{}); err != nil {
		return nil, err
	}
	// Register before encoding, decoding, or execution that can save this value.
	// docs:end codec-register
	return codecs, nil
}
