package gent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCompactionConfigurationRequiresBothComponents(t *testing.T) {
	type input struct {
		trigger  CompactionTrigger
		strategy CompactionStrategy
	}
	type expected struct{ errorText string }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	failure := "gent: prepare execution configuration: gent: configure both " +
		"CompactionTrigger and CompactionStrategy"
	cases := []testCase{
		{"disabled", input{}, expected{}},
		{"enabled", input{&stubTrigger{}, &stubStrategy{}}, expected{}},
		{"missing trigger", input{strategy: &stubStrategy{}}, expected{failure}},
		{"missing strategy", input{trigger: &stubTrigger{}}, expected{failure}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			runner := NewRunner[string](&turnTestLoop{}, &Config{
				CompactionTrigger: test.input.trigger, CompactionStrategy: test.input.strategy,
			})
			execCtx, err := runner.NewExecutionContext(context.Background(), "test")
			if test.expected.errorText != "" {
				assert.Nil(t, execCtx)
				assert.EqualError(t, err, test.expected.errorText)
				return
			}
			if assert.NoError(t, err) {
				execCtx.Release()
			}
		})
	}
}

type stubTrigger struct{}

func (*stubTrigger) ShouldCompact(*ExecutionContext) (bool, error) { return false, nil }
func (*stubTrigger) NotifyCompacted(*ExecutionContext) error       { return nil }

type stubStrategy struct{}

func (*stubStrategy) Compact(_ *ExecutionContext, input *CompactionInput) (*HistoryUpdate, error) {
	return &HistoryUpdate{ExpectedDraftRevision: input.Revision, Active: input.EntryIds()}, nil
}
