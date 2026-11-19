package gent

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
)

// A compact projection retains absolute order separately from its remaining entry count.
func prepareBatchAt(
	view RunView, batch *SaveBatch, digest string, entryOrder uint64,
	sources map[EntryId]historySource,
) (RunView, error) {
	if batch == nil {
		return RunView{}, errors.New("gent: nil save batch")
	}
	if batch.SchemaVersion != RunSchemaVersion || view.SchemaVersion != RunSchemaVersion {
		return RunView{}, errors.New("gent: unsupported run schema version")
	}
	if batch.RunId != view.RunId || batch.RunId == "" {
		return RunView{}, ErrWrongRun
	}
	if batch.BatchId == "" || batch.BatchId != digest {
		return RunView{}, errors.New("gent: batch digest mismatch")
	}
	if old, ok := view.Batches[batch.BatchId]; ok {
		if old != digest {
			return RunView{}, errors.New("gent: conflicting duplicate batch")
		}
		return view, nil
	}
	if batch.PreviousEventSequenceNumber != view.LastAppliedEventSequenceNumber || len(
		batch.Events,
	) == 0 {
		return RunView{}, ErrEventGap
	}
	next := view
	for _, event := range batch.Events {
		if event == nil {
			return RunView{}, errors.New("gent: nil recorded event")
		}
		if event.RunId != view.RunId {
			return RunView{}, ErrWrongRun
		}
		if event.EventSequenceNumber != next.LastAppliedEventSequenceNumber+1 {
			return RunView{}, ErrEventGap
		}
		if event.Type == "" || event.Ts.IsZero() {
			return RunView{}, errors.New("gent: event has no type or timestamp")
		}
		if recordedHistoryChange(event) != nil {
			if event.Type != "state_change" {
				return RunView{}, errors.New("gent: history change has wrong event type")
			}
			if err := applyHistoryAt(
				&next.History,
				*recordedHistoryChange(
					event,
				),
				next.RunId,
				entryOrder,
				sources,
			); err != nil {
				return RunView{}, err
			}
			entryOrder += uint64(len(recordedHistoryChange(event).AppendedEntries))
		}
		next.LastAppliedEventSequenceNumber = event.EventSequenceNumber
	}
	if batch.Runtime.Allocation < next.Runtime.Allocation {
		return RunView{}, errors.New("gent: allocation high-water mark moved backwards")
	}
	if batch.Runtime.Contexts != nil {
		if err := validateRuntime(next.Runtime, batch.Runtime); err != nil {
			return RunView{}, err
		}
		next.Runtime = batch.Runtime
	} else {
		next.Runtime.Allocation = batch.Runtime.Allocation
	}
	if batch.Checkpoint != nil {
		if err := validateCheckpoint(*batch.Checkpoint, next); err != nil {
			return RunView{}, err
		}
		next.Checkpoint = batch.Checkpoint
	}
	return next, nil
}

func applyHistory(history *HistorySnapshot, change HistoryChange, runId string) error {
	return applyHistoryAt(history, change, runId, uint64(len(history.Entries)), nil)
}

