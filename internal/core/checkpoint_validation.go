package gent

import (
	"encoding/json"
	"fmt"
	"reflect"
)

func validResumePhase(phase ExecutionPhase) bool {
	switch phase {
	case PhaseInitial, PhaseIterationReady, PhaseDecisionReady, PhaseToolReady, PhaseToolCompleted,
		PhaseIterationCompleted, PhaseCompactionCompleted:
		return true
	default:
		return false
	}
}

// Check saved control state before it can enter the accepted reducer or an execution context.
// These checks need no current Loop or application callbacks; component compatibility is separate.
func validateCheckpointState(checkpoint *Checkpoint, runtime RuntimeState) error {
	saved := checkpoint.Contexts[0]
	if len(runtime.Contexts) != 1 || saved.ContextId != runtime.Contexts[0].ContextId ||
		saved.Runtime.ContextId != saved.ContextId || saved.TurnId == "" || saved.AttemptId == "" ||
		saved.StartedAt.IsZero() {
		return fmt.Errorf("gent: checkpoint context, runtime, or turn identity is incomplete")
	}
	if saved.Iteration < 0 || saved.CompletedIteration < 0 ||
		saved.CompletedIteration > saved.Iteration || saved.Runtime.Iteration != saved.Iteration {
		return fmt.Errorf("gent: invalid checkpoint iteration position")
	}
	if err := validateRuntime(
		RuntimeState{
			Contexts: []ContextRuntime{
				saved.Runtime,
			},
		},
		runtime,
	); err != nil {
		return err
	}
	if saved.TurnOpen == (checkpoint.Phase == PhaseTerminal) {
		return fmt.Errorf("gent: checkpoint phase and unfinished turn disagree")
	}
	if checkpoint.Phase == PhasePaused || checkpoint.Phase == PhaseRecoveryCompleted {
		if !validResumePhase(checkpoint.ResumePhase) {
			return fmt.Errorf("gent: invalid saved resume phase %q", checkpoint.ResumePhase)
		}
	}
	if checkpoint.ResumePhase != "" && !validResumePhase(checkpoint.ResumePhase) {
		return fmt.Errorf("gent: invalid saved resume phase %q", checkpoint.ResumePhase)
	}
	if !validSavedValue(saved.Task) {
		return fmt.Errorf("gent: saved Task is missing or malformed")
	}
	for _, message := range saved.Instructions {
		if message == nil || message.Role != ChatMessageTypeSystem {
			return fmt.Errorf("gent: saved instruction prefix contains a non-system message")
		}
		if err := ValidateContentParts(message.Parts); err != nil {
			return fmt.Errorf("gent: invalid saved instruction content: %w", err)
		}
	}
	if _, found := saved.Extensions["data"]; !found {
		return fmt.Errorf("gent: saved LoopData is missing")
	}
	for name, extension := range saved.Extensions {
		if name == "" || extension.Type == "" || extension.Version <= 0 ||
			!validSavedValue(extension.Value) {
			return fmt.Errorf("gent: invalid saved component %q", name)
		}
	}
	for name, value := range saved.State {
		if name == "" || !validSavedValue(value) {
			return fmt.Errorf("gent: invalid saved state value %q", name)
		}
	}
	if checkpoint.Outcome != nil {
		if err := validateSavedOutcome(checkpoint.Outcome); err != nil {
			return err
		}
		result := checkpoint.Outcome.Result
		if result.TurnId != saved.TurnId ||
			(result.Status == ExecutionPaused) != (checkpoint.Phase == PhasePaused) {
			return fmt.Errorf("gent: checkpoint outcome does not match its turn and phase")
		}
	}
	if checkpoint.LastOutcome != nil {
		if err := validateSavedOutcome(checkpoint.LastOutcome); err != nil {
			return err
		}
		if checkpoint.LastOutcome.Result.Status != ExecutionFinished ||
			saved.TurnOpen && checkpoint.LastOutcome.Result.TurnId == saved.TurnId {
			return fmt.Errorf("gent: last completed result is not a completed earlier turn")
		}
	}
	if checkpoint.Phase == PhaseTerminal &&
		!reflect.DeepEqual(checkpoint.Outcome, checkpoint.LastOutcome) {
		return fmt.Errorf("gent: terminal outcome and last completed result disagree")
	}
	return nil
}

func validSavedValue(value EncodedValue) bool {
	return value.Type != "" && value.Version > 0 && json.Valid(value.Data)
}

func validateSavedOutcome(outcome *SavedOutcome) error {
	result := outcome.Result
	if result == nil || result.TurnId == "" || outcome.CompletedAt.IsZero() {
		return fmt.Errorf("gent: saved outcome has no result, turn, or completion time")
	}
	if result.Status == ExecutionFinished {
		if result.ExceededLimit != nil {
			return fmt.Errorf("gent: a completed result cannot contain a pause limit")
		}
		switch result.TerminationReason {
		case TerminationSuccess:
			if outcome.Value == nil || !validSavedValue(*outcome.Value) || result.Refusal != nil ||
				result.CancellationCause != nil {
				return fmt.Errorf("gent: saved success requires only an accepted value and output")
			}
			return ValidateContentParts(result.Output)
		case TerminationRefused:
			if result.Refusal == nil || result.CancellationCause != nil {
				return fmt.Errorf("gent: saved refusal requires refusal details")
			}
		case TerminationCanceled:
			if result.Refusal != nil {
				return fmt.Errorf("gent: saved cancellation cannot contain a refusal")
			}
		default:
			return fmt.Errorf("gent: invalid saved completion reason %q", result.TerminationReason)
		}
	} else if result.Status == ExecutionPaused {
		if result.Refusal != nil || result.CancellationCause != nil {
			return fmt.Errorf("gent: saved pause cannot contain terminal refusal or cancellation")
		}
		switch result.TerminationReason {
		case TerminationStopped:
			if result.ExceededLimit != nil {
				return fmt.Errorf("gent: saved Stop cannot contain a pause limit")
			}
		case TerminationLimitExceeded:
			if result.ExceededLimit == nil || result.ExceededLimit.Limit == nil ||
				result.ExceededLimit.MatchedKey == "" {
				return fmt.Errorf("gent: saved limit pause requires its effective limit")
			}
		default:
			return fmt.Errorf("gent: invalid saved pause reason %q", result.TerminationReason)
		}
	} else {
		return fmt.Errorf("gent: invalid saved outcome status %q", result.Status)
	}
	if outcome.Value != nil || len(result.Output) != 0 {
		return fmt.Errorf("gent: saved non-success outcome cannot contain accepted output")
	}
	return nil
}
