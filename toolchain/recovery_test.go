package toolchain

import (
	"context"
	"errors"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRestoreKeepsErrorGaugesWithoutCountingAgain(t *testing.T) {
	worker, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	chain := New().RegisterTool(gent.DefineTool("act", func(context.Context, struct{}) (
		*gent.ToolExecutionResponse, error,
	) {
		calls++
		if calls == 4 {
			cancel()
			return nil, context.Canceled
		}
		if calls < 4 {
			return nil, errors.New("private operation error")
		}
		return &gent.ToolExecutionResponse{Status: gent.ToolBlocked,
			Instructions: "Request approval."}, nil
	}).Recovery(func(context.Context, *gent.ToolRecoveryRequest[struct{}]) (
		*gent.ToolRecoveryResponse, error,
	) {
		return nil, errors.New("private recovery error")
	}).MustBuild())
	var requests []*gent.ModelToolCallRequest
	for range 4 {
		requests = append(requests, nativeCall("act", "{}"))
	}
	var plan *gent.ToolPlan
	loop := planTestLoop(chain, requests, &plan)
	runner, execCtx, store := newToolTestRun(t, worker, loop, &gent.Config{ToolChain: chain})
	_, _, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Run four operations.")))
	assert.ErrorIs(t, err, context.Canceled)
	codecs := gent.NewCodecRegistry()
	freshRunner, fresh := restoreToolTest(t, loop, &gent.Config{Codecs: codecs, ToolChain: chain},
		store.runStateSnapshot)
	_, result, err := freshRunner.Continue(fresh)
	require.NoError(t, err)
	require.Equal(t, gent.ExecutionFinished, result.Status)
	assert.Equal(t, 4, calls)
	assert.Equal(t, int64(3), fresh.Stats().GetCounter(gent.SCToolCallsErrorTotal))
	assert.Equal(t, int64(1), fresh.Stats().GetCounter(gent.SCToolRecoveryErrorTotal))
	assert.Equal(t, float64(3), fresh.Stats().GetGauge(gent.SGToolCallsErrorConsecutive))
	assert.Equal(t, float64(3), fresh.Stats().GetGauge(gent.SGToolCallsErrorConsecutiveFor+"act"))
	// A new operation belongs to the next turn. It resets turn gauges and retains counters.
	var nextPlan *gent.ToolPlan
	nextLoop := planTestLoop(chain, []*gent.ModelToolCallRequest{nativeCall("act", "{}")}, &nextPlan)
	loop.next = nextLoop.next
	_, result, err = freshRunner.NewTurn(fresh, gent.NewTask(gent.TextPart("Try a new operation.")))
	require.NoError(t, err)
	assert.Equal(t, gent.ToolBlocked, nextPlan.Results[0].Execution().Status)
	assert.Equal(t, float64(0), fresh.Stats().GetGauge(gent.SGToolCallsErrorConsecutive))
	assert.Equal(t, float64(0), fresh.Stats().GetGauge(gent.SGToolCallsErrorConsecutiveFor+"act"))
	assert.Equal(t, int64(3), fresh.Stats().GetCounter(gent.SCToolCallsErrorTotal))
}

func TestDefaultRecoveryReportsSafeError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tool := gent.DefineTool("act", func(context.Context, struct{}) (
		*gent.ToolExecutionResponse, error,
	) {
		cancel()
		return nil, context.Canceled
	}).MustBuild()
	chain := New().RegisterTool(tool)
	var plan *gent.ToolPlan
	loop := planTestLoop(chain, []*gent.ModelToolCallRequest{nativeCall("act", "{}")}, &plan)
	runner, execCtx, store := newToolTestRun(t, ctx, loop, &gent.Config{ToolChain: chain})
	_, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Run the operation.")))
	assert.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, result)
	codecs := gent.NewCodecRegistry()
	freshRunner, fresh := restoreToolTest(t, loop, &gent.Config{Codecs: codecs, ToolChain: chain},
		store.runStateSnapshot)
	_, result, err = freshRunner.Continue(fresh)
	require.NoError(t, err)
	require.Equal(t, gent.ExecutionFinished, result.Status)
	record := plan.Results[0]
	assert.Equal(t, gent.ToolResponseRecoveryError, record.Kind())
	assert.Equal(t, gent.DefaultSteeringMessages().RecoveryError, recordText(record))
	diagnostic, err := codecs.Decode(*record.Error())
	assert.NoError(t, err)
	assert.EqualError(t, diagnostic.(error), `tool "act" has no recovery handler`)
}
