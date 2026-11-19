package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

func TestMemoryTransactionRetryAndFence(t *testing.T) {
	app, _ := testApplication(t, 0)
	state := executeAction(t, app, &actionRequest{Action: actionTurn, Prompt: "Find John Smith"})
	before, _ := app.store.read(state.RunId)
	var batch gent.SaveBatch
	assert.NoError(t, app.store.codecs.Unmarshal(before.batches[0], &batch))
	writer := app.store.claim(state.RunId)
	// An exact retry uses the original encoded bytes and does not replace a newer capture.
	assert.NoError(t, writer.commit(context.Background(), &batch, before.batches[0], nil))
	after, _ := app.store.read(state.RunId)
	assert.Equal(t, before, after)
	assert.EqualError(t, writer.commit(context.Background(), &batch, []byte("changed"), nil),
		"storage: retry bytes differ")
	app.store.claim(state.RunId)
	assert.EqualError(t, writer.commit(context.Background(), &batch, before.batches[0], nil),
		"storage: worker lease expired")
	unchanged, _ := app.store.read(state.RunId)
	assert.Equal(t, before, unchanged)
}

func TestSlowBrowserDisconnectAndAcceptedReplay(t *testing.T) {
	app, _ := testApplication(t, 0)
	first := executeAction(t, app, &actionRequest{Action: actionTurn, Prompt: "Find John Smith"})
	queue, unsubscribe := app.hub.subscribe(first.RunId)
	defer unsubscribe()
	// No consumer reads this bounded queue. Normal execution must still finish and save output.
	second := executeAction(t, app, &actionRequest{Action: actionTurn, RunId: first.RunId,
		Prompt: "Remember the first message and lookup."})
	assert.Equal(t, &attemptOutcome{Status: gent.ExecutionFinished}, second.Outcome)
	var delivery []uint64
	for data := range queue {
		var frame browserFrame
		assert.NoError(t, json.Unmarshal(data, &frame))
		if frame.Delivery == gent.SubscriberAccepted {
			delivery = append(delivery, frame.Event.EventSequenceNumber)
		}
	}
	assert.LessOrEqual(t, len(delivery), 64)
	// A reconnect replaces preview state from complete saved bytes at a fixed head. Every event
	// position, including those that were not rendered, is present exactly once and in order.
	reloaded, err := app.snapshot(first.RunId, replayBatches)
	assert.NoError(t, err)
	assert.Equal(t, second.Events, reloaded.Events)
	positions := make([]uint64, len(reloaded.Events))
	expected := make([]uint64, len(reloaded.Events))
	for i, event := range reloaded.Events {
		positions[i], expected[i] = event.EventSequenceNumber, uint64(i+1)
	}
	assert.Equal(t, expected, positions)
	assert.Equal(t, second.LastResult, reloaded.LastResult)
}
