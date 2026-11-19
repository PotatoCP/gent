package gent

import (
	"context"
	core "github.com/rickchristie/gent/internal/core"
)

type ExecutionPhase = core.ExecutionPhase

// PhaseInitial means task admission and the turn reset are accepted before loop work.
const PhaseInitial ExecutionPhase = core.PhaseInitial

// PhaseIterationReady means the next Loop.Next entry has an accepted iteration position.
const PhaseIterationReady ExecutionPhase = core.PhaseIterationReady

// PhaseDecisionReady keeps a prepared reference-loop decision for managed continuation.
const PhaseDecisionReady ExecutionPhase = core.PhaseDecisionReady

// PhaseToolReady means the effective input and identity are saved before a managed tool call.
const PhaseToolReady ExecutionPhase = core.PhaseToolReady

// PhaseToolCompleted means a managed tool result and its plan position are accepted.
const PhaseToolCompleted ExecutionPhase = core.PhaseToolCompleted

// PhaseIterationCompleted means the completed iteration state is accepted before the next one.
const PhaseIterationCompleted ExecutionPhase = core.PhaseIterationCompleted

// PhaseCompactionCompleted means the new summary, selection, and trigger state are accepted.
const PhaseCompactionCompleted ExecutionPhase = core.PhaseCompactionCompleted

// PhaseTerminal means the completed-turn outcome and output are accepted.
const PhaseTerminal ExecutionPhase = core.PhaseTerminal

// PhasePaused preserves an unfinished turn at an intentional Stop or limit boundary.
const PhasePaused ExecutionPhase = core.PhasePaused

// PhaseRecoveryCompleted accepts managed recovery preparation before entering the Loop.
const PhaseRecoveryCompleted ExecutionPhase = core.PhaseRecoveryCompleted

type Checkpoint = core.Checkpoint

type ContextCheckpoint = core.ContextCheckpoint

type SavedCheckpointState = core.SavedCheckpointState

// SavedOutcome keeps the typed value with the result from one accepted completion or pause.
// Only a Finished outcome replaces RunState's last completed result.
type SavedOutcome = core.SavedOutcome

type CheckpointState = core.CheckpointState

var ErrIncompatibleCheckpoint = core.ErrIncompatibleCheckpoint

var ErrUnsupportedRecovery = core.ErrUnsupportedRecovery

// ToolIdentity lets a tool pass the stable logical ID to an external idempotency mechanism.
// AttemptId changes after takeover; ToolCallId does not.
type ToolIdentity = core.ToolIdentity

func ToolIdentityFromContext(ctx context.Context) (ToolIdentity, bool) {
	return core.ToolIdentityFromContext(ctx)
}
