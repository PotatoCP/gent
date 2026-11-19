package gent

import "encoding/json"

// MarshalJSON exposes accepted state for diagnostics without exposing mutable field references.
// The state must remain stable during this call. This JSON has no recovery format or integrity
// contract. Use CodecRegistry.MarshalRunState for a RunStateSnapshot and UnmarshalRunState to load
// it. Diagnostic output can include private tool evidence and must not become model input.
func (state *RunState) MarshalJSON() ([]byte, error) {
	return json.Marshal(state.data)
}