func applyHistoryAt(
	history *HistorySnapshot, change HistoryChange, runId string, entryOrder uint64,
	sources map[EntryId]historySource,
) error {
	if history.Revision == math.MaxUint64 {
		return errors.New("gent: history revision exhausted")
	}
	if uint64(len(change.AppendedEntries)) > math.MaxUint64-entryOrder {
		return errors.New("gent: history order exhausted")
	}
	if change.PreviousRevision != history.Revision || change.Revision != history.Revision+1 {
		return ErrHistoryConflict
	}
	entries := make(map[EntryId]*HistoryEntry, len(history.Entries)+len(change.AppendedEntries))
	for _, entry := range history.Entries {
		if entry == nil {
			return errors.New("gent: nil history entry")
		}
		entries[entry.Id] = entry
	}
	for i, entry := range change.AppendedEntries {
		if entry == nil {
			return errors.New("gent: nil history entry")
		}
		if entry.Id == "" || entry.RunId != runId || entry.ContextId == "" {
			return errors.New("gent: invalid history entry identity")
		}
		if entry.Order != entryOrder+uint64(i)+1 || entry.CreatedAt.IsZero() {
			return errors.New("gent: invalid history entry order or timestamp")
		}
		if _, exists := entries[entry.Id]; exists {
			return fmt.Errorf("gent: history entry %s already exists", entry.Id)
		}
		switch entry.Kind {
		case HistoryMessages, HistorySummary:
			if entry.Compaction != nil {
				return errors.New("gent: message entry cannot contain a compaction")
			}
			for _, message := range entry.Messages {
				if message == nil {
					return errors.New("gent: nil history message")
				}
			}
		case HistoryCompaction:
			if entry.Compaction == nil || len(entry.Messages) != 0 {
				return errors.New("gent: compaction entry requires a compaction only")
			}
		default:
			return fmt.Errorf("gent: unknown history entry kind %q", entry.Kind)
		}
		entries[entry.Id] = entry
	}
	for _, entry := range change.AppendedEntries {
		for _, replaced := range entry.Replaces {
			source, found := sources[replaced]
			if original := entries[replaced]; original != nil {
				source, found = sourceForEntry(original), true
			}
			if !found || source.ContextId != entry.ContextId || source.Order >= entry.Order {
				return errors.New("gent: invalid replacement reference")
			}
		}
		if entry.Compaction != nil {
			lists := [][]EntryId{entry.Compaction.InputIds, entry.Compaction.Active,
				entry.Compaction.SummaryIds}
			for _, ids := range lists {
				if err := validateSelection(entries, entry.ContextId, ids); err != nil {
					return err
				}
			}
			for _, id := range entry.Compaction.SummaryIds {
				if entries[id].Kind != HistorySummary {
					return errors.New("gent: compaction summary reference is not a summary")
				}
			}
		}
	}
	seenContexts := map[string]bool{}
	for _, selection := range change.Selections {
		if seenContexts[selection.ContextId] || selection.ContextId == "" {
			return errors.New("gent: duplicate or empty context selection")
		}
		seenContexts[selection.ContextId] = true
		if err := validateSelection(entries, selection.ContextId, selection.Active); err != nil {
			return err
		}
	}
	for _, state := range change.CustomState {
		if state.Name == "" || state.ContextId == "" || !json.Valid(state.Value.Data) {
			return errors.New("gent: invalid custom state replacement")
		}
	}
	history.Entries = append(append([]*HistoryEntry(nil), history.Entries...),
		change.AppendedEntries...)
	history.Changes = append(append([]HistoryChange(nil), history.Changes...), change)
	history.Revision = change.Revision
	return nil
}

func validateSelection(
	entries map[EntryId]*HistoryEntry, contextId string, active []EntryId,
) error {
	seen := map[EntryId]bool{}
	for _, id := range active {
		entry, ok := entries[id]
		if !ok || entry.ContextId != contextId || entry.Kind == HistoryCompaction {
			return fmt.Errorf("gent: invalid active entry %q in context %q", id, contextId)
		}
		if seen[id] {
			return fmt.Errorf("gent: duplicate active entry %q", id)
		}
		seen[id] = true
	}
	return nil
}

func validateRuntime(previous, next RuntimeState) error {
	seen := map[string]bool{}
	oldById := map[string]ContextRuntime{}
	for _, old := range previous.Contexts {
		oldById[old.ContextId] = old
	}
	for _, state := range next.Contexts {
		if state.ContextId == "" || seen[state.ContextId] {
			return errors.New("gent: invalid runtime context identity")
		}
		seen[state.ContextId] = true
		old := oldById[state.ContextId]
		if state.Iteration < 0 || state.Iteration < old.Iteration {
			return errors.New("gent: iteration position moved backwards")
		}
		for key, value := range state.Gauges {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return fmt.Errorf("gent: gauge %q has no finite value", key)
			}
		}
		if state.NextModelCall < old.NextModelCall || state.NextToolCall < old.NextToolCall {
			return errors.New("gent: call allocation moved backwards")
		}
		for key, value := range state.Counters {
			if value < 0 || value < oldById[state.ContextId].Counters[key] {
				return fmt.Errorf("gent: counter %q moved backwards", key)
			}
		}
		for key, baseline := range state.TurnCounterBaselines {
			if baseline < 0 || baseline > state.Counters[key] {
				return fmt.Errorf("gent: invalid turn baseline for counter %q", key)
			}
		}
		for key, value := range oldById[state.ContextId].Counters {
			if state.Counters[key] < value {
				return fmt.Errorf("gent: counter %q is missing", key)
			}
		}
	}
	for id := range oldById {
		if !seen[id] {
			return errors.New("gent: runtime context disappeared")
		}
	}
	return nil
}

