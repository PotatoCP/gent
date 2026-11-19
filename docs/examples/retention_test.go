package examples

import (
	"testing"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

func TestRetentionProposal(t *testing.T) {
	type input struct {
		messages []*gent.UserMessageRetentionEntry
		bytes    int
		omitted  bool
	}
	type expected struct {
		result    *gent.UserMessageRetentionResult
		errorText string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	message := &gent.UserMessageRetentionEntry{EntryId: "original",
		Message: gent.TextParts(gent.ChatMessageTypeHuman, "Keep the original task.")}
	cases := []testCase{
		{"empty", input{}, expected{&gent.UserMessageRetentionResult{}, ""}},
		{"omit user input", input{[]*gent.UserMessageRetentionEntry{message}, 30, false},
			expected{&gent.UserMessageRetentionResult{
				EarlierContentOmitted: true, RetainedBytes: 1}, ""}},
		{"keep prior omission", input{nil, 1, true},
			expected{&gent.UserMessageRetentionResult{
				EarlierContentOmitted: true, RetainedBytes: 1}, ""}},
		{"notice cannot fit", input{[]*gent.UserMessageRetentionEntry{message}, 0, false},
			expected{nil, "omission notice needs 1 bytes; allowance is 0"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := &gent.UserMessageRetentionInput{Messages: test.input.messages,
				MaxBytes: test.input.bytes, Marker: "!", EarlierContentOmitted: test.input.omitted}
			codecs := gent.NewCodecRegistry()
			before, err := codecs.Marshal(request)
			assert.NoError(t, err)
			result, err := (&NoUserRetention{}).Retain(request)
			if test.expected.errorText == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, test.expected.errorText)
			}
			assert.Equal(t, test.expected.result, result)
			after, err := codecs.Marshal(request)
			assert.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}
