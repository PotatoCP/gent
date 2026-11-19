package toolchain

import (
	"errors"
	"fmt"
	"sync"

	"github.com/rickchristie/gent"
	core "github.com/rickchristie/gent/internal/core"
)

// SupportsParallelCalls permits concurrent execution of independent plans.
func (c *Registry) SupportsParallelCalls() bool { return true }

// SupportsParallelCalls permits concurrent execution with separate search state for each plan.
func (c *SearchChain) SupportsParallelCalls() bool { return true }

func executeParallelToolPlans(execCtx *gent.ExecutionContext, chain gent.ToolChain,
	plans []*gent.ToolPlan, format gent.TextFormat,
) ([]*gent.ToolCallRecord, error) {
	// Each worker owns its plan. The VM and result collector remain on the caller goroutine.
	// Wait on every exit path so tools cannot change the run after the program has returned.
	var workers sync.WaitGroup
	var firstPanic sync.Once
	var panicValue any
	errorsByPlan := make([]error, len(plans))
	for i, plan := range plans {
		workers.Add(1)
		go func() {
			defer workers.Done()
			// Stop context-aware siblings before waiting. A panic during their cleanup must not
			// replace the first panic. Keep the accepted checkpoint available for recovery.
			defer func() {
				if value := recover(); value != nil {
					firstPanic.Do(func() {
						panicValue = value
						execCtx.Stop()
					})
				}
			}()
			for plan.NextCall < len(plan.Calls) {
				call := plan.Calls[plan.NextCall]
				if _, err := chain.ExecuteNext(execCtx, plan, format); err != nil {
					// A returned operation error ends the attempt. Ordinary Tool.Call failures
					// are handled records and do not take this path. Cancel before joining so
					// a cooperative peer cannot keep the failed operation alive.
					err = fmt.Errorf("gent: parallel ToolChain.ExecuteNext for tool %q call %q: %w",
						call.Name, call.Id, err)
					errorsByPlan[i] = core.FailAttempt(execCtx, err)
					return
				}
			}
		}()
	}
	workers.Wait()
	if panicValue != nil {
		panic(panicValue)
	}
	if err := errors.Join(errorsByPlan...); err != nil {
		return nil, err
	}
	result := make([]*gent.ToolCallRecord, 0, len(plans))
	for _, plan := range plans {
		result = append(result, plan.Results...)
	}
	return result, nil
}

var _ gent.ParallelToolChain = (*Registry)(nil)
var _ gent.ParallelToolChain = (*SearchChain)(nil)
