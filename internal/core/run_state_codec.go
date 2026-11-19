package gent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// The envelope detects incomplete or altered storage values before decoding application content.
// It is an integrity check, not authentication. Applications still own trusted storage and fencing.
type runStateSnapshotEncoding struct {
	SchemaVersion int             `json:"schemaVersion"`
	Digest        string          `json:"digest"`
	State         json.RawMessage `json:"state"`
}

// MarshalRunState encodes a stable RunState as a RunStateSnapshot with format and integrity data.
// It borrows the state. Do not mutate or transfer it until this call returns. Recorder capture
// supplies an independent stable value when execution must continue during asynchronous encoding.
// A RunStateSnapshot is optional: complete SaveBatch replay restores the same logical state.
func (codecs *CodecRegistry) MarshalRunState(state *RunState) ([]byte, error) {
	if result := state.validate(); result != nil {
		return nil, fmt.Errorf("gent: encode RunStateSnapshot: %s", result.Detail)
	}
	data, err := codecs.Marshal(state.data)
	if err != nil {
		return nil, fmt.Errorf("gent: encode RunStateSnapshot for run %q: %w", state.RunId(), err)
	}
	sum := sha256.Sum256(data)
	encoded, err := json.Marshal(runStateSnapshotEncoding{SchemaVersion: RunSchemaVersion,
		Digest: hex.EncodeToString(sum[:]), State: data})
	if err != nil {
		return nil, err
	}
	return encoded, codecs.checkEncodedSize(len(encoded))
}

// UnmarshalRunState checks format, integrity, types, and logical state before returning Ready.
// The caller owns State only for RunStateReady. Invalid or unavailable saved data uses Status;
// a custom codec operation failure returns nil result and its wrapped error. No model, tool,
// output processing, or LoopData restore callback runs here.
//
// Apply the accepted tail to State, then use Runner.Restore. Full batch replay and this
// RunStateSnapshot-plus-tail path must produce the same state at the same accepted sequence.
func (codecs *CodecRegistry) UnmarshalRunState(data []byte) (*RunStateLoadResult, error) {
	invalid := func(detail string) (*RunStateLoadResult, error) {
		return &RunStateLoadResult{Status: RunStateInvalid, Detail: detail}, nil
	}
	if err := codecs.checkEncodedSize(len(data)); err != nil {
		return invalid(err.Error())
	}
	var wire runStateSnapshotEncoding
	if err := json.Unmarshal(data, &wire); err != nil {
		return invalid(fmt.Sprintf("gent: invalid RunStateSnapshot envelope: %v", err))
	}
	if wire.SchemaVersion != RunSchemaVersion {
		return &RunStateLoadResult{Status: RunStateIncompatible,
			Detail: fmt.Sprintf("gent: expected run schema %d, received %d",
				RunSchemaVersion, wire.SchemaVersion)}, nil
	}
	sum := sha256.Sum256(wire.State)
	if wire.Digest == "" || wire.Digest != hex.EncodeToString(sum[:]) {
		return invalid("gent: RunStateSnapshot digest mismatch")
	}
	var encoded EncodedValue
	if err := json.Unmarshal(wire.State, &encoded); err != nil {
		return invalid(fmt.Sprintf("gent: invalid RunStateSnapshot value: %v", err))
	}
	decoder := valueDecoder{CodecRegistry: codecs, status: valueDecodeInvalid}
	decoded, err := decoder.value(encoded, 0)
	if err != nil {
		switch decoder.status {
		case valueDecodeOperationFailed:
			return nil, fmt.Errorf("gent: decode RunStateSnapshot: %w", err)
		case valueDecodeIncompatible:
			return &RunStateLoadResult{Status: RunStateIncompatible, Detail: err.Error()}, nil
		default:
			return invalid(err.Error())
		}
	}
	saved, ok := decoded.(runStateData)
	if !ok {
		return invalid(fmt.Sprintf("gent: RunStateSnapshot contains %T, expected run state", decoded))
	}
	state := &RunState{data: saved}
	if result := state.validate(); result != nil {
		return result, nil
	}
	return &RunStateLoadResult{Status: RunStateReady, State: state}, nil
}

