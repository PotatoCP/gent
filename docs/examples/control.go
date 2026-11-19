package examples

import (
	"context"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/executor"
)

func stopAttempt(execCtx *gent.ExecutionContext) {
	// docs:start control-stop
	execCtx.Stop()
	// Stop requests a pause. Wait for the running NewTurn or Continue call to return.
	// An idle Stop does nothing and cannot stop a later attempt.
	// docs:end control-stop
}

func deadlineContext(parent context.Context) (context.Context, context.CancelFunc) {
	// docs:start control-cancel
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	// Pass ctx to NewExecutionContext or Restore. Its cancellation interrupts the worker.
	// Restore accepted state before another attempt after interruption.
	// docs:end control-cancel
	return ctx, cancel
}

func runStatus(result *gent.ExecutionResult) string {
	// docs:start control-status
	switch result.Status {
	case gent.ExecutionFinished:
		return "The turn finished; inspect TerminationReason."
	case gent.ExecutionPaused:
		return "The task remains unfinished."
	case gent.ExecutionBusy:
		return "Another attempt already owns this context."
	case gent.ExecutionNoActiveTurn:
		return "There is no unfinished turn to continue."
	default:
		return "Unknown status; check the Gent version."
	}
	// docs:end control-status
}

func independentConversations(ctx context.Context, runner *executor.Runner[string]) error {
	// docs:start ownership-contexts
	first, err := runner.NewExecutionContext(ctx, "first")
	if err != nil {
		return err
	}
	defer first.Release()
	second, err := runner.NewExecutionContext(ctx, "second")
	if err != nil {
		return err
	}
	defer second.Release()
	// Shared services must support concurrent calls. Run progress belongs to each context.
	// docs:end ownership-contexts
	return nil
}
