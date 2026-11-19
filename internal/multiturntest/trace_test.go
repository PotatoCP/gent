package multiturntest

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/internal/tt"
	"github.com/rickchristie/gent/trace"
	"github.com/stretchr/testify/assert"
)

func TestTraceSubscriberRetainsTurnAndContentAssociation(t *testing.T) {
	type input struct{ delivery gent.SubscriberDelivery }
	type expected struct{ reason gent.SubscriberStopReason }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"accepted", input{gent.SubscriberAccepted}, expected{gent.SubscriberClosed}},
		{"live", input{gent.SubscriberLive}, expected{gent.SubscriberClosed}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := gent.NewCodecRegistry()
			traceConfig := trace.Config{Codecs: codecs, IncludeChunkText: true, IncludeRunOutput: true}
			seq, err := trace.NewSequencer("", traceConfig)
			if !assert.NoError(t, err) {
				return
			}
			defer seq.Close()
			recorder := &memoryRecorder{codecs: codecs}
			model := tt.NewMockModel().AddResponse("First.", 10, 2).AddResponse("Second.", 15, 3)
			runner := executor.NewRunner(react.NewWithOutput(model, &transformedOutput{}), &gent.Config{
				Codecs: codecs, Recorder: recorder, Subscribers: []*gent.SubscriberRegistration{{
					Subscriber: seq, Delivery: test.input.delivery}}})
			execCtx, err := runner.NewExecutionContext(context.Background(), "trace conversation")
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			var turns []string
			for _, text := range []string{"First task.", "Next task."} {
				_, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart(text)))
				if !assert.NoError(t, err) {
					return
				}
				turns = append(turns, result.TurnId)
				stop := seq.LastDeliveryStop()
				assert.Equal(t, test.expected.reason, stop.Reason)
				assert.NoError(t, stop.Error)
				assert.Equal(t, stop.LastAcceptedEventSequenceNumber, stop.LastDeliveredEventSequenceNumber)
			}
			saved := gent.NewRunView(execCtx.RunId())
			for _, data := range recorder.batches {
				var batch gent.SaveBatch
				assert.NoError(t, codecs.Unmarshal(data, &batch))
				applied, err := saved.Apply(&batch, codecs)
				assert.NoError(t, err)
				assert.Equal(t, &gent.ApplyStateResult{Status: gent.ApplyApplied}, applied)
			}
			replayed, err := trace.RestoreSequencer(*saved, traceConfig)
			if !assert.NoError(t, err) {
				return
			}
			defer replayed.Close()
			snapshot := seq.Snapshot()
			assert.Equal(t, snapshot, replayed.Snapshot())
			assert.Equal(t, execCtx.RunId(), snapshot.RunId)
			assert.Equal(t, turns[1], snapshot.TurnId)
			if !assert.Len(t, snapshot.ModelCalls, 2) {
				return
			}
			for index, call := range snapshot.ModelCalls {
				assert.Equal(t, turns[index], call.TurnId)
				text := []string{"First.", "Second."}[index]
				assert.Equal(t, text, gent.ContentText(call.Stream.Content))
			}
			assert.Equal(t, "Processed: Second.", gent.ContentText(snapshot.Contexts[0].Content))
		})
	}
}