func validateCheckpoint(checkpoint Checkpoint, view RunView) error {
	if checkpoint.SchemaVersion != RunSchemaVersion || checkpoint.RunId != view.RunId {
		return ErrIncompatibleCheckpoint
	}
	if checkpoint.EventSequenceNumber != view.LastAppliedEventSequenceNumber ||
		checkpoint.Revision != view.History.Revision {
		return errors.New("gent: checkpoint cursor or history revision mismatch")
	}
	if len(checkpoint.Contexts) != 1 {
		return errors.New("gent: checkpoint requires exactly one context")
	}

	switch checkpoint.Phase {
	case PhaseInitial, PhaseIterationReady, PhaseDecisionReady, PhaseToolReady, PhaseToolCompleted,
		PhaseIterationCompleted, PhaseCompactionCompleted, PhaseTerminal, PhasePaused,
		PhaseRecoveryCompleted:
	default:
		return ErrUnsupportedRecovery
	}
	hasOutcome := checkpoint.Phase == PhaseTerminal || checkpoint.Phase == PhasePaused
	if hasOutcome != (checkpoint.Outcome != nil) {
		return errors.New("gent: terminal checkpoint and outcome disagree")
	}
	seen := map[string]bool{}
	for _, state := range checkpoint.Contexts {
		if state.ContextId == "" || seen[state.ContextId] {
			return errors.New("gent: duplicate checkpoint context")
		}

		seen[state.ContextId] = true
		active, err := view.History.ContextAt(state.ContextId, checkpoint.Revision)
		if err != nil {
			return err
		}
		if !equalIds(active, state.Active) {
			return errors.New("gent: checkpoint selection differs from history")
		}
	}
	return validateCheckpointState(&checkpoint, view.Runtime)
}

func equalIds(a, b []EntryId) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ValidateBundle reconstructs history from the complete activity sequence. A bundle cannot claim
// a cursor for missing events or silently substitute a newer selection for an older checkpoint.
func ValidateBundle(bundle RunView, registries ...*CodecRegistry) error {
	if bundle.SchemaVersion != RunSchemaVersion || bundle.RunId == "" {
		return ErrIncompatibleCheckpoint
	}
	if _, err := registryOrDefault(registries).Encode(bundle); err != nil {
		return err
	}
	var history HistorySnapshot
	var checkpointRevision HistoryRevision
	for i, event := range bundle.Activity {
		if event == nil {
			return errors.New("gent: nil recorded event")
		}
		if event.RunId != bundle.RunId {
			return ErrWrongRun
		}
		if event.EventSequenceNumber != uint64(i+1) {
			return ErrEventGap
		}
		if event.Type == "" || event.Ts.IsZero() ||
			(recordedHistoryChange(event) != nil && event.Type != "state_change") {
			return errors.New("gent: malformed recorded event")
		}
		if recordedHistoryChange(event) != nil {
			if err := applyHistory(&history, *recordedHistoryChange(event), bundle.RunId); err != nil {
				return err
			}
		}
		if bundle.Checkpoint != nil &&
			event.EventSequenceNumber == bundle.Checkpoint.EventSequenceNumber {
			checkpointRevision = history.Revision
		}
	}
	if bundle.LastAppliedEventSequenceNumber != uint64(len(bundle.Activity)) {
		return ErrEventGap
	}
	if !reflect.DeepEqual(history, bundle.History) {
		return errors.New("gent: history does not match recorded transitions")
	}
	if bundle.Checkpoint != nil {
		if bundle.Checkpoint.EventSequenceNumber == 0 ||
			bundle.Checkpoint.Revision != checkpointRevision {
			return errors.New("gent: checkpoint revision differs from its committed cursor")
		}
		checkpointView := bundle
		checkpointView.LastAppliedEventSequenceNumber = bundle.Checkpoint.EventSequenceNumber
		checkpointView.History.Revision = bundle.Checkpoint.Revision
		if bundle.Checkpoint.EventSequenceNumber > bundle.LastAppliedEventSequenceNumber ||
			bundle.Checkpoint.Revision > bundle.History.Revision {
			return errors.New("gent: checkpoint is ahead of committed activity")
		}
		if err := validateCheckpoint(*bundle.Checkpoint, checkpointView); err != nil {
			return err
		}
	}
	return validateRuntime(RuntimeState{}, bundle.Runtime)
}
