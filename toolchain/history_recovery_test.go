package toolchain

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type checkpointInput struct {
	ID string `json:"id"`
}

func TestRecoveryObservesUnfinishedBatchWithoutRetry(t *testing.T) {
	type input struct {
		status  gent.ToolRecoveryStatus
		failure bool
		missing bool
	}
	type expected struct {
		kind       gent.ToolResponseKind
		status     gent.ToolRecoveryStatus
		recoveries int
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"unresolved", input{status: gent.ToolRecoveryUnresolved}, expected{
			gent.ToolResponseRecovery, gent.ToolRecoveryUnresolved, 1}},
		{"not started", input{status: gent.ToolRecoveryNotStarted}, expected{
			gent.ToolResponseRecovery, gent.ToolRecoveryNotStarted, 1}},
		{"completed", input{status: gent.ToolRecoveryCompleted}, expected{
			gent.ToolResponseRecovery, gent.ToolRecoveryCompleted, 1}},
		{"failed check", input{failure: true}, expected{gent.ToolResponseRecoveryError, "", 1}},
		{"missing handler", input{missing: true}, expected{gent.ToolResponseRecoveryError, "", 0}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			chain := New().RegisterTool(gent.DefineTool("book", func(_ context.Context,
				input checkpointInput) (
				*gent.ToolExecutionResponse, error,
			) {
				calls++
				if input.ID == "4" {
					cancel()
					return nil, context.Canceled
				}
				return capturedToolResponse("booking "+input.ID, nil)
			}).MustBuild())
			var requests []*gent.ModelToolCallRequest
			for i := 1; i <= 5; i++ {
				request := nativeCall("book", fmt.Sprintf(`{"id":"%d"}`, i))
				request.ID = fmt.Sprintf("provider-%d", i)
				requests = append(requests, request)
			}
			var plan *gent.ToolPlan
			loop := planTestLoop(chain, requests, &plan)
			runner, ctx, store := newToolTestRun(t, base, loop, &gent.Config{ToolChain: chain})
			_, result, err := runner.NewTurn(ctx, gent.NewTask(gent.TextPart("Book five items.")))
			assert.ErrorIs(t, err, context.Canceled)
			assert.Nil(t, result)
			assert.Equal(t, 4, calls)
			saved := store.runStateSnapshot
			freshChain := New()
			recoveries := 0
			if !test.input.missing {
				freshChain.RegisterTool(gent.DefineTool("book", func(context.Context, checkpointInput) (
					*gent.ToolExecutionResponse, error,
				) {
					calls++
					return nil, errors.New("must not run")
				}).Recovery(
					func(_ context.Context, request *gent.ToolRecoveryRequest[checkpointInput]) (
						*gent.ToolRecoveryResponse, error,
					) {
						recoveries++
						assert.Equal(t, checkpointInput{ID: "4"}, request.Input())
						assert.Equal(t, plan.Calls[3].Id, request.Identity().ToolCallId)
						if test.input.failure {
							return nil, errors.New("private recovery backend failure")
						}
						report := &gent.ToolRecoveryResponse{Status: test.input.status,
							Parts:        []gent.ContentPart{gent.TextPart("Checked external receipt.")},
							Instructions: "Review availability now."}
						if test.input.status == gent.ToolRecoveryCompleted {
							report.Execution = &gent.ToolExecutionResponse{Status: gent.ToolExecuted,
								Parts:        []gent.ContentPart{gent.TextPart("original booking 4")},
								Instructions: "Original guidance."}
						}
						return report, nil
					}).MustBuild())
			}
			codecs := gent.NewCodecRegistry()
			assert.NoError(t, freshChain.RegisterCodecs(codecs))
			var resumed *gent.ToolPlan
			freshLoop := planTestLoop(freshChain, nil, &resumed)
			freshStore := &toolTestRecorder{codecs: codecs, capture: true}
			freshRunner, fresh := restoreToolTest(t, freshLoop, &gent.Config{Codecs: codecs,
				ToolChain: freshChain, Recorder: freshStore}, saved)
			accepted, found, err := fresh.SavedToolPlan()
			require.NoError(t, err)
			require.True(t, found)
			original := append([]*gent.ToolCallRecord(nil), accepted.Results...)
			_, result, err = freshRunner.Continue(fresh)
			require.NoError(t, err)
			require.Equal(t, gent.ExecutionFinished, result.Status)
			assert.Equal(t, 4, calls, "recovery never calls the original tool")
			fourth := resumed.Results[3]
			status, _ := fourth.Response().RecoveryStatus()
			assert.Equal(t, test.expected, expected{fourth.Kind(), status, recoveries})
			assert.Equal(t, original, resumed.Results[:3])
			assert.Equal(t, gent.ToolRecoveryNotStarted, resumed.Results[4].Recovery().Status)
			assert.Equal(t, int64(4), fresh.Stats().GetCounter(gent.SCToolCalls))
			expectedErrors := int64(0)
			if test.expected.kind == gent.ToolResponseRecoveryError {
				expectedErrors = 1
			}
			assert.Equal(t, expectedErrors, fresh.Stats().GetCounter(gent.SCToolRecoveryErrorTotal))
			archive := gent.NewRunView(ctx.RunId())
			for _, batches := range [][][]byte{store.batches, freshStore.batches} {
				for _, data := range batches {
					var batch gent.SaveBatch
					require.NoError(t, codecs.Unmarshal(data, &batch))
					applied, err := archive.Apply(&batch, codecs)
					require.NoError(t, err)
					require.Equal(t, gent.ApplyApplied, applied.Status)
				}
			}
			assert.NoError(t, gent.ValidateBundle(*archive, codecs))
		})
	}
}

func TestCompletedToolRecordsNeedNoOldBindings(t *testing.T) {
	chain := New().RegisterTool(gent.DefineTool("old", func(_ context.Context, input checkpointInput) (
		*gent.ToolExecutionResponse, error,
	) {
		parts, err := gent.NewParts().Data(input).PDF([]byte("%PDF-original")).Build()
		return &gent.ToolExecutionResponse{Status: gent.ToolExecuted, Parts: parts,
			Instructions: "Retain the original evidence."}, err
	}).MustBuild())
	var plan *gent.ToolPlan
	loop := planTestLoop(
		chain,
		[]*gent.ModelToolCallRequest{
			nativeCall(
				"old",
				`{"id":"saved"}`,
			),
		},
		&plan,
	)
	runner, execCtx, store := newToolTestRun(
		t,
		context.Background(),
		loop,
		&gent.Config{
			ToolChain: chain,
		},
	)
	_, completed, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read the old record.")))
	require.NoError(t, err)
	// A fresh registry has no registration for checkpointInput or the removed tool.
	codecs := gent.NewCodecRegistry()
	freshRunner, fresh := restoreToolTest(t, &toolTestLoop{}, &gent.Config{Codecs: codecs},
		store.runStateSnapshot)
	saved, found, err := fresh.SavedToolPlan()
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, plan.Results, saved.Results)
	value, result, err := freshRunner.GetLastTurnResult(fresh)
	assert.NoError(t, err)
	assert.Equal(t, "Done", value)
	assert.Equal(t, completed, result)
}
