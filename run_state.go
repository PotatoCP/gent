package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

type RunStateLoadStatus = core.RunStateLoadStatus

// RunStateReady means the decoded state is valid and available in State.
const RunStateReady RunStateLoadStatus = core.RunStateReady

// RunStateInvalid means the saved representation is corrupt or inconsistent.
const RunStateInvalid RunStateLoadStatus = core.RunStateInvalid

// RunStateIncompatible means a required format or codec is unavailable or unsupported.
const RunStateIncompatible RunStateLoadStatus = core.RunStateIncompatible

type RunStateLoadResult = core.RunStateLoadResult

type RestoreStatus = core.RestoreStatus

// RestoreReady means Context owns the restored state and is ready for execution.
const RestoreReady RestoreStatus = core.RestoreReady

// RestoreInvalid means the state is incomplete or inconsistent for execution.
const RestoreInvalid RestoreStatus = core.RestoreInvalid

// RestoreIncompatible means required current component bindings cannot restore the state.
const RestoreIncompatible RestoreStatus = core.RestoreIncompatible

type RestoreResult = core.RestoreResult

type ApplyStateStatus = core.ApplyStateStatus

// ApplyApplied means the complete batch was accepted into the replay target.
const ApplyApplied ApplyStateStatus = core.ApplyApplied

// ApplyDuplicate means the exact batch already has a known accepted receipt.
const ApplyDuplicate ApplyStateStatus = core.ApplyDuplicate

// ApplyCursorConflict means the batch cannot follow the current accepted cursor.
const ApplyCursorConflict ApplyStateStatus = core.ApplyCursorConflict

// ApplyReceiptRequired means storage must prove an older batch was already accepted.
const ApplyReceiptRequired ApplyStateStatus = core.ApplyReceiptRequired

// ApplyInvalid means the batch is malformed, has a bad digest, or belongs to another run.
const ApplyInvalid ApplyStateStatus = core.ApplyInvalid

// ApplyIncompatible means required format or codec support is unavailable.
const ApplyIncompatible ApplyStateStatus = core.ApplyIncompatible

type ApplyStateResult = core.ApplyStateResult

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
type RunState = core.RunState

// NewRunState creates an empty replay target at sequence zero. An empty ID is allowed here;
// Apply rejects it as ApplyInvalid. The target does not infer its identity from a batch, and a
// target without accepted initial execution state cannot be restored for execution.
func NewRunState(runId string) *RunState {
	return core.NewRunState(runId)
}
