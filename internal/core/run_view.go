package gent

import (
	"fmt"
	"reflect"
)

// NewRunView creates an empty archive replay target with Gent's format metadata. An empty run ID
// is allowed at construction and rejected by Apply. The ID is never inferred from a batch.
// RunView retains the full archive; use RunState for bounded execution restoration.
func NewRunView(runId string) *RunView {
	return &RunView{SchemaVersion: RunSchemaVersion, RunId: runId,
		Batches: make(map[string]string), replay: NewRunState(runId)}
}

// Apply reconstructs the complete accepted archive without executing models, tools, Hooks, or
// output processing. It shares validation with RunState and retains every accepted event and
// original history entry. A RunStateSnapshot alone cannot reconstruct an earlier archive.
//
// Successful Apply takes ownership of retained batch values. The view, its fields, and all
// reachable values are read-only outside Apply. Duplicate or rejected Apply leaves both inputs
// unchanged. Unlike RunState, the complete archive keeps every accepted batch receipt.
func (view *RunView) Apply(batch *SaveBatch, codecs *CodecRegistry) (*ApplyStateResult, error) {
	state := view.replay
	if state == nil {
		var result *ApplyStateResult
		state, result = view.replayState()
		if result != nil {
			return result, nil
		}
	}
	if state.RunId() != view.RunId ||
		state.LastAppliedEventSequenceNumber() != view.LastAppliedEventSequenceNumber {
		return &ApplyStateResult{Status: ApplyInvalid,
			Detail: "gent: archive identity or cursor changed outside Apply"}, nil
	}
	result, err := state.checkBatch(batch, codecs)
	if err != nil {
		return nil, err
	}
	if result != nil {
		if result.Status == ApplyReceiptRequired && view.Batches[batch.BatchId] == batch.BatchId {
			return &ApplyStateResult{Status: ApplyDuplicate}, nil
		}
		return result, nil
	}
	candidate, result := state.prepare(batch)
	if result != nil {
		return result, nil
	}
	view.accept(candidate, batch)
	return &ApplyStateResult{Status: ApplyApplied}, nil
}

func (view *RunView) accept(candidate runStateData, batch *SaveBatch) {
	// All validation is complete. Extend the archive in place instead of copying its growing
	// history and receipt indexes for each batch. Existing entries and payloads remain immutable.
	for _, event := range batch.Events {
		if recordedHistoryChange(event) != nil {
			view.History.Entries = append(
				view.History.Entries,
				recordedHistoryChange(
					event,
				).AppendedEntries...,
			)
			view.History.Changes = append(view.History.Changes, *recordedHistoryChange(event))
			view.History.Revision = recordedHistoryChange(event).Revision
		}
	}
	view.Activity = append(view.Activity, batch.Events...)
	view.Runtime, view.Checkpoint = candidate.Runtime, candidate.Checkpoint
	view.LastAppliedEventSequenceNumber = candidate.EventSequenceNumber
	if view.Batches == nil {
		view.Batches = make(map[string]string)
	}
	view.Batches[batch.BatchId] = batch.BatchId
	view.replay = &RunState{data: candidate}
}

func (view *RunView) replayState() (*RunState, *ApplyStateResult) {
	state := NewRunState(view.RunId)
	state.data.SchemaVersion = view.SchemaVersion
	if view.LastAppliedEventSequenceNumber == 0 {
		if len(view.Activity) != 0 || !reflect.DeepEqual(view.History, HistorySnapshot{}) ||
			view.Checkpoint != nil || len(view.Batches) != 0 ||
			!reflect.DeepEqual(view.Runtime, RuntimeState{}) {
			return nil, &ApplyStateResult{Status: ApplyInvalid,
				Detail: "gent: empty archive cursor has accepted content"}
		}
		return state, nil
	}
	// Public archive fields do not include ordered batch receipts. They cannot supply the
	// latest receipt for a reducer. Never guess it from an unordered map or rebuild partial state.
	return nil, &ApplyStateResult{Status: ApplyInvalid,
		Detail: "gent: nonempty archive lacks its saved replay state"}
}

