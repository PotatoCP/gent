package gent

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math"

	"sync"
	"time"
)

type executionRun struct {
	subscribers  []*subscriberWorker
	historyOrder uint64

	mu                      sync.RWMutex
	flushMu                 sync.Mutex
	view                    RunView
	recorder                Recorder
	failed                  *PersistenceError
	codecs                  *CodecRegistry
	id                      string
	attempt                 string
	allocation              uint64
	working                 HistorySnapshot
	pending                 []*RecordedEvent
	pendingSizes            []int
	pendingBytes            int
	policy                  RecordingConfig
	nextEventSequenceNumber uint64
	liveEventSequenceNumber uint64
	deferLive               bool
	err                     error
	errRevision             uint64
	root                    *ExecutionContext
	components              map[string]CheckpointState
	extensions              map[string]SavedCheckpointState
}

func randomIdentity() string {
	// crypto/rand.Read cannot fail on supported Go platforms; Go terminates the process on
	// unrecoverable system entropy failure. IDs must not repeat across worker processes.
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(fmt.Errorf("gent: allocate run identity: %w", err))
	}
	return fmt.Sprintf("%x", value[:])
}

func (execCtx *ExecutionContext) initializeHistory(config *Config) {
	runId := "run:" + randomIdentity()
	execCtx.run = &executionRun{recorder: config.Recorder, view: *NewRunView(runId),
		codecs: config.Codecs, id: runId, attempt: randomIdentity(), root: execCtx,
		policy: *config.Recording}
	execCtx.contextId = runId + ":root"
	execCtx.state = map[string]EncodedValue{}
	execCtx.phase = PhaseInitial
	var err error
	execCtx.savedTask, err = config.Codecs.Encode((*Task)(nil))
	if err != nil {
		execCtx.run.err = fmt.Errorf("gent: initial task: %w", err)
	}
}

func (execCtx *ExecutionContext) RunId() string { return execCtx.run.id }

func (execCtx *ExecutionContext) AttemptId() string {
	execCtx.run.mu.RLock()
	defer execCtx.run.mu.RUnlock()
	return execCtx.run.attempt
}

func (execCtx *ExecutionContext) Codecs() *CodecRegistry { return execCtx.run.codecs }

func (execCtx *ExecutionContext) RecordingError() error {
	execCtx.run.mu.RLock()
	defer execCtx.run.mu.RUnlock()
	return execCtx.run.err
}

func (execCtx *ExecutionContext) stopRecording(err error) error {
	if err == nil {
		return nil
	}
	run := execCtx.run
	defer run.root.cancel(err)
	for {
		run.mu.RLock()
		previous, revision := run.err, run.errRevision
		run.mu.RUnlock()
		// Matching can call application-defined Is or Unwrap methods that inspect the run.
		// Keep it outside run.mu and retry if another failure changes the record meanwhile.
		combined := err
		if previous != nil {
			if errors.Is(previous, err) {
				return err
			}
			// An incoming joined error can contain the earlier failure AND a storage failure.
			// Keep that complete error, including its uncertain batch, without duplicating it.
			if !errors.Is(err, previous) {
				combined = errors.Join(previous, err)
			}
		}
		run.mu.Lock()
		if run.errRevision != revision {
			run.mu.Unlock()
			continue
		}
		run.err = combined
		run.errRevision++
		run.mu.Unlock()
		return err
	}
}

func (execCtx *ExecutionContext) Task() (*Task, error) {
	execCtx.mu.RLock()
	saved := execCtx.savedTask
	execCtx.mu.RUnlock()
	return decodeAs[*Task](execCtx.run.codecs, saved)
}

// History returns a read-only view of accepted history. Entries and payloads must not be changed.
// Later appends use new selection slices, so the retained view stays valid. It contains only
// retained execution history. Replay the stored batches into RunView to inspect the full archive.
func (execCtx *ExecutionContext) History() HistorySnapshot {
	execCtx.run.mu.RLock()
	defer execCtx.run.mu.RUnlock()
	return execCtx.run.view.History
}

func (execCtx *ExecutionContext) ContextDraft() ContextDraft {
	execCtx.run.mu.RLock()
	defer execCtx.run.mu.RUnlock()
	return ContextDraft{
		Revision: execCtx.draftRevision,
		Active: append(
			[]EntryId(
				nil,
			),
			execCtx.active...,
		),
	}
}