func (state *RunState) validate() *RunStateLoadResult {
	invalid := func(detail string) *RunStateLoadResult {
		return &RunStateLoadResult{Status: RunStateInvalid, Detail: detail}
	}
	data := &state.data
	if data.SchemaVersion != RunSchemaVersion {
		return &RunStateLoadResult{Status: RunStateIncompatible,
			Detail: fmt.Sprintf("gent: unsupported run schema %d", data.SchemaVersion)}
	}
	if data.RunId == "" {
		return invalid("gent: saved run ID is empty")
	}
	if (data.EventSequenceNumber == 0) != (data.LastBatchId == "") {
		return invalid("gent: saved event cursor and latest receipt disagree")
	}
	if data.EventSequenceNumber == 0 {
		if data.History.Revision != 0 || len(data.History.Entries) != 0 ||
			len(data.History.Changes) != 0 || data.HistoryOrder != 0 || len(data.HistorySources) != 0 ||
			data.Checkpoint != nil || len(data.Runtime.Contexts) != 0 || data.Runtime.Allocation != 0 ||
			len(data.Evidence) != 0 || data.LastOutcome != nil {
			return invalid("gent: empty event cursor has accepted state")
		}
		return nil
	}
	if receipt, err := hex.DecodeString(data.LastBatchId); err != nil || len(receipt) != sha256.Size {
		return invalid("gent: saved latest receipt is not a batch digest")
	}
	entries := make(map[EntryId]*HistoryEntry, len(data.History.Entries))
	var order uint64
	for _, entry := range data.History.Entries {
		if entry == nil || entry.Id == "" || entry.RunId != data.RunId || entry.ContextId == "" ||
			entry.CreatedAt.IsZero() || entry.Order <= order || entry.Order > data.HistoryOrder {
			return invalid("gent: invalid retained history identity, order, or timestamp")
		}
		if entries[entry.Id] != nil {
			return invalid(fmt.Sprintf("gent: duplicate retained history identity %q", entry.Id))
		}
		entries[entry.Id], order = entry, entry.Order
	}
	if err := validateHistorySources(data); err != nil {
		return invalid(err.Error())
	}
	var revision HistoryRevision
	for _, change := range data.History.Changes {
		if change.Revision < revision || change.Revision > data.History.Revision {
			return invalid("gent: invalid retained selection revision")
		}
		revision = change.Revision
		for _, selection := range change.Selections {
			if err := validateSelection(entries, selection.ContextId, selection.Active); err != nil {
				return invalid(err.Error())
			}
		}
	}
	if err := validateRuntime(RuntimeState{}, data.Runtime); err != nil {
		return invalid(err.Error())
	}
	var sequence uint64
	for _, event := range data.Evidence {
		if event == nil || event.RunId != data.RunId || event.EventSequenceNumber <= sequence ||
			event.EventSequenceNumber > data.EventSequenceNumber || event.Type == "" || event.Ts.IsZero() {
			return invalid("gent: invalid retained operation evidence")
		}
		sequence = event.EventSequenceNumber
		if err := validateRecordedPayload(event); err != nil {
			return invalid(err.Error())
		}
	}
	if data.Checkpoint == nil {
		return nil
	}
	checkpoint := data.Checkpoint
	if !reflect.DeepEqual(data.LastOutcome, checkpoint.LastOutcome) {
		return invalid("gent: saved last completed result differs from its checkpoint")
	}
	if checkpoint.EventSequenceNumber == 0 ||
		checkpoint.EventSequenceNumber > data.EventSequenceNumber ||
		checkpoint.Revision > data.History.Revision {
		return invalid("gent: checkpoint is outside the accepted state")
	}
	projection := state.projection()
	projection.LastAppliedEventSequenceNumber = checkpoint.EventSequenceNumber
	projection.History.Revision = checkpoint.Revision
	if err := validateCheckpoint(*checkpoint, projection); err != nil {
		if errors.Is(err, ErrIncompatibleCheckpoint) || errors.Is(err, ErrUnsupportedRecovery) {
			return &RunStateLoadResult{Status: RunStateIncompatible, Detail: err.Error()}
		}
		return invalid(err.Error())
	}
	return nil
}

func (state *RunState) capture(codecs *CodecRegistry) (*RunState, error) {
	// This copy has a specific purpose: later acceptance cannot change a Recorder's saved value.
	data, err := cloneOwned(codecs, state.data)
	if err != nil {
		return nil, err
	}
	return &RunState{data: data}, nil
}
