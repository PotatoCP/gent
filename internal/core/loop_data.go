package gent

import (
	"encoding/json"
	"fmt"
)

// CheckpointType identifies the empty standard data contract. Custom LoopData must replace its
// capture and restore methods so application fields are saved as part of the same savepoint.
func (*BasicLoopData) CheckpointType() string { return "gent:basic_loop_data" }

// CheckpointVersion is the standard data format version, independent of Runner implementation.
func (*BasicLoopData) CheckpointVersion() int { return 1 }

// SaveCheckpointState returns independent, stable bytes for the empty standard data state.
func (data *BasicLoopData) SaveCheckpointState() (*EncodedValue, error) {
	return &EncodedValue{Type: data.CheckpointType(), Version: data.CheckpointVersion(),
		Data: json.RawMessage(`{}`)}, nil
}

// RestoreCheckpointState accepts the empty standard state. The Task belongs to ExecutionContext.
func (data *BasicLoopData) RestoreCheckpointState(state *EncodedValue) error {
	if state.Type != data.CheckpointType() || state.Version != data.CheckpointVersion() {
		return fmt.Errorf("gent: BasicLoopData expected %s version %d; received %s version %d",
			data.CheckpointType(), data.CheckpointVersion(), state.Type, state.Version)
	}
	var empty struct{}
	if err := json.Unmarshal(state.Data, &empty); err != nil {
		return fmt.Errorf("gent: decode BasicLoopData: %w", err)
	}
	return nil
}

// ResetTurn has no fields to clear. Custom data must reset its own turn fields with local changes.
func (*BasicLoopData) ResetTurn() {}