func (execCtx *ExecutionContext) Scratchpad() ([]*HistoryEntry, error) {
	input, err := execCtx.CompactionInput()
	if err != nil {
		return nil, err
	}
	return input.Entries, nil
}

// CompactionInput captures selection and revision under one lock. A strategy cannot accidentally
// pair an old list of IDs with records from a concurrent context change. Entries and all reachable
// values are borrowed read-only. Create a new projection when a strategy must shorten content.
func (execCtx *ExecutionContext) CompactionInput() (*CompactionInput, error) {
	execCtx.run.mu.RLock()
	entries := make([]*HistoryEntry, len(execCtx.active))
	for i, id := range execCtx.active {
		var ok bool
		entries[i], ok = execCtx.run.working.Entry(id)
		if !ok {
			execCtx.run.mu.RUnlock()
			return nil, fmt.Errorf("gent: missing active history entry %s", id)
		}
	}
	revision := execCtx.draftRevision
	required, err := execCtx.requiredHistoryLocked()
	execCtx.run.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	return &CompactionInput{Revision: revision, Entries: entries, Required: required}, nil
}

// NewHistoryEntry assigns a new identity without changing the caller's entry header. It takes
// ownership of nested payloads. The proposal can be edited until UpdateHistory accepts it.
// Do not change transferred payloads through old aliases.
func (execCtx *ExecutionContext) NewHistoryEntry(input *HistoryEntry) (*HistoryEntry, error) {
	if err := execCtx.RecordingError(); err != nil {
		return nil, err
	}
	if input == nil {
		return nil, errors.New("gent: nil history entry")
	}
	value := *input
	entry := &value
	if entry.Kind == "" {
		entry.Kind = HistoryMessages
	}
	iteration := execCtx.Iteration()
	execCtx.run.mu.Lock()
	defer execCtx.run.mu.Unlock()
	if execCtx.run.allocation == math.MaxUint64 {
		return nil, errors.New("gent: history identity allocation exhausted")
	}
	execCtx.run.allocation++
	entry.Id = EntryId(
		fmt.Sprintf(
			"%s:entry:%s:%d",
			execCtx.run.id,
			execCtx.run.attempt,
			execCtx.run.allocation,
		),
	)
	entry.RunId = execCtx.run.id
	entry.ContextId = execCtx.contextId
	entry.AttemptId = execCtx.run.attempt
	entry.Iteration, entry.Order, entry.CreatedAt = iteration, 0, time.Now().UTC()
	return entry, nil
}

// UpdateHistory accepts an owned proposal. On success, entries and payloads become read-only.
// Event serialization captures durable evidence; another deep clone is not needed at submission.
func (execCtx *ExecutionContext) UpdateHistory(update HistoryUpdate) error {
	for _, entry := range update.AppendedEntries {
		if entry == nil {
			return errors.New("gent: nil history entry")
		}
	}
	for _, state := range update.CustomState {
		if state.ContextId != execCtx.ContextId() || state.Name == "" {
			return errors.New("gent: invalid custom state identity")
		}
		if _, err := execCtx.Codecs().Decode(state.Value); err != nil {
			return err
		}
	}
	iteration := execCtx.Iteration()
	execCtx.run.mu.Lock()
	if execCtx.run.err != nil {
		err := execCtx.run.err
		execCtx.run.mu.Unlock()
		return err
	}
	if update.ExpectedDraftRevision != execCtx.draftRevision {
		execCtx.run.mu.Unlock()
		return ErrHistoryConflict
	}
	required, err := execCtx.requiredHistoryLocked()
	if err != nil {
		execCtx.run.mu.Unlock()
		return err
	}
	update.Active = keepRequiredRecords(update.Active, required)
	if update.Compaction != nil {
		update.Compaction.Active = append([]EntryId(nil), update.Active...)
	}
	for _, entry := range update.AppendedEntries {
		if entry.Compaction != nil {
			entry.Compaction.Active = append([]EntryId(nil), update.Active...)
		}
	}
	change := HistoryChange{PreviousRevision: execCtx.run.working.Revision,
		Revision: execCtx.run.working.Revision + 1, AppendedEntries: update.AppendedEntries,
		Selections:  []ContextSelection{{execCtx.contextId, append([]EntryId(nil), update.Active...)}},
		CustomState: cloneWire(update.CustomState), Reason: update.Reason}
	if uint64(len(change.AppendedEntries)) > math.MaxUint64-execCtx.run.historyOrder {
		execCtx.run.mu.Unlock()
		return errors.New("gent: history order exhausted")
	}
	for i := range change.AppendedEntries {
		entry := change.AppendedEntries[i]
		if entry.ContextId != execCtx.contextId || entry.RunId != execCtx.run.id {
			execCtx.run.mu.Unlock()
			return ErrWrongRun
		}
		entry.Order = execCtx.run.historyOrder + uint64(i) + 1
	}
	if err := applyHistoryAt(&execCtx.run.working, change, execCtx.run.id, execCtx.run.historyOrder,
		execCtx.run.view.replay.data.HistorySources); err != nil {
		execCtx.run.mu.Unlock()
		return err
	}
	execCtx.run.historyOrder += uint64(len(change.AppendedEntries))
	execCtx.active = append([]EntryId(nil), update.Active...)
	for _, state := range change.CustomState {
		execCtx.state[state.Name] = cloneWire(state.Value)
	}
	execCtx.draftRevision++
	err = execCtx.run.queueEvent(&RecordedEvent{Type: "state_change",
		ContextId: execCtx.contextId, Iteration: iteration, AttemptId: execCtx.run.attempt,
		EventName: EventNameStateChange, Payload: &change})
	execCtx.run.mu.Unlock()
	if err != nil {
		return execCtx.stopRecording(err)
	}
	execCtx.Stats().SetGauge(SGScratchpadLength, float64(len(update.Active)))
	return execCtx.flushPendingLimit()
}

