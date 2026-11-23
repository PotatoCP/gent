package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/rickchristie/gent"
)

type replayPath string

const (
	// replayBatches reconstructs execution from every complete saved batch.
	replayBatches replayPath = "batches"
	// replayCapture loads a RunStateSnapshot and applies its accepted tail.
	replayCapture replayPath = "capture"
)

// Only encoded bytes are authoritative. Metadata indexes those bytes under the same lock. No
// ExecutionContext or decoded RunState lives here. Restarting the process discards this store.
type memoryStore struct {
	mu     sync.Mutex
	codecs *gent.CodecRegistry
	values map[string][]byte
	runs   map[string]*savedRun
}

type savedRun struct {
	lease   uint64
	head    uint64
	batches []string
	capture string
	tail    int
}

type storedBytes struct {
	batches [][]byte
	capture []byte
	tail    int
	head    uint64
}

type memoryRecorder struct {
	store *memoryStore
	runId string
	lease uint64
}

func newMemoryStore(codecs *gent.CodecRegistry) *memoryStore {
	return &memoryStore{codecs: codecs, values: map[string][]byte{}, runs: map[string]*savedRun{}}
}

func (store *memoryStore) clear() {
	store.mu.Lock()
	defer store.mu.Unlock()
	clear(store.values)
	clear(store.runs)
}

func (store *memoryStore) claim(runId string) *memoryRecorder {
	store.mu.Lock()
	defer store.mu.Unlock()
	run := store.runs[runId]
	if run == nil {
		run = &savedRun{}
		store.runs[runId] = run
	}
	run.lease++
	return &memoryRecorder{store: store, runId: runId, lease: run.lease}
}

func (writer *memoryRecorder) Save(ctx context.Context, batch *gent.SaveBatch,
	captureRunStateSnapshot func() (*gent.RunState, error),
) error {
	if batch.RunId != writer.runId {
		return errors.New("storage: wrong run")
	}
	data, err := writer.store.codecs.Marshal(batch)
	if err != nil {
		return err
	}
	// Capture every third batch before completion. Leaving a terminal tail makes both replay
	// operations visible in the example. All batches remain mandatory. Encode the capture before
	// the storage transaction accepts it; its cursor must never lead its accepted batch.
	writer.store.mu.Lock()
	run := writer.store.runs[writer.runId]
	if run == nil || run.lease != writer.lease {
		writer.store.mu.Unlock()
		return errors.New("storage: worker lease expired")
	}
	count := len(run.batches)
	writer.store.mu.Unlock()
	var captured []byte
	if count%3 == 0 && batch.Checkpoint.Phase != gent.PhaseTerminal {
		state, err := captureRunStateSnapshot()
		if err != nil {
			return err
		}
		captured, err = writer.store.codecs.MarshalRunState(state)
		if err != nil {
			return err
		}
	}
	return writer.commit(ctx, batch, data, captured)
}

func (writer *memoryRecorder) commit(ctx context.Context, batch *gent.SaveBatch,
	data, captured []byte,
) error {
	store := writer.store
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	run := store.runs[writer.runId]
	if run == nil || run.lease != writer.lease {
		return errors.New("storage: worker lease expired")
	}
	key := writer.runId + "/batch/" + batch.BatchId
	if saved, exists := store.values[key]; exists {
		if !bytes.Equal(saved, data) {
			return errors.New("storage: retry bytes differ")
		}
		return nil
	}
	if batch.PreviousEventSequenceNumber != run.head {
		return errors.New("storage: accepted cursor changed")
	}
	if len(batch.Events) == 0 {
		return errors.New("storage: empty batch")
	}
	// One in-memory transaction accepts bytes, ordering, retry receipt, and optional capture.
	store.values[key] = data
	run.batches = append(run.batches, key)
	run.head = batch.Events[len(batch.Events)-1].EventSequenceNumber
	if captured != nil {
		run.capture = writer.runId + "/state"
		store.values[run.capture] = captured
		run.tail = len(run.batches)
	}
	return nil
}

func (store *memoryStore) read(runId string) (*storedBytes, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	run, ok := store.runs[runId]
	if !ok {
		return nil, false
	}
	data := &storedBytes{capture: store.values[run.capture], tail: run.tail, head: run.head}
	for _, key := range run.batches {
		data.batches = append(data.batches, store.values[key])
	}
	// Each stored byte slice is immutable. A new slice of references freezes this read's head
	// without copying the archive bytes or holding the storage lock during decoding.
	return data, true
}

func (store *memoryStore) load(runId string, data *storedBytes, path replayPath) (
	*gent.RunState, error,
) {
	state, start := gent.NewRunState(runId), 0
	if path == replayCapture && data.capture != nil {
		loaded, err := store.codecs.UnmarshalRunState(data.capture)
		if err != nil {
			return nil, err
		}
		if loaded.Status != gent.RunStateReady {
			return nil, fmt.Errorf("load: %s: %s", loaded.Status, loaded.Detail)
		}
		state, start = loaded.State, data.tail
	}
	for _, encoded := range data.batches[start:] {
		var batch gent.SaveBatch
		if err := store.codecs.Unmarshal(encoded, &batch); err != nil {
			return nil, err
		}
		result, err := state.Apply(&batch, store.codecs)
		if err := checkApply(result, err); err != nil {
			return nil, err
		}
	}
	return state, nil
}

func checkApply(result *gent.ApplyStateResult, err error) error {
	if err != nil {
		return err
	}
	if result.Status != gent.ApplyApplied {
		return fmt.Errorf("replay: %s: %s", result.Status, result.Detail)
	}
	return nil
}

func (store *memoryStore) archive(runId string, data *storedBytes) (*gent.RunView, error) {
	view := gent.NewRunView(runId)
	for _, encoded := range data.batches {
		var batch gent.SaveBatch
		if err := store.codecs.Unmarshal(encoded, &batch); err != nil {
			return nil, err
		}
		result, err := view.Apply(&batch, store.codecs)
		if err := checkApply(result, err); err != nil {
			return nil, err
		}
	}
	return view, nil
}
