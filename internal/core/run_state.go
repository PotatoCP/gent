package gent

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

type RunStateLoadStatus string

const (
	// RunStateReady means the decoded state is valid and available in State.
	RunStateReady RunStateLoadStatus = "ready"

	// RunStateInvalid means the saved representation is corrupt or inconsistent.
	RunStateInvalid RunStateLoadStatus = "invalid"

	// RunStateIncompatible means a required format or codec is unavailable or unsupported.
	RunStateIncompatible RunStateLoadStatus = "incompatible"
)

type RunStateLoadResult struct {
	Status RunStateLoadStatus
	State  *RunState
	Detail string
}

type RestoreStatus string

const (
	// RestoreReady means Context owns the restored state and is ready for execution.
	RestoreReady RestoreStatus = "ready"

	// RestoreInvalid means the state is incomplete or inconsistent for execution.
	RestoreInvalid RestoreStatus = "invalid"

	// RestoreIncompatible means required current component bindings cannot restore the state.
	RestoreIncompatible RestoreStatus = "incompatible"
)

type RestoreResult struct {
	Status  RestoreStatus
	Context *ExecutionContext
	Detail  string
}

type ApplyStateStatus string

const (
	// ApplyApplied means the complete batch was accepted into the replay target.
	ApplyApplied ApplyStateStatus = "applied"

	// ApplyDuplicate means the exact batch already has a known accepted receipt.
	ApplyDuplicate ApplyStateStatus = "duplicate"

	// ApplyCursorConflict means the batch cannot follow the current accepted cursor.
	ApplyCursorConflict ApplyStateStatus = "cursor_conflict"

	// ApplyReceiptRequired means storage must prove an older batch was already accepted.
	ApplyReceiptRequired ApplyStateStatus = "receipt_required"

	// ApplyInvalid means the batch is malformed, has a bad digest, or belongs to another run.
	ApplyInvalid ApplyStateStatus = "invalid"

	// ApplyIncompatible means required format or codec support is unavailable.
	ApplyIncompatible ApplyStateStatus = "incompatible"
)

type ApplyStateResult struct {
	Status                              ApplyStateStatus
	Detail                              string
	ExpectedPreviousEventSequenceNumber uint64
	ReceivedPreviousEventSequenceNumber uint64
}

// RunState holds the accepted logical execution state. Its private fields keep its cursor,
// retained context, accounting, and recovery evidence consistent. It is not the full archive.
// Capture through Recorder.Save and use CodecRegistry.MarshalRunState for a RunStateSnapshot.
//
// Full batch replay == RunStateSnapshot + accepted tail == accepted logical state at the same
// position. Both paths preserve custom data, managed recovery, counters, and accepted output.
// This guarantees saved state, not identical future model output or reversal of external effects.
// Custom Next restarts at entry; its author owns repeated local work and new operation identities.
// See Recorder for the managed-execution and application storage contracts.
//
// Successful Apply takes ownership of retained batch values. Do not mutate them through old
// references. Decode returns caller-owned state; successful Runner.Restore transfers it to the
// ExecutionContext. A rejected Apply or failed Restore transfers no ownership. Do not encode or
// apply concurrently with mutation or ownership transfer.
type RunState struct {
	data runStateData
}

// runStateData is the private saved layout. The public RunState cannot expose independently
// mutable cursor and history fields. Runtime clients and worker resources never enter this value.
type runStateData struct {
	SchemaVersion       int
	RunId               string
	EventSequenceNumber uint64
	LastBatchId         string
	History             HistorySnapshot
	HistoryOrder        uint64
	HistorySources      map[EntryId]historySource
	Checkpoint          *Checkpoint
	Runtime             RuntimeState
	Evidence            []*RecordedEvent
	LastOutcome         *SavedOutcome
}

// NewRunState creates an empty replay target at sequence zero. An empty ID is allowed here;
// Apply rejects it as ApplyInvalid. The target does not infer its identity from a batch, and a
// target without accepted initial execution state cannot be restored for execution.
func NewRunState(runId string) *RunState {
	return &RunState{data: runStateData{SchemaVersion: RunSchemaVersion, RunId: runId}}
}

// RunId identifies the saved conversation across turns and worker attempts.
func (state *RunState) RunId() string { return state.data.RunId }

