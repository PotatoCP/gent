package gent

import (
	"context"
	"errors"
	"time"
)

type ExecutionPhase string

const (
	// PhaseInitial means task admission and the turn reset are accepted before loop work.
	PhaseInitial ExecutionPhase = "initial"
	// PhaseIterationReady means the next Loop.Next entry has an accepted iteration position.
	PhaseIterationReady ExecutionPhase = "iteration_ready"
	// PhaseDecisionReady keeps a prepared reference-loop decision for managed continuation.
	PhaseDecisionReady ExecutionPhase = "decision_ready"
	// PhaseToolReady means the effective input and identity are saved before a managed tool call.
	PhaseToolReady ExecutionPhase = "tool_ready"
	// PhaseToolCompleted means a managed tool result and its plan position are accepted.
	PhaseToolCompleted ExecutionPhase = "tool_completed"
	// PhaseIterationCompleted means the completed iteration state is accepted before the next one.
	PhaseIterationCompleted ExecutionPhase = "iteration_completed"
	// PhaseCompactionCompleted means the new summary, selection, and trigger state are accepted.
	PhaseCompactionCompleted ExecutionPhase = "compaction_completed"
	// PhaseTerminal means the completed-turn outcome and output are accepted.
	PhaseTerminal ExecutionPhase = "terminal"
	// PhasePaused preserves an unfinished turn at an intentional Stop or limit boundary.
	PhasePaused ExecutionPhase = "paused"
	// PhaseRecoveryCompleted accepts managed recovery preparation before entering the Loop.
	PhaseRecoveryCompleted ExecutionPhase = "recovery_completed"
)

type Checkpoint struct {
	// LastOutcome retains the most recent completed turn, including canceled-turn replacement.
	LastOutcome *SavedOutcome `json:"lastOutcome,omitempty"`
	// ResumePhase keeps the control boundary while PhasePaused describes a saved pause.
	ResumePhase ExecutionPhase `json:"resumePhase,omitempty"`

	SchemaVersion       int                 `json:"schemaVersion"`
	RunId               string              `json:"runId"`
	Revision            HistoryRevision     `json:"revision"`
	EventSequenceNumber uint64              `json:"eventSequenceNumber"`
	Phase               ExecutionPhase      `json:"phase"`
	Contexts            []ContextCheckpoint `json:"contexts"`
	Outcome             *SavedOutcome       `json:"outcome,omitempty"`
}

type ContextCheckpoint struct {
	TurnId       string          `json:"turnId"`
	TurnOpen     bool            `json:"turnOpen"`
	Instructions []*ModelMessage `json:"instructions"`

	ContextId          string                          `json:"contextId"`
	Name               string                          `json:"name"`
	Task               EncodedValue                    `json:"task"`
	Active             []EntryId                       `json:"active"`
	Iteration          int                             `json:"iteration"`
	CompletedIteration int                             `json:"completedIteration"`
	AttemptId          string                          `json:"attemptId"`
	Runtime            ContextRuntime                  `json:"runtime"`
	StartedAt          time.Time                       `json:"startedAt"`
	State              map[string]EncodedValue         `json:"state"`
	Extensions         map[string]SavedCheckpointState `json:"extensions,omitempty"`
}

type SavedCheckpointState struct {
	Type    string       `json:"type"`
	Version int          `json:"version"`
	Value   EncodedValue `json:"value"`
}

// SavedOutcome keeps the typed value with the result from one accepted completion or pause.
// Only a Finished outcome replaces RunState's last completed result.
type SavedOutcome struct {
	Value       *EncodedValue    `json:"value,omitempty"`
	Result      *ExecutionResult `json:"result"`
	CompletedAt time.Time        `json:"completedAt"`
}

type CheckpointState interface {
	CheckpointType() string
	CheckpointVersion() int
	SaveCheckpointState() (*EncodedValue, error)
	RestoreCheckpointState(state *EncodedValue) error
}

var (
	ErrIncompatibleCheckpoint = errors.New("gent: incompatible checkpoint")
	ErrUnsupportedRecovery    = errors.New("gent: unsupported recovery phase")
)

type toolIdentityKey struct{}

// ToolIdentity lets a tool pass the stable logical ID to an external idempotency mechanism.
// AttemptId changes after takeover; ToolCallId does not.
type ToolIdentity struct {
	RunId      string
	ContextId  string
	ToolCallId string
	AttemptId  string
}

func ToolIdentityFromContext(ctx context.Context) (ToolIdentity, bool) {
	identity, ok := ctx.Value(toolIdentityKey{}).(ToolIdentity)
	return identity, ok
}
