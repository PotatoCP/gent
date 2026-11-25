package gent_test

import (
	"math"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

func TestSaveBatchShouldCaptureRunStateSnapshot(t *testing.T) {
	type input struct {
		batch *gent.SaveBatch
		every uint64
	}
	type expected struct{ capture bool }
	type testCase struct {
		name     string
		input    input
		expected expected
	}

	// A batch advances from its previous accepted position through all its consecutive events.
	eventBatch := func(previous uint64, count int) *gent.SaveBatch {
		batch := &gent.SaveBatch{PreviousEventSequenceNumber: previous}
		for i := range count {
			batch.Events = append(batch.Events, &gent.RecordedEvent{
				EventSequenceNumber: previous + uint64(i) + 1,
			})
		}
		return batch
	}
	completed := eventBatch(95, 3)
	completed.Checkpoint = &gent.Checkpoint{Phase: gent.PhaseCompactionCompleted}
	eventOnly := eventBatch(95, 3)
	eventOnly.Events[1].Payload = &gent.CompactionEvent{}
	common := eventBatch(95, 3)
	common.Events[1].EventName = gent.EventNameCompaction
	common.Events[1].Payload = &gent.CommonEvent{Description: "compaction"}

	for _, test := range []testCase{
		{"zero interval selects completed phase", input{completed, 0}, expected{true}},
		{"typed event without checkpoint", input{eventOnly, 100}, expected{true}},
		{"common event with same name", input{common, 0}, expected{false}},
		{"zero disables interval", input{eventBatch(95, 8), 0}, expected{false}},
		{"before first boundary", input{eventBatch(0, 9), 10}, expected{false}},
		{"ends on boundary", input{eventBatch(95, 5), 100}, expected{true}},
		{"crosses boundary", input{eventBatch(95, 8), 100}, expected{true}},
		{"previous batch reached boundary", input{eventBatch(100, 3), 100}, expected{false}},
		{"crosses several boundaries", input{eventBatch(8, 23), 10}, expected{true}},
		{"every event", input{eventBatch(0, 1), 1}, expected{true}},
		{"empty batch", input{&gent.SaveBatch{}, 1}, expected{false}},
		{"last possible boundary", input{eventBatch(math.MaxUint64-2, 2), math.MaxUint64},
			expected{true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			capture := test.input.batch.ShouldCaptureRunStateSnapshot(test.input.every)
			assert.Equal(t, test.expected, expected{capture})
		})
	}
}
