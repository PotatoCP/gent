// Package executor runs agent loops with one recoverable conversation per ExecutionContext.
package executor

import (
	"context"

	"github.com/rickchristie/gent"
	core "github.com/rickchristie/gent/internal/core"
)

// Runner binds a Loop and shared execution configuration. It owns admission, recovery, limits,
// savepoints, and attempt cleanup. Configure the Loop and Config before use; their services must
// support concurrent use when one Runner serves separate conversations.
type Runner[T any] struct {
	worker *core.Runner[T]
}

// NewRunner binds the Loop and read-only configuration. NewExecutionContext or Restore validates
// and resolves configuration before execution. Zero Config fields select documented defaults.
func NewRunner[T any](loop gent.Loop[T], config *gent.Config) *Runner[T] {
	return &Runner[T]{worker: core.NewRunner(loop, config)}
}

// NewExecutionContext creates a conversation and its task-independent LoopData. It does not admit
// a task or call the model. NewTurn supplies the first task. Release the context when its owner no
// longer needs it. The owner's cancellation ends the attempt; restore accepted state to continue.
func (runner *Runner[T]) NewExecutionContext(
	ctx context.Context, name string,
) (*gent.ExecutionContext, error) {
	return runner.worker.NewExecutionContext(ctx, name)
}

// Restore prepares a conversation from accepted RunState without executing the Loop, recovering
// tools, resetting turn fields, or processing completed output. Ready transfers state ownership
// to the returned context. Other results and failures leave ownership with the caller.
//
// Correctly stored complete SaveBatch values restore the same logical state through full replay
// or a RunStateSnapshot plus its accepted tail. This includes custom data, continuation, counters,
// history, output, and recovery evidence. Continue resumes an unfinished turn; NewTurn accepts new
// input. Future model output can differ, and unfinished external work uses managed recovery.
func (runner *Runner[T]) Restore(
	ctx context.Context, state *gent.RunState,
) (*gent.RestoreResult, error) {
	return runner.worker.Restore(ctx, state)
}

// NewTurn accepts and executes the first or a later task in this conversation. It saves the input,
// effective instructions, and declared turn reset before dependent work. It keeps run counters,
// custom run data, active history, and the last completed result. Busy does not admit a task.
// An idle unfinished turn is recovered as needed, then canceled in the same save as new admission.
//
// Nil Task.Instructions inherits the saved prefix. A non-nil slice replaces it, including an empty
// slice that clears it. Replacement can reduce provider cache reuse; no cache hit is guaranteed.
// Unexpected failures return zero T, nil result, and an error. Fix the cause, restore the accepted
// state, and Continue an admitted task instead of submitting that task again.
func (runner *Runner[T]) NewTurn(
	execCtx *gent.ExecutionContext, task *gent.Task,
) (T, *gent.ExecutionResult, error) {
	return runner.worker.NewTurn(execCtx, task)
}

// Continue executes the unfinished accepted turn with its saved input, turn counters, and current
// usable configuration. Required recovery runs before Loop.Next. NoActiveTurn means no unfinished
// turn exists. A clean Stop or limit pause keeps the handle reusable. An unexpected failure needs
// restoration. Live/accepted subscribers and mutable Hooks follow the same path as NewTurn.
func (runner *Runner[T]) Continue(
	execCtx *gent.ExecutionContext,
) (
	T,
	*gent.ExecutionResult,
	error,
) {
	return runner.worker.Continue(execCtx)
}

// GetLastTurnResult reads the last accepted completed result without executing or publishing it.
// A nil result means no completed turn exists. The result and all reachable values are read-only.
// Current unfinished work can coexist with this prior completed result.
func (runner *Runner[T]) GetLastTurnResult(
	execCtx *gent.ExecutionContext,
) (T, *gent.ExecutionResult, error) {
	return runner.worker.GetLastTurnResult(execCtx)
}
