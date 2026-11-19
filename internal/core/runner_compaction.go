package gent

import (
	"fmt"
	"time"
)

func (runner *Runner[T]) compact(execCtx *ExecutionContext) error {
	trigger, strategy := execCtx.config.CompactionTrigger, execCtx.config.CompactionStrategy
	if trigger == nil {
		return nil
	}
	compact, err := trigger.ShouldCompact(execCtx)
	if err != nil || !compact {
		return err
	}
	input, err := execCtx.CompactionInput()
	if err != nil {
		return err
	}
	started := time.Now()
	execCtx.run.mu.RLock()
	firstEvent := execCtx.run.nextEventSequenceNumber
	execCtx.run.mu.RUnlock()
	proposal, err := strategy.Compact(execCtx, input)
	if err != nil {
		return fmt.Errorf("gent: compaction strategy %T for run %q turn %q: %w",
			strategy, execCtx.RunId(), execCtx.TurnId(), err)
	}
	if proposal == nil {
		return fmt.Errorf("gent: compaction strategy %T for run %q turn %q returned no proposal",
			strategy, execCtx.RunId(), execCtx.TurnId())
	}
	if proposal.ExpectedDraftRevision != input.Revision {
		return ErrHistoryConflict
	}
	decision := proposal.Compaction
	if decision == nil {
		decision = &CompactionRecord{}
	}
	decision.Strategy, decision.InputIds, decision.Active = fmt.Sprintf("%T", strategy),
		input.EntryIds(), proposal.Active
	seen := map[string]bool{}
	execCtx.run.mu.RLock()
	for _, event := range execCtx.run.pending {
		if event.EventSequenceNumber > firstEvent && event.ModelCallId != "" &&
			!seen[event.ModelCallId] {
			decision.ModelCallIds = append(decision.ModelCallIds, event.ModelCallId)
			seen[event.ModelCallId] = true
		}
	}
	execCtx.run.mu.RUnlock()
	marker, err := execCtx.NewHistoryEntry(
		&HistoryEntry{
			Kind:       HistoryCompaction,
			Compaction: decision,
		},
	)
	if err != nil {
		return err
	}
	proposal.AppendedEntries = append(proposal.AppendedEntries, marker)
	proposal.Reason = ContextCompact
	if err := execCtx.UpdateHistory(*proposal); err != nil {
		return err
	}
	execCtx.PublishCompaction(len(input.EntryIds()), len(proposal.Active), time.Since(started))
	if err := trigger.NotifyCompacted(execCtx); err != nil {
		return err
	}
	return execCtx.CommitCheckpoint(PhaseCompactionCompleted)
}
