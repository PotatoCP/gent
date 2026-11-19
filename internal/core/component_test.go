package gent

import (
	"context"
	"testing"
)

// newComponentContext creates an idle context for tests of a single service. Tests of admission,
// saves, subscriptions, or execution use NewTurn so they exercise the same lifecycle as callers.
func newComponentContext(t testing.TB, config *Config) *ExecutionContext {
	t.Helper()
	runner := NewRunner[string](&turnTestLoop{}, config)
	execCtx, err := runner.NewExecutionContext(context.Background(), t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(execCtx.Release)
	return execCtx
}
