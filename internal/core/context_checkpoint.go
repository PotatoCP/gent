package gent

import (
	"context"
)

func (execCtx *ExecutionContext) CommitCheckpoint(phase ExecutionPhase) error {
	return execCtx.flushHistory(phase)
}

func (execCtx *ExecutionContext) flushHistory(phase ExecutionPhase) error {
	run := execCtx.run
	run.flushMu.Lock()
	locked := true
	defer func() {
		if locked {
			run.flushMu.Unlock()
		}
	}()
	if err := execCtx.RecordingError(); err != nil {
		return err
	}
	// Parallel tool calls can checkpoint together. Capture extension state under the same lock
	// as acceptance so concurrent saves cannot replace it with an older snapshot.
	if phase != "" {
		if err := execCtx.captureExtensions(); err != nil {
			return execCtx.stopRecording(err)
		}
	}
	run.mu.Lock()
	runtimeState := RuntimeState{Contexts: []ContextRuntime{execCtx.runtimeSnapshot()},
		Allocation: run.allocation}
	var checkpoint *Checkpoint
	var err error
	// Each approved boundary saves custom data and framework continuation together.
	if phase != "" {
		checkpoint, err = execCtx.prepareCheckpoint(phase, runtimeState)
	}
	if err == nil && phase == PhaseTerminal && execCtx.phase != PhaseTerminal {
		if execCtx.terminalEvent != nil {
			err = run.queueEvent(execCtx.terminalEvent)
			execCtx.terminalEvent = nil
		}
		if err == nil {
			err = execCtx.queueFinalContent()
		}
	}
	if err == nil && len(run.pending) == 0 && checkpoint != nil {
		err = run.queueEvent(&RecordedEvent{Type: "checkpoint"})
	}
	events := append([]*RecordedEvent(nil), run.pending...)
	pendingCount := len(events)
	pendingBytes := run.pendingBytes
	run.mu.Unlock()
	if err != nil {
		return execCtx.stopRecording(err)
	}
	if pendingCount == 0 && checkpoint == nil {
		return nil
	}
	// A canceled worker must still be able to record already observed evidence. Storage adapters
	// fence stale workers; the independent bounded context is not permission to ignore a lease.
	// run.save checks unexpected attempt failure at admission, after all capture and encoding.
	saveCtx, cancel := context.WithTimeout(
		context.WithoutCancel(execCtx.Context()), run.policy.SaveTimeout)
	defer cancel()
	batch, err := run.accept(saveCtx, &SaveBatch{Events: events, Checkpoint: checkpoint,
		Runtime: runtimeState})
	if err != nil {
		return execCtx.stopRecording(err)
	}
	run.mu.Lock()
	for i, event := range batch.Events {
		for _, worker := range run.subscribers {
			if worker.delivery != SubscriberLive {
				worker.send(event, run.pendingSizes[i])
			}
		}
	}
	for i, event := range batch.Events {
		run.publishLive(event, run.pendingSizes[i])
	}
	clear(run.pending[:pendingCount])
	run.pending = run.pending[pendingCount:]
	run.pendingSizes = run.pendingSizes[pendingCount:]
	run.pendingBytes -= pendingBytes
	if len(run.pending) == 0 {
		run.pending = nil
		run.pendingSizes = nil
	}
	run.deferLive = false
	for i, event := range run.pending {
		if event.Type == "execution_finished" || event.Type == "accepted_answer" ||
			event.Type == "execution_paused" {
			run.deferLive = true
		}
		if !run.deferLive {
			run.publishLive(event, run.pendingSizes[i])
		}
	}
	if phase != "" {
		execCtx.mu.Lock()
		execCtx.phase = phase
		if phase == PhaseIterationCompleted || phase == PhaseTerminal {
			execCtx.completedIteration = execCtx.iteration
		}
		execCtx.mu.Unlock()
	}
	run.mu.Unlock()
	run.flushMu.Unlock()
	locked = false
	return nil
}

// prepareCheckpoint runs with run.mu held; no application I/O is performed here.
func (execCtx *ExecutionContext) prepareCheckpoint(
	phase ExecutionPhase, runtimeState RuntimeState,
) (*Checkpoint, error) {
	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()
	state := ContextCheckpoint{
		TurnId: execCtx.turnId, TurnOpen: execCtx.turnOpen, Instructions: execCtx.instructions,
		ContextId: execCtx.contextId, Name: execCtx.name, Task: execCtx.savedTask,
		Active: append([]EntryId(nil), execCtx.active...), Iteration: execCtx.iteration,
		CompletedIteration: execCtx.completedIteration, AttemptId: execCtx.run.attempt,
		Runtime: runtimeState.Contexts[0], StartedAt: execCtx.startTime,
		State: cloneWire(execCtx.state), Extensions: cloneWire(execCtx.run.extensions),
	}
	if phase == PhaseIterationCompleted || phase == PhaseTerminal {
		state.CompletedIteration = state.Iteration
	}
	checkpoint := &Checkpoint{SchemaVersion: RunSchemaVersion, RunId: execCtx.run.id,
		Revision: execCtx.run.working.Revision, Phase: phase, LastOutcome: execCtx.lastOutcome,
		ResumePhase: execCtx.resumePhase, Contexts: []ContextCheckpoint{state}}
	if phase == PhaseTerminal || phase == PhasePaused {
		checkpoint.Outcome = &SavedOutcome{Value: execCtx.acceptedValue, Result: execCtx.result,
			CompletedAt: execCtx.endTime}
		if checkpoint.Outcome.Result.Status == ExecutionFinished {
			checkpoint.LastOutcome = checkpoint.Outcome
		}
	}
	return checkpoint, nil
}

func (execCtx *ExecutionContext) runtimeSnapshot() ContextRuntime {
	execCtx.mu.RLock()
	result := ContextRuntime{ContextId: execCtx.contextId, Iteration: execCtx.iteration,
		NextModelCall: execCtx.nextModelCallSeq, NextToolCall: execCtx.nextToolCallSeq}
	if execCtx.exceededLimit != nil {
		limit := *execCtx.exceededLimit
		result.ExceededLimit = &limit
	}
	execCtx.mu.RUnlock()
	result.Counters = execCtx.stats.Counters()
	result.TurnCounterBaselines = execCtx.stats.savedTurnBaselines()
	result.Gauges = execCtx.stats.Gauges()
	return result
}