// AppendRecord is the ordinary append-and-select operation. Final answers can set active=false
// to retain evidence without changing the context used by a subsequent request.
func (execCtx *ExecutionContext) AppendRecord(record *HistoryEntry, active bool) error {
	entry, err := execCtx.NewHistoryEntry(record)
	if err != nil {
		return err
	}
	draft := execCtx.ContextDraft()
	if active {
		draft.Active = append(draft.Active, entry.Id)
	}
	return execCtx.UpdateHistory(HistoryUpdate{ExpectedDraftRevision: draft.Revision,
		AppendedEntries: []*HistoryEntry{entry}, Active: draft.Active, Reason: ContextAppend})
}

func (execCtx *ExecutionContext) ExpireRecords(nextIteration int) error {
	// Expiry needs only immutable entry metadata. Copying all selected media and tool payloads
	// here adds work on every iteration. Capture IDs and revision together, then let UpdateHistory
	// reject a stale selection if a concurrent append wins after the read lock is released.
	execCtx.run.mu.RLock()
	revision := execCtx.draftRevision
	active := make([]EntryId, 0, len(execCtx.active))
	for _, id := range execCtx.active {
		record, ok := execCtx.run.working.Entry(id)
		if !ok {
			execCtx.run.mu.RUnlock()
			return fmt.Errorf("gent: missing active history entry %s", id)
		}
		if record.ExpireAfterIteration > 0 && nextIteration >= record.ExpireAfterIteration {
			continue
		}
		active = append(active, record.Id)
	}
	unchanged := len(active) == len(execCtx.active)
	execCtx.run.mu.RUnlock()
	if unchanged {
		return nil
	}
	return execCtx.UpdateHistory(HistoryUpdate{ExpectedDraftRevision: revision,
		Active: active, Reason: ContextExpire})
}

func (execCtx *ExecutionContext) SetCheckpointState(name string, value any) error {
	encoded, err := execCtx.run.codecs.Encode(value)
	if err != nil {
		return err
	}
	execCtx.run.mu.Lock()
	defer execCtx.run.mu.Unlock()
	execCtx.state[name] = encoded
	return nil
}

func (execCtx *ExecutionContext) CheckpointState(name string) (any, bool, error) {
	execCtx.run.mu.RLock()
	value, ok := execCtx.state[name]
	execCtx.run.mu.RUnlock()
	if !ok {
		return nil, false, nil
	}
	decoded, err := execCtx.run.codecs.Decode(value)
	return decoded, true, err
}

func (execCtx *ExecutionContext) Phase() ExecutionPhase {
	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()
	return execCtx.phase
}

func (execCtx *ExecutionContext) setPhase(phase ExecutionPhase) {
	execCtx.mu.Lock()
	execCtx.phase = phase
	execCtx.mu.Unlock()
}

func (execCtx *ExecutionContext) ClearCheckpointState(names ...string) {
	execCtx.run.mu.Lock()
	defer execCtx.run.mu.Unlock()
	for _, name := range names {
		delete(execCtx.state, name)
	}
}