// LastAppliedEventSequenceNumber is the final accepted event position, or zero for an empty target.
// A live preview position does not establish persistence or advance this cursor.
func (state *RunState) LastAppliedEventSequenceNumber() uint64 {
	return state.data.EventSequenceNumber
}

// Apply accepts one complete batch without running models, tools, Hooks, or output processing.
// Full replay and RunStateSnapshot plus accepted tail must produce the same accepted logical state.
// Invalid, incompatible, duplicate, conflicting, and unproven old batches leave both inputs intact.
// A successful Apply takes ownership of retained values; those values must remain read-only.
// Older unknown receipts require proof from application storage. RunState keeps only the latest.
func (state *RunState) Apply(batch *SaveBatch, codecs *CodecRegistry) (*ApplyStateResult, error) {
	result, err := state.checkBatch(batch, codecs)
	if result != nil || err != nil {
		return result, err
	}
	candidate, result := state.prepare(batch)
	if result != nil {
		return result, nil
	}
	state.data = candidate
	return &ApplyStateResult{Status: ApplyApplied}, nil
}

func (state *RunState) checkBatch(
	batch *SaveBatch, codecs *CodecRegistry,
) (*ApplyStateResult, error) {
	invalid := func(detail string) (*ApplyStateResult, error) {
		return &ApplyStateResult{Status: ApplyInvalid, Detail: detail}, nil
	}
	if state.data.RunId == "" {
		return invalid("gent: replay target run ID is empty")
	}
	if batch.RunId == "" {
		return invalid("gent: batch run ID is empty")
	}
	if batch.RunId != state.data.RunId {
		return invalid(fmt.Sprintf("gent: expected run %q, received %q", state.data.RunId, batch.RunId))
	}
	if state.data.SchemaVersion != RunSchemaVersion || batch.SchemaVersion != RunSchemaVersion {
		return &ApplyStateResult{Status: ApplyIncompatible,
			Detail: fmt.Sprintf("gent: expected run schema %d; state has %d, batch has %d",
				RunSchemaVersion, state.data.SchemaVersion, batch.SchemaVersion)}, nil
	}
	if len(batch.Events) == 0 ||
		uint64(len(batch.Events)) > math.MaxUint64-batch.PreviousEventSequenceNumber {
		return invalid("gent: batch event sequence is empty or overflows uint64")
	}
	for i, event := range batch.Events {
		if event == nil || event.RunId != batch.RunId || event.Type == "" || event.Ts.IsZero() {
			return invalid(fmt.Sprintf("gent: invalid event envelope at batch position %d", i))
		}
		if event.EventSequenceNumber != batch.PreviousEventSequenceNumber+uint64(i)+1 {
			return invalid(fmt.Sprintf("gent: nonconsecutive event at batch position %d", i))
		}
		if err := validateRecordedPayload(event); err != nil {
			return invalid(err.Error())
		}
	}
	encoder := newValueEncoder(codecs)
	digest, err := batchDigest(batch, encoder)
	if err != nil {
		switch encoder.status {
		case valueEncodeIncompatible:
			return &ApplyStateResult{Status: ApplyIncompatible, Detail: err.Error()}, nil
		case valueEncodeInvalid:
			return invalid(err.Error())
		}
		return nil, fmt.Errorf("gent: validate batch for run %q: %w", batch.RunId, err)
	}
	if batch.BatchId == "" || batch.BatchId != digest {
		return invalid("gent: batch digest mismatch")
	}
	if state.data.LastBatchId == batch.BatchId {
		return &ApplyStateResult{Status: ApplyDuplicate}, nil
	}
	cursor := state.data.EventSequenceNumber
	last := batch.Events[len(batch.Events)-1].EventSequenceNumber
	if last <= cursor {
		return &ApplyStateResult{Status: ApplyReceiptRequired,
			Detail: "gent: prove this older batch's exact accepted receipt in storage"}, nil
	}
	if batch.PreviousEventSequenceNumber != cursor {
		return &ApplyStateResult{Status: ApplyCursorConflict,
			Detail:                              "gent: batch does not follow the accepted event sequence",
			ExpectedPreviousEventSequenceNumber: cursor,
			ReceivedPreviousEventSequenceNumber: batch.PreviousEventSequenceNumber}, nil
	}
	return nil, nil
}