// The archive codec includes its compact reducer position so a decoded view can accept more
// batches without rebuilding the full archive. The public fields remain the complete archive.
type runViewData struct {
	SchemaVersion                  int
	RunId                          string
	LastAppliedEventSequenceNumber uint64
	History                        HistorySnapshot
	Activity                       []*RecordedEvent
	Checkpoint                     *Checkpoint
	Runtime                        RuntimeState
	Batches                        map[string]string
	State                          *runStateData
}

func registerRunViewCodec(codecs *CodecRegistry) {
	addFrameworkValueCodec(
		codecs,
		func(
			encoder *valueEncoder,
			view RunView,
			depth int,
		) (
			EncodedValue,
			error,
		) {
			data := runViewData{SchemaVersion: view.SchemaVersion, RunId: view.RunId,
				LastAppliedEventSequenceNumber: view.LastAppliedEventSequenceNumber,
				History:                        view.History,
				Activity:                       view.Activity,
				Checkpoint:                     view.Checkpoint,
				Runtime:                        view.Runtime, Batches: view.Batches}
			if view.replay != nil {
				data.State = &view.replay.data
			}
			return encoder.value(data, depth)
		},
		func(decoder *valueDecoder, value decodingValue, depth int) (RunView, error) {
			data, err := decodeSavedAs[runViewData](decoder, value, depth)
			if err != nil {
				return RunView{}, err
			}
			view := RunView{SchemaVersion: data.SchemaVersion, RunId: data.RunId,
				LastAppliedEventSequenceNumber: data.LastAppliedEventSequenceNumber,
				History:                        data.History,
				Activity:                       data.Activity,
				Checkpoint:                     data.Checkpoint,
				Runtime:                        data.Runtime, Batches: data.Batches}
			if data.State != nil {
				view.replay = &RunState{data: *data.State}
				if result := view.replay.validate(); result != nil {
					if result.Status == RunStateIncompatible {
						decoder.status = valueDecodeIncompatible
					}
					return RunView{}, fmt.Errorf("gent: invalid archive replay state: %s", result.Detail)
				}
				if err := view.validateReplayProjection(); err != nil {
					return RunView{}, err
				}
			} else if _, result := view.replayState(); result != nil {
				return RunView{}, fmt.Errorf("%s", result.Detail)
			}
			return view, nil
		},
	)
}

// Decode checks the saved compact reducer against the archive once. Normal Apply validates the
// new batch without scanning earlier archive records. Execution restoration never loads RunView.
func (view *RunView) validateReplayProjection() error {
	data := &view.replay.data
	if data.RunId != view.RunId || data.SchemaVersion != view.SchemaVersion ||
		data.EventSequenceNumber != view.LastAppliedEventSequenceNumber ||
		data.EventSequenceNumber != uint64(len(view.Activity)) ||
		data.HistoryOrder != uint64(len(view.History.Entries)) ||
		!reflect.DeepEqual(data.Runtime, view.Runtime) ||
		!reflect.DeepEqual(data.Checkpoint, view.Checkpoint) {
		return fmt.Errorf("gent: archive and replay state disagree")
	}
	if data.EventSequenceNumber > 0 && view.Batches[data.LastBatchId] != data.LastBatchId {
		return fmt.Errorf("gent: archive lacks its latest accepted receipt")
	}
	history := retainedHistory(view.History, view.Checkpoint)
	if !reflect.DeepEqual(data.History, history) ||
		!reflect.DeepEqual(data.HistorySources, retainedHistorySources(nil, view.History, history)) ||
		!reflect.DeepEqual(data.Evidence, retainedEvidence(nil, view.Activity, view.Checkpoint)) {
		return fmt.Errorf("gent: archive retained projection differs from its replay state")
	}
	return nil
}
