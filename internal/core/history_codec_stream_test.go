package gent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCodecStreamSourceIds(t *testing.T) {
	type input struct{ sources []string }
	type expected struct {
		value StreamChunk
		err   error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	content := []ContentPart{&AssistantContent{Type: AssistantContentFinalAnswerCandidate,
		MessageId: "candidate", Content: []ContentPart{TextPart(strings.Repeat("saved answer ", 1024))}}}
	cases := []testCase{
		{"nil", input{}, expected{value: StreamChunk{Content: content}}},
		{"empty", input{[]string{}}, expected{value: StreamChunk{Content: content,
			SourceMessageIds: []string{}}}},
		{"populated", input{[]string{"source-a", "source-b"}}, expected{value: StreamChunk{
			Content: content, SourceMessageIds: []string{"source-a", "source-b"}}}},
	}
	for _, test := range cases {
		for _, path := range []string{"component", "plain", "compressed"} {
			t.Run(test.name+"/"+path, func(t *testing.T) {
				compression := CodecCompressionSnappy
				if path == "plain" {
					compression = CodecCompressionNone
				}
				codecs := NewCodecRegistry(WithCodecCompression(compression))
				original := StreamChunk{Content: content, SourceMessageIds: test.input.sources}
				var actual StreamChunk
				var err error
				if path == "component" {
					encoded, encodeErr := codecs.Encode(original)
					if !assert.NoError(t, encodeErr) {
						return
					}
					decoded, decodeErr := codecs.Decode(encoded)
					err = decodeErr
					if err == nil {
						actual = decoded.(StreamChunk)
					}
				} else {
					data, encodeErr := codecs.Marshal(original)
					if !assert.NoError(t, encodeErr) {
						return
					}
					err = codecs.Unmarshal(data, &actual)
					clear(data)
				}
				assert.Equal(t, test.expected, expected{actual, err})
				if len(actual.SourceMessageIds) > 0 {
					actual.SourceMessageIds[0] = "changed"
					assert.Equal(t, []string{"source-a", "source-b"}, original.SourceMessageIds)
				}
			})
		}
	}
}
