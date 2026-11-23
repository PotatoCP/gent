package gent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type repeatedFields struct {
	ID         string
	Sources    []string
	Values     []any
	Attributes map[string]any
	Chunk      StreamChunk
	Callback   func() `json:"-"`
}

func repeatedFieldValue() repeatedFields {
	sources := make([]string, 0, 1280)
	for i := range 640 {
		text := fmt.Sprintf("source-%03d: <&> 世界 %s", i, strings.Repeat("saved ", i%4))
		sources = append(sources, text, text)
	}
	return repeatedFields{ID: "accepted", Sources: sources,
		Values: []any{nil, []string(nil), []string{}, []byte{0, 255},
			json.RawMessage(` { "value": "\u0061<&>" } `), int64(-7), uint64(1 << 63),
			map[string]any{"nil": nil, "empty": []string{}, "pointer": new(int)}},
		Attributes: map[string]any{"nil": nil, "empty": []string{}, "text": "accepted",
			"long": strings.Repeat("Keep the original task and every accepted answer. ", 128)},
		Chunk: StreamChunk{SourceMessageIds: []string{}}}
}

// These reference digests came from the source before the encoding optimization. They cover
// the whole component, storage document, and receipt, rather than selected fields. The fixture
// crosses the text-cache bound and includes escaped text, native bytes, and nil/empty values.
func TestRepeatedEncodingKeepsBytesAndReceipts(t *testing.T) {
	type input struct{ compression CodecCompression }
	type expected struct {
		document string
		size     int
		err      error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"plain", input{CodecCompressionNone}, expected{
			document: "81a2c25d6b70bece7f2c7e21ab687bc8958af1e6d0190852fb4f2cf0b3e18bc6", size: 54435}},
		{"compressed", input{CodecCompressionSnappy}, expected{
			document: "309d2800c6a4f63ccce5d7ebd8520d134b3e2620d3c72c79d5393021e8bad84a", size: 10113}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			registry := NewCodecRegistry(WithCodecCompression(test.input.compression))
			assert.NoError(t, registry.RegisterType("test.repeated-fields", 1, repeatedFields{}))
			original := repeatedFieldValue()
			for range 3 {
				component, err := registry.Encode(original)
				assert.NoError(t, err)
				data, err := json.Marshal(component)
				assert.NoError(t, err)
				assert.Equal(t, "faae91846bbaebda0ccdad82ce1b0781e3d54a9b7091693260125697df2e15ad",
					encodingDigest(data))
				data, err = registry.Marshal(original)
				assert.Equal(t, test.expected.err, err)
				assert.Equal(t, test.expected.size, len(data))
				assert.Equal(t, test.expected.document, encodingDigest(data))
				var restored repeatedFields
				assert.NoError(t, registry.Unmarshal(data, &restored))
				assert.Equal(t, original, restored)
				clone, err := registry.Clone(original)
				assert.NoError(t, err)
				assert.Equal(t, original, clone)
				restored.Sources[0] = "changed"
				restored.Attributes["long"] = "changed"
				assert.Equal(t, repeatedFieldValue(), original)
				assert.Equal(t, original, clone)
				batch := &SaveBatch{SchemaVersion: RunSchemaVersion, RunId: "repeat-check",
					Events: []*RecordedEvent{{RunId: "repeat-check", EventSequenceNumber: 1, Type: "app:repeat",
						Ts: time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC), Payload: original}}}
				digest, err := BatchDigest(batch, registry)
				assert.NoError(t, err)
				assert.Equal(t, "beb7744437c384bd9de74e4d8f6d2f3d74f5ac135354b51cc99a0113cf82229b", digest)
			}
		})
	}
}

func encodingDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Reused map assignment slots must not retain a previous value when the next entry is nil.
// Both decoding and Clone must give each occurrence its own mutable storage.
func TestMapCopyKeepsNilAndOwnedValues(t *testing.T) {
	type input struct {
		copy func(*CodecRegistry, any) (any, error)
	}
	type expected struct{ err error }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{name: "clone", input: input{copy: func(c *CodecRegistry, v any) (any, error) {
			return c.Clone(v)
		}}},
		{name: "component", input: input{copy: func(c *CodecRegistry, v any) (any, error) {
			encoded, err := c.Encode(v)
			if err != nil {
				return nil, err
			}
			return c.Decode(encoded)
		}}},
		{name: "storage", input: input{copy: func(c *CodecRegistry, v any) (any, error) {
			data, err := c.Marshal(v)
			if err != nil {
				return nil, err
			}
			var restored any
			err = c.Unmarshal(data, &restored)
			return restored, err
		}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			registry := NewCodecRegistry()
			shared := []int{7, 8}
			original := map[any]any{nil: nil, "a": shared, "b": shared, "c": []int(nil), "d": []int{}}
			for range 20 {
				copied, err := test.input.copy(registry, original)
				assert.Equal(t, test.expected.err, err)
				assert.Equal(t, original, copied)
				copyMap := copied.(map[any]any)
				copyMap["a"].([]int)[0] = 99
				assert.Equal(t, []int{7, 8}, copyMap["b"])
				assert.Equal(t, []int{7, 8}, original["a"])
				assert.Equal(t, []int{7, 8}, original["b"])
			}
		})
	}
}

// Invalid child data must fail without reserving scratch space for every remaining child. A
// zero-size Go element makes this visible without first allocating a large application value.
type rejectedEncodingValue struct{ hidden struct{} }

func TestInvalidContainerBoundsInitialAllocation(t *testing.T) {
	type input struct{ value any }
	type expected struct {
		value     EncodedValue
		errorText string
		maxBytes  uint64
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	values := make(map[int]rejectedEncodingValue, 131072)
	for i := range 131072 {
		values[i] = rejectedEncodingValue{}
	}
	want := expected{
		errorText: "gent: unexported field gent.rejectedEncodingValue.hidden",
		maxBytes:  1 << 20,
	}
	cases := []testCase{
		{"slice", input{make([]rejectedEncodingValue, 131072)}, want},
		{"array", input{[131072]rejectedEncodingValue{}}, want},
		{"map", input{values}, want},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			registry := NewCodecRegistry()
			assert.NoError(t, registry.RegisterType("test.rejected-child", 1, rejectedEncodingValue{}))
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			value, err := registry.Encode(test.input.value)
			runtime.ReadMemStats(&after)
			assert.Equal(t, test.expected.value, value)
			assert.EqualError(t, err, test.expected.errorText)
			assert.LessOrEqual(t, after.TotalAlloc-before.TotalAlloc, test.expected.maxBytes)
		})
	}
}
