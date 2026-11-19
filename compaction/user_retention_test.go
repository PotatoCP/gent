package compaction

import (
	"testing"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

func TestUserRetentionBoundarySelection(t *testing.T) {
	message := func(id gent.EntryId, parts ...gent.ContentPart) *gent.UserMessageRetentionEntry {
		return &gent.UserMessageRetentionEntry{EntryId: id, Message: &gent.ModelMessage{
			Role: gent.ChatMessageTypeHuman, Parts: parts}}
	}
	clipped := func(id gent.EntryId, parts ...gent.ContentPart) *gent.UserMessageRetentionEntry {
		entry := message(id, parts...)
		entry.Truncated = true
		return entry
	}
	data, err := gent.DataPart(map[string]any{"a": 1})
	if !assert.NoError(t, err) {
		return
	}
	cache := &gent.CacheControl{Type: "ephemeral"}
	type input struct {
		messages []*gent.UserMessageRetentionEntry
		max      int
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
	cases := []testCase{
		{"unchanged", input{[]*gent.UserMessageRetentionEntry{
			message("a", gent.TextPart("first")), message("b", gent.TextPart("last"))}, 9, false},
			expected{&gent.UserMessageRetentionResult{Messages: []*gent.UserMessageRetentionEntry{
				message("a", gent.TextPart("first")), message("b", gent.TextPart("last"))},
				RetainedBytes: 9}, ""}},
		{"boundary and newest", input{[]*gent.UserMessageRetentionEntry{
			message("a", gent.TextPart("first")), message("b", gent.TextPart("last"))}, 7, false},
			expected{&gent.UserMessageRetentionResult{Messages: []*gent.UserMessageRetentionEntry{
				clipped("a", gent.TextPart("st")), message("b", gent.TextPart("last"))},
				RetainedBytes: 7, EarlierContentOmitted: true}, ""}},
		{"UTF-8 boundary", input{[]*gent.UserMessageRetentionEntry{
			message("a", gent.TextPart("older界é"))}, 5, false},
			expected{&gent.UserMessageRetentionResult{Messages: []*gent.UserMessageRetentionEntry{
				clipped("a", gent.TextPart("é"))}, RetainedBytes: 3, EarlierContentOmitted: true}, ""}},
		{"indivisible media boundary", input{[]*gent.UserMessageRetentionEntry{
			message("a", gent.TextPart("older"), gent.BinaryPart("image/png", []byte{1, 2, 3, 4}),
				gent.TextPart("new"))}, 6, false},
			expected{&gent.UserMessageRetentionResult{Messages: []*gent.UserMessageRetentionEntry{
				clipped("a", gent.TextPart("new"))}, RetainedBytes: 4, EarlierContentOmitted: true}, ""}},
		{"notice only", input{[]*gent.UserMessageRetentionEntry{
			message("a", gent.TextPart("new"))}, 1, false},
			expected{&gent.UserMessageRetentionResult{RetainedBytes: 1, EarlierContentOmitted: true}, ""}},
		{"previous omission", input{[]*gent.UserMessageRetentionEntry{
			message("a", gent.TextPart("new"))}, 8, true},
			expected{&gent.UserMessageRetentionResult{Messages: []*gent.UserMessageRetentionEntry{
				message("a", gent.TextPart("new"))}, RetainedBytes: 4, EarlierContentOmitted: true}, ""}},
		{"structured bytes", input{[]*gent.UserMessageRetentionEntry{message("a", data)}, 7, false},
			expected{&gent.UserMessageRetentionResult{Messages: []*gent.UserMessageRetentionEntry{
				message("a", data)}, RetainedBytes: 7}, ""}},
		{"wrapped suffix", input{[]*gent.UserMessageRetentionEntry{message("a", gent.CachedContent{
			ContentPart: gent.TextPart("abcdef"), CacheControl: cache})}, 4, false},
			expected{&gent.UserMessageRetentionResult{Messages: []*gent.UserMessageRetentionEntry{
				clipped("a", gent.CachedContent{ContentPart: gent.TextPart("def"), CacheControl: cache})},
				RetainedBytes: 4, EarlierContentOmitted: true}, ""}},
		{"invalid allowance", input{nil, 0, false}, expected{nil,
			"gent: user retention allowance 0 must fit the complete 1-byte notice"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			input := &gent.UserMessageRetentionInput{Messages: test.input.messages,
				MaxBytes: test.input.max, Marker: "!", EarlierContentOmitted: test.input.omitted}
			codecs := gent.NewCodecRegistry()
			before, err := codecs.Marshal(input)
			if !assert.NoError(t, err) {
				return
			}
			result, err := (boundaryUserRetention{}).Retain(input)
			if test.expected.errorText == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, test.expected.errorText)
			}
			assert.Equal(t, test.expected.result, result)
			after, err := codecs.Marshal(input)
			assert.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}
