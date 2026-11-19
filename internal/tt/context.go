package tt

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/output"
)

// NewContext creates an idle context for a component unit test. Execution tests must use the
// Runner's NewTurn or Continue path. Cleanup joins resources even when an assertion fails.
func NewContext(t testing.TB, config *gent.Config) *gent.ExecutionContext {
	t.Helper()
	runner := executor.NewRunner(&componentLoop{}, config)
	execCtx, err := runner.NewExecutionContext(context.Background(), t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(execCtx.Release)
	return execCtx
}

type componentLoop struct{}

func (*componentLoop) Output() gent.Output[string] { return output.NewTextOutput() }
func (*componentLoop) Next(*gent.ExecutionContext) (*gent.AgentLoopResult, error) {
	panic("component unit tests do not execute this loop")
}
