package gent

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

type depthStateRecorder struct {
	*runStateRecorder
	attemptedBatchId string
}

func (recorder *depthStateRecorder) Save(ctx context.Context, batch *SaveBatch,
	capture func() (*RunState, error),
) error {
	recorder.attemptedBatchId = batch.BatchId
	return recorder.runStateRecorder.Save(ctx, batch, capture)
}

func TestCheckpointDocumentDepthRejectionKeepsAcceptedState(t *testing.T) {
	codecs := depthCodecs(t, CodecCompressionSnappy)
	recorder := &depthStateRecorder{runStateRecorder: &runStateRecorder{codecs: codecs, capture: true}}
	var nextValue any = "accepted buffer state"
	loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
		if err := execCtx.SetCheckpointState("buffers", nextValue); err != nil {
			return nil, err
		}
		execCtx.Stop()
		return &AgentLoopResult{Action: LAContinue}, nil
	}}
	runner := NewRunner[string](loop, &Config{Codecs: codecs, Recorder: recorder})
	execCtx, err := runner.NewExecutionContext(context.Background(), t.Name())
	if !assert.NoError(t, err) {
		return
	}
	t.Cleanup(execCtx.Release)
	_, result, err := runner.NewTurn(execCtx, NewTask(TextPart("Keep the accepted buffer.")))
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, &ExecutionResult{Status: ExecutionPaused, TurnId: execCtx.TurnId(),
		TerminationReason: TerminationStopped}, result)
	acceptedBatches := recorder.batches
	acceptedState := encodeRunState(t, codecs, recorder.states[len(recorder.states)-1])
	// This component fits the logical value limit on its own. Its named map envelopes plus
	// the surrounding SaveBatch exceed the separate document-depth bound. No save may accept it.
	nextValue = mapAtDepth(100)
	value, result, err := runner.Continue(execCtx)
	assert.EqualError(t, err, fmt.Sprintf(
		"gent: save batch %s: gent: saved document nesting exceeds 512", recorder.attemptedBatchId))
	assert.Equal(t, "", value)
	assert.Nil(t, result)
	assert.Equal(t, acceptedBatches, recorder.batches)
	assert.Equal(t, acceptedState,
		encodeRunState(t, codecs, recorder.states[len(recorder.states)-1]))
	execCtx.Release()
	for _, path := range []string{"full batches", "RunStateSnapshot plus tail",
		"latest RunStateSnapshot"} {
		t.Run(path, func(t *testing.T) {
			verifyCheckpointTypeRecovery(t, recorder.runStateRecorder, execCtx.RunId(),
				execCtx.TurnId(), path, "accepted buffer state")
		})
	}
}
