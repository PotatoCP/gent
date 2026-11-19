package gent

import (
	"errors"
	"fmt"
	"time"
)

func prepareTask(codecs *CodecRegistry, task *Task) (*Task, error) {
	if err := validateTask(task); err != nil {
		return nil, err
	}
	// Input freezing is required: callers can keep their task after NewTurn returns. The saved
	// instructions and history must not change when those caller-owned values change later.
	return cloneOwned(codecs, task)
}

func validateTask(task *Task) error {
	for i, message := range task.Instructions {
		if message == nil || message.Role != ChatMessageTypeSystem {
			return fmt.Errorf("Instructions[%d] must be a system message", i)
		}
		if err := ValidateContentParts(message.Parts); err != nil {
			return fmt.Errorf("Instructions[%d]: %w", i, err)
		}
	}
	userInput := false
	for i, message := range task.Messages {
		if message == nil {
			return fmt.Errorf("Messages[%d] is nil", i)
		}
		switch message.Role {
		case ChatMessageTypeHuman:
			userInput = true
		case ChatMessageTypeAI, ChatMessageTypeTool:
		default:
			return fmt.Errorf("Messages[%d] has invalid conversation role %q", i, message.Role)
		}
	}
	if !userInput {
		return errors.New("Task.Messages must contain at least one user message")
	}
	return validateTaskConversation(task.Messages)
}

func (execCtx *ExecutionContext) admitTurn(task *Task) error {
	if execCtx.turnOpen {
		execCtx.SetTermination(TerminationCanceled, nil, nil)
		execCtx.PublishAfterExecution(TerminationCanceled, nil)
		execCtx.lastOutcome = &SavedOutcome{Result: execCtx.result, CompletedAt: execCtx.endTime}
		// The canceled old turn and the new turn share one acceptance boundary. Keep the old
		// event's TurnId before changing identity, and hold live completion until Save succeeds.
		execCtx.run.mu.Lock()
		if event := execCtx.terminalEvent; event != nil {
			execCtx.terminalEvent = nil
			if err := execCtx.run.queueEvent(event); err != nil {
				execCtx.run.mu.Unlock()
				return execCtx.stopRecording(err)
			}
		}
		execCtx.run.mu.Unlock()
	}
	encoded, err := execCtx.Codecs().Encode(task)
	if err != nil {
		return err
	}
	execCtx.data.ResetTurn()
	execCtx.stats.resetTurn()
	execCtx.mu.Lock()
	execCtx.turnId, execCtx.turnOpen = "turn:"+randomIdentity(), true
	execCtx.savedTask = encoded
	if task.Instructions != nil {
		execCtx.instructions = task.Instructions
	}
	execCtx.endTime = time.Time{}
	execCtx.result, execCtx.acceptedValue, execCtx.refusal, execCtx.finalResult = nil, nil, nil, nil
	execCtx.finalContentSource = nil
	execCtx.terminationReason, execCtx.phase, execCtx.resumePhase = "", PhaseInitial, ""
	execCtx.mu.Unlock()
	// Clear only task continuation. Counter baselines and other run state must survive because
	// NewTurn keeps cumulative usage. A namespace prefix does not establish state lifetime.
	execCtx.run.mu.Lock()
	for _, name := range []string{"gent:react_frame", "gent:tool_plan", "gent:search_seen",
		"gent:program", "gent:program_calls", requiredHistoryState} {
		delete(execCtx.state, name)
	}
	execCtx.currentToolPlan, execCtx.boundToolPlans = nil, nil
	execCtx.run.mu.Unlock()
	execCtx.modelReplay = modelReplay{}
	if err := execCtx.appendTaskRecords(task.Messages); err != nil {
		return err
	}
	execCtx.PublishBeforeExecution()
	return execCtx.CommitCheckpoint(PhaseInitial)
}

// Instructions returns the accepted effective system prefix. This slice and all reachable
// values are read-only. A later NewTurn can replace the prefix; compaction cannot change it.
func (execCtx *ExecutionContext) Instructions() []*ModelMessage {
	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()
	return execCtx.instructions
}
