package trace

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStitchEventSequenceNumbers(t *testing.T) {
	type input struct {
		last   uint64
		events []*Event
	}
	type expected struct {
		result StitchResult
		wire   string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"empty buffer", input{last: 4}, expected{
			StitchResult{Status: StitchStatusOK, SnapshotLastEventSequenceNumber: 4},
			`{
  "status": "ok",
  "snapshotLastEventSequenceNumber": 4
}`}},
		{"contiguous", input{4, []*Event{{EventSequenceNumber: 5}, {EventSequenceNumber: 6}}},
			expected{StitchResult{Status: StitchStatusOK, SnapshotLastEventSequenceNumber: 4,
				FirstBufferedEventSequenceNumber: 5}, `{
  "status": "ok",
  "snapshotLastEventSequenceNumber": 4,
  "firstBufferedEventSequenceNumber": 5
}`}},
		{"gap after saved head", input{4, []*Event{{EventSequenceNumber: 6}}},
			expected{StitchResult{Status: StitchStatusGap, SnapshotLastEventSequenceNumber: 4,
				FirstBufferedEventSequenceNumber: 6, MissingAfterEventSequenceNumber: 4,
				MissingBeforeEventSequenceNumber: 6}, `{
  "status": "gap",
  "snapshotLastEventSequenceNumber": 4,
  "firstBufferedEventSequenceNumber": 6,
  "missingAfterEventSequenceNumber": 4,
  "missingBeforeEventSequenceNumber": 6
}`}},
		{"gap inside buffer", input{4, []*Event{{EventSequenceNumber: 5}, {EventSequenceNumber: 7}}},
			expected{StitchResult{Status: StitchStatusGap, SnapshotLastEventSequenceNumber: 4,
				FirstBufferedEventSequenceNumber: 5, MissingAfterEventSequenceNumber: 5,
				MissingBeforeEventSequenceNumber: 7}, `{
  "status": "gap",
  "snapshotLastEventSequenceNumber": 4,
  "firstBufferedEventSequenceNumber": 5,
  "missingAfterEventSequenceNumber": 5,
  "missingBeforeEventSequenceNumber": 7
}`}},
		{"unsorted", input{4, []*Event{{EventSequenceNumber: 6}, {EventSequenceNumber: 5}}},
			expected{StitchResult{Status: StitchStatusUnsorted, SnapshotLastEventSequenceNumber: 4,
				FirstBufferedEventSequenceNumber: 6}, `{
  "status": "unsorted",
  "snapshotLastEventSequenceNumber": 4,
  "firstBufferedEventSequenceNumber": 6
}`}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			result := ValidateStitch(test.input.last, test.input.events)
			assert.Equal(t, test.expected.result, result)
			encoded, err := json.MarshalIndent(result, "", "  ")
			assert.NoError(t, err)
			assert.Equal(t, test.expected.wire, string(encoded))
		})
	}
}
