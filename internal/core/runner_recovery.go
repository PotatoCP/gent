package gent

import (
	"fmt"
	"strings"
)

// BindManagedRecovery is internal to Gent. The reference loop supplies its native message and
// frame preparation without adding a public continuation method to the custom Loop interface.
func BindManagedRecovery(
	execCtx *ExecutionContext, prepare func(*ExecutionContext) (bool, error),
) {
	execCtx.prepareRecovery = prepare
}

func (runner *Runner[T]) prepareRecovery(execCtx *ExecutionContext) error {
	if execCtx.Phase() == PhaseRecoveryCompleted {
		execCtx.setPhase(execCtx.resumePhase)
		return nil
	}
	plan, found, err := execCtx.SavedToolPlan()
	if err != nil {
		return err
	}
	if found && plan.NextCall < len(plan.Calls) {
		// Recovery still belongs to the old task and must use its saved allowance. NewTurn
		// can reset turn limits only after required recovery finishes. With no pending work,
		// it can admit new input even when the previous turn used its entire allowance.
		execCtx.checkLimits()
		if execCtx.iteration <= execCtx.completedIteration {
			execCtx.EnforceIterationLimit()
		}
		if err := execCtx.Context().Err(); err != nil {
			return err
		}
	}
	prepare := execCtx.prepareRecovery
	if prepare == nil {
		prepare = prepareManagedPlan
	}
	resume := execCtx.Phase()
	changed, err := prepare(execCtx)
	if err != nil {
		return fmt.Errorf("gent: recovery preparation for run %q turn %q: %w",
			execCtx.RunId(), execCtx.TurnId(), err)
	}
	if !changed {
		return nil
	}
	execCtx.resumePhase = resume
	if err := execCtx.CommitCheckpoint(PhaseRecoveryCompleted); err != nil {
		return err
	}
	execCtx.setPhase(resume)
	return nil
}

func prepareManagedPlan(execCtx *ExecutionContext) (bool, error) {
	plan, found, err := execCtx.SavedToolPlan()
	if err != nil || !found {
		return false, err
	}
	if plan.AttemptId == execCtx.AttemptId() {
		return false, nil
	}
	chain := execCtx.config.ToolChain
	changed := plan.NextCall < len(plan.Calls)
	if plan.NextCall < len(plan.Calls) && chain == nil {
		return false, fmt.Errorf("gent: saved tool plan needs Config.ToolChain")
	}
	for plan.NextCall < len(plan.Calls) {
		// A previous attempt's plan takes the recovery path, never the business-call path.
		if _, err := chain.ExecuteNext(execCtx, plan, recoveryTextFormat{}); err != nil {
			return false, err
		}
	}
	return changed, nil
}

// Recovery itself does not render a new business action. Custom chains that render report
// sections receive a plain format; the reference loop binds its configured format instead.
type recoveryTextFormat struct{}

func (recoveryTextFormat) FormatSections(sections []FormattedSection) string {
	var text strings.Builder
	for _, section := range sections {
		if section.Name != "" {
			text.WriteString(section.Name + ":\n")
		}
		text.WriteString(section.Content)
		text.WriteByte('\n')
		text.WriteString((recoveryTextFormat{}).FormatSections(section.Children))
	}
	return text.String()
}