func (state *RunState) projection() RunView {
	return RunView{SchemaVersion: state.data.SchemaVersion, RunId: state.data.RunId,
		LastAppliedEventSequenceNumber: state.data.EventSequenceNumber,
		History:                        state.data.History,
		Checkpoint:                     state.data.Checkpoint,
		Runtime:                        state.data.Runtime}
}

// prepare shares validation with archive replay and live acceptance. It copies only bounded
// retained index slices. No accepted state changes until every batch transition has passed.
func (state *RunState) prepare(batch *SaveBatch) (runStateData, *ApplyStateResult) {
	next, err := prepareBatchAt(
		state.projection(),
		batch,
		batch.BatchId,
		state.data.HistoryOrder,
		state.data.HistorySources,
	)
	if err != nil {
		status := ApplyInvalid
		if errors.Is(err, ErrIncompatibleCheckpoint) || errors.Is(err, ErrUnsupportedRecovery) {
			status = ApplyIncompatible
		}
		return runStateData{}, &ApplyStateResult{Status: status, Detail: err.Error()}
	}
	data := state.data
	data.EventSequenceNumber = next.LastAppliedEventSequenceNumber
	data.LastBatchId = batch.BatchId
	data.Checkpoint, data.Runtime = next.Checkpoint, next.Runtime
	for _, event := range batch.Events {
		if recordedHistoryChange(event) != nil {
			data.HistoryOrder += uint64(len(recordedHistoryChange(event).AppendedEntries))
		}
	}
	data.History = retainedHistory(next.History, data.Checkpoint)
	data.HistorySources = retainedHistorySources(state.data.HistorySources, next.History, data.History)
	data.Evidence = retainedEvidence(state.data.Evidence, batch.Events, data.Checkpoint)
	if data.Checkpoint != nil {
		data.LastOutcome = data.Checkpoint.LastOutcome
	}
	return data, nil
}

// References to archived originals remain provenance. They do not force those originals back
// into execution memory. Keep the current and resumable selections with their absolute revisions.
func retainedHistory(history HistorySnapshot, checkpoint *Checkpoint) HistorySnapshot {
	selected := map[string][]EntryId{}
	for _, change := range history.Changes {
		for _, selection := range change.Selections {
			selected[selection.ContextId] = selection.Active
		}
	}
	needed := map[EntryId]bool{}
	var contexts []string
	for contextId, ids := range selected {
		contexts = append(contexts, contextId)
		for _, id := range ids {
			needed[id] = true
		}
	}
	sort.Strings(contexts)
	current := HistoryChange{Revision: history.Revision}
	for _, contextId := range contexts {
		current.Selections = append(current.Selections,
			ContextSelection{ContextId: contextId, Active: selected[contextId]})
	}
	result := HistorySnapshot{Revision: history.Revision}
	if checkpoint != nil {
		earlier := HistoryChange{Revision: checkpoint.Revision}
		for _, context := range checkpoint.Contexts {
			for _, id := range context.Active {
				needed[id] = true
			}
			earlier.Selections = append(earlier.Selections,
				ContextSelection{ContextId: context.ContextId, Active: context.Active})
		}
		if checkpoint.Revision < history.Revision {
			result.Changes = append(result.Changes, earlier)
		}
	}
	if history.Revision > 0 {
		result.Changes = append(result.Changes, current)
	}
	for _, entry := range history.Entries {
		if needed[entry.Id] {
			result.Entries = append(result.Entries, entry)
		}
	}
	return result
}

func retainedEvidence(
	previous, added []*RecordedEvent, checkpoint *Checkpoint,
) []*RecordedEvent {
	if checkpoint == nil {
		return append(append([]*RecordedEvent(nil), previous...), added...)
	}
	iterations := map[string]int{}
	for _, context := range checkpoint.Contexts {
		iterations[context.ContextId] = context.Iteration
	}
	var retained []*RecordedEvent
	for _, events := range [][]*RecordedEvent{previous, added} {
		for _, event := range events {
			if event.ModelCallId == "" && event.ToolCallId == "" {
				continue
			}
			if event.EventSequenceNumber > checkpoint.EventSequenceNumber ||
				(checkpoint.Phase != PhaseTerminal &&
					event.Iteration >= iterations[event.ContextId]) {
				retained = append(retained, event)
			}
		}
	}
	return retained
}
