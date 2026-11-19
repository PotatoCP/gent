package gent

import (
	"context"
	"fmt"
	"maps"
)

// Restore validates saved state and prepares fresh resources. It does not reset LoopData or run
// models, tools, recovery, or output processing. Success transfers ownership of state; failure
// leaves it owned by the caller. Only the accepted tail must be replayed after a RunStateSnapshot.
func (runner *Runner[T]) Restore(ctx context.Context, state *RunState) (*RestoreResult, error) {
	if result := state.validate(); result != nil {
		status := RestoreInvalid
		if result.Status == RunStateIncompatible {
			status = RestoreIncompatible
		}
		return &RestoreResult{Status: status, Detail: result.Detail}, nil
	}
	checkpoint := state.data.Checkpoint
	if checkpoint == nil || len(checkpoint.Contexts) != 1 || len(state.data.Runtime.Contexts) != 1 {
		return &RestoreResult{Status: RestoreInvalid,
			Detail: "gent: execution needs one accepted root checkpoint and runtime"}, nil
	}
	saved := checkpoint.Contexts[0]
	runtime := state.data.Runtime.Contexts[0]
	if saved.ContextId != runtime.ContextId || saved.TurnId == "" {
		return &RestoreResult{Status: RestoreInvalid,
			Detail: "gent: saved context, runtime, or turn identity is inconsistent"}, nil
	}
	execCtx, err := runner.NewExecutionContext(ctx, saved.Name)
	if err != nil {
		return nil, err
	}
	transferred := false
	defer func() {
		if !transferred {
			execCtx.Release()
		}
	}()
	// Check all component contracts before any restore callback. Matching a Runner version is
	// not required: new tools, prompts, limits, and compatible custom fields remain usable.
	for name, value := range saved.Extensions {
		component := execCtx.run.components[name]
		if component == nil || component.CheckpointType() != value.Type ||
			component.CheckpointVersion() != value.Version {
			return &RestoreResult{Status: RestoreIncompatible,
				Detail: fmt.Sprintf("gent: missing or incompatible saved component %q", name)}, nil
		}
	}
	if _, found := saved.Extensions["data"]; !found {
		return &RestoreResult{Status: RestoreInvalid, Detail: "gent: saved LoopData is missing"}, nil
	}
	// Saved encoded values must be usable with the new bindings before ownership transfers.
	for name, encoded := range saved.State {
		if _, result, err := checkRestoreValue(
			execCtx.Codecs(),
			encoded,
			name,
		); result != nil || err != nil {
			return result, err
		}
	}
	taskValue, taskResult, err := checkRestoreValue(execCtx.Codecs(), saved.Task, "Task")
	if taskResult != nil || err != nil {
		return taskResult, err
	}
	task, ok := taskValue.(*Task)
	if !ok || task == nil {
		return &RestoreResult{Status: RestoreInvalid,
			Detail: fmt.Sprintf("gent: saved Task must contain a task, received %T", taskValue)}, nil
	}
	if err := validateTask(task); err != nil {
		return &RestoreResult{
			Status: RestoreInvalid,
			Detail: fmt.Sprintf(
				"gent: saved Task: %v",
				err,
			),
		}, nil
	}
	if state.data.LastOutcome != nil && state.data.LastOutcome.Value != nil {
		if _, result, err := checkRestoreValue(execCtx.Codecs(), *state.data.LastOutcome.Value,
			"accepted output"); result != nil || err != nil {
			return result, err
		}
		if _, err := runner.outcomeValue(execCtx.Codecs(), state.data.LastOutcome); err != nil {
			return &RestoreResult{Status: RestoreIncompatible, Detail: err.Error()}, nil
		}
	}
	for name, value := range saved.Extensions {
		if err := execCtx.run.components[name].RestoreCheckpointState(&value.Value); err != nil {
			return nil, fmt.Errorf("gent: restore run %q component %q type %q version %d: %w",
				state.RunId(), name, value.Type, value.Version, err)
		}
	}
	execCtx.contextId, execCtx.turnId, execCtx.turnOpen = saved.ContextId, saved.TurnId, saved.TurnOpen
	execCtx.savedTask, execCtx.instructions = saved.Task, saved.Instructions
	execCtx.startTime = saved.StartedAt
	execCtx.iteration, execCtx.completedIteration = runtime.Iteration, saved.CompletedIteration
	execCtx.active = append([]EntryId(nil), saved.Active...)
	// A restored selection keeps its absolute revision. Resetting it would make stale update
	// tokens valid again and would change the logical state at the accepted savepoint.
	execCtx.draftRevision = uint64(state.data.History.Revision)
	// Maps are working containers. Their encoded values remain read-only; a later change replaces
	// a value rather than editing accepted bytes. No second deep copy of state is needed.
	execCtx.state = maps.Clone(saved.State)
	execCtx.phase, execCtx.resumePhase = checkpoint.Phase, checkpoint.ResumePhase
	execCtx.restored, execCtx.lastOutcome = true, state.data.LastOutcome
	execCtx.stats.counters = maps.Clone(runtime.Counters)
	execCtx.stats.turnBaselines = maps.Clone(runtime.TurnCounterBaselines)
	execCtx.stats.gauges = maps.Clone(runtime.Gauges)
	if execCtx.stats.counters == nil {
		execCtx.stats.counters = map[string]int64{}
	}
	if execCtx.stats.gauges == nil {
		execCtx.stats.gauges = map[string]float64{}
	}
	execCtx.nextModelCallSeq, execCtx.nextToolCallSeq = runtime.NextModelCall, runtime.NextToolCall
	execCtx.modelReplay = modelReplay{next: saved.Runtime.NextModelCall,
		end: runtime.NextModelCall, eventSequenceNumber: state.data.EventSequenceNumber,
		iteration: saved.Iteration}
	run := execCtx.run
	run.id, run.historyOrder = state.RunId(), state.data.HistoryOrder
	run.view = state.projection()
	run.view.Activity, run.view.replay = state.data.Evidence, state
	run.working = state.data.History
	run.nextEventSequenceNumber, run.liveEventSequenceNumber = state.data.EventSequenceNumber,
		state.data.EventSequenceNumber
	run.allocation, run.extensions = state.data.Runtime.Allocation, saved.Extensions
	if checkpoint.Outcome != nil {
		outcome := checkpoint.Outcome
		execCtx.result, execCtx.acceptedValue = outcome.Result, outcome.Value
		execCtx.terminationReason = outcome.Result.TerminationReason
		execCtx.finalResult, execCtx.refusal = outcome.Result.Output, outcome.Result.Refusal
		execCtx.endTime = outcome.CompletedAt
	}
	transferred = true
	return &RestoreResult{Status: RestoreReady, Context: execCtx}, nil
}

func checkRestoreValue(
	codecs *CodecRegistry, encoded EncodedValue, name string,
) (any, *RestoreResult, error) {
	decoder := valueDecoder{CodecRegistry: codecs, status: valueDecodeInvalid}
	value, err := decoder.value(encoded, 0)
	if err != nil {
		if decoder.status == valueDecodeOperationFailed {
			return nil, nil, fmt.Errorf("gent: restore %s: %w", name, err)
		}
		status := RestoreInvalid
		if decoder.status == valueDecodeIncompatible {
			status = RestoreIncompatible
		}
		return nil, &RestoreResult{
			Status: status,
			Detail: fmt.Sprintf(
				"gent: restore %s: %v",
				name,
				err,
			),
		}, nil
	}
	return value, nil, nil
}
