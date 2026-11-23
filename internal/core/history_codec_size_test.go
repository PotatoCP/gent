package gent

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Recording allowances use the uncompressed document length. Cover escaping, shared references with
// several index widths, opaque JSON, and failed encodings so scratch reuse cannot change it.
func TestSavedDocumentSize(t *testing.T) {
	type input struct {
		value  any
		limits CodecLimits
	}
	type expected struct {
		size      int
		errorText string
	}
	type testCase struct {
		name  string
		input input
	}
	shared := make([]any, 0, 400)
	for i := range 200 {
		value := []any{fmt.Sprintf("item %d: %s", i, strings.Repeat("saved ", 20)), i}
		shared = append(shared, value, value)
	}
	cases := []testCase{
		{"nil", input{value: nil}},
		{"scalars", input{value: []any{true, int64(-1), uint64(math.MaxUint64), 0.1}}},
		{"empty containers", input{value: []any{[]string(nil), []string{}, map[string]int(nil)}}},
		{"nil stream sources", input{value: StreamChunk{}}},
		{"empty stream sources", input{value: StreamChunk{SourceMessageIds: []string{}}}},
		{"stream sources", input{value: StreamChunk{SourceMessageIds: []string{"accepted"}}}},
		{"escaped text", input{value: "<&>\"\\\t\u2028\u2029\xff"}},
		{"native bytes", input{value: json.RawMessage(` { "id": "\u0061<&>" } `)}},
		{"shared references", input{value: shared}},
		{"encoded limit", input{value: shared, limits: CodecLimits{MaxEncodedBytes: 1024}}},
		{"expanded limit", input{value: shared, limits: CodecLimits{MaxExpandedBytes: 1024}}},
		{"invalid scalar", input{value: math.NaN()}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			registry := NewCodecRegistry(WithCodecLimits(test.input.limits),
				WithCodecCompression(CodecCompressionNone))
			data, err := registry.Marshal(test.input.value)
			want := expected{size: len(data)}
			if err != nil {
				want.errorText = err.Error()
			}
			for range 3 {
				size, err := registry.uncompressedSize(test.input.value)
				actual := expected{size: size}
				if err != nil {
					actual.errorText = err.Error()
				}
				assert.Equal(t, want, actual)
			}
			if want.errorText != "" {
				return
			}
			// The direct writer must still produce canonical JSON and the full typed value.
			var document savedDocument
			assert.NoError(t, json.Unmarshal(data, &document))
			canonical, err := json.Marshal(document)
			assert.NoError(t, err)
			assert.Equal(t, canonical, data)
			envelope, err := registry.Encode(test.input.value)
			assert.NoError(t, err)
			expanded, err := json.Marshal(envelope)
			assert.NoError(t, err)
			assert.Equal(t, expanded, expandTestDocument(t, registry, data))
			again, err := registry.Marshal(test.input.value)
			assert.NoError(t, err)
			assert.Equal(t, data, again)
		})
	}
}
