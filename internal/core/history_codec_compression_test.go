package gent

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/golang/snappy"
	"github.com/stretchr/testify/assert"
)

func TestCodecCompression(t *testing.T) {
	type input struct {
		value       any
		compression CodecCompression
	}
	type expected struct {
		compressed bool
		value      any
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	text := strings.Repeat("Keep the original task and all accepted tool results. ", 1024)
	noise := make([]byte, 64*1024)
	_, err := rand.New(rand.NewSource(7)).Read(noise)
	assert.NoError(t, err)
	native := json.RawMessage(` { "signature": "\u0061<&>", "original": "` + text + `" } `)
	cases := []testCase{
		{"small", input{"short", CodecCompressionSnappy}, expected{false, "short"}},
		{"plain", input{text, CodecCompressionNone}, expected{false, text}},
		{"compressed", input{text, CodecCompressionSnappy}, expected{true, text}},
		{"native bytes", input{native, CodecCompressionSnappy}, expected{true, native}},
		{"incompressible", input{noise, CodecCompressionSnappy}, expected{false, noise}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			registry := NewCodecRegistry(WithCodecCompression(test.input.compression))
			data, err := registry.Marshal(test.input.value)
			if !assert.NoError(t, err) {
				return
			}
			compressed := v3Compressed(t, data)
			// Decoding does not depend on the writer's compression preference or process state.
			var restored any
			assert.NoError(t, NewCodecRegistry(WithCodecCompression(CodecCompressionNone)).
				Unmarshal(data, &restored))
			assert.Equal(t, test.expected, expected{compressed, restored})
			// Storage policy must not change the queue allowance for the same logical value.
			plain, err := NewCodecRegistry(WithCodecCompression(CodecCompressionNone)).
				Marshal(test.input.value)
			assert.NoError(t, err)
			size, err := registry.uncompressedSize(test.input.value)
			assert.NoError(t, err)
			assert.Equal(t, len(plain), size)
		})
	}
}

// Check the saved representation itself. Enabling compression can still produce plain tables.
func v3Compressed(t *testing.T, data []byte) bool {
	t.Helper()
	var document savedDocument
	if !assert.NoError(t, json.Unmarshal(data, &document)) ||
		!assert.Equal(t, 3, document.FormatVersion) || !assert.NotEmpty(t, document.Data) {
		t.FailNow()
	}
	switch document.Data[0] {
	case '{':
		return false
	case '"':
		return true
	default:
		t.Fatalf("unexpected v3 data %s", document.Data)
		return false
	}
}

func compressedTestDocument(t *testing.T, compressed []byte) []byte {
	t.Helper()
	payload, err := json.Marshal(base64.StdEncoding.EncodeToString(compressed))
	assert.NoError(t, err)
	data, err := json.Marshal(savedDocument{FormatVersion: savedDocumentV3,
		Data: payload, Digest: v3DocumentDigest(payload)})
	assert.NoError(t, err)
	return data
}

func TestCompressedDocumentBoundsAndIntegrity(t *testing.T) {
	type input struct {
		data   []byte
		limits CodecLimits
	}
	type expected struct {
		value     any
		errorText string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	registry := NewCodecRegistry()
	value := strings.Repeat("saved ", 4096)
	data, err := registry.Marshal(value)
	assert.NoError(t, err)
	var changed savedDocument
	assert.NoError(t, json.Unmarshal(data, &changed))
	changed.Data = json.RawMessage(`{}`)
	changedEncoding, err := json.Marshal(changed)
	assert.NoError(t, err)
	cases := []testCase{
		{"encoded boundary", input{data, CodecLimits{MaxEncodedBytes: len(data)}},
			expected{value: value}},
		{"encoded overflow", input{data, CodecLimits{MaxEncodedBytes: len(data) - 1}},
			expected{value: "unchanged", errorText: fmt.Sprintf(
				"gent: encoded value exceeds %d bytes", len(data)-1)}},
		{"wire expansion", input{data, CodecLimits{MaxExpandedBytes: 1024}},
			expected{value: "unchanged", errorText: "gent: decompressed document exceeds 1024 bytes"}},
		{"declared expansion before allocation", input{
			compressedTestDocument(t, binary.AppendUvarint(nil, 1<<30)), CodecLimits{}},
			expected{value: "unchanged", errorText: "gent: decompressed document exceeds 268435456 bytes"}},
		{"truncated block", input{compressedTestDocument(t, []byte{20}), CodecLimits{}},
			expected{value: "unchanged", errorText: "gent: decompress document: snappy: corrupt input"}},
		{"changed compression representation", input{changedEncoding, CodecLimits{}},
			expected{value: "unchanged", errorText: "gent: saved document digest mismatch"}},
		{"invalid expanded tables", input{compressedTestDocument(t, snappy.Encode(nil,
			[]byte(`{"types":[],"layouts":[],"shared":[],"root":[-2,0]}`))), CodecLimits{}},
			expected{value: "unchanged", errorText: "gent: invalid or forward shared reference"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			registry := NewCodecRegistry(WithCodecLimits(test.input.limits))
			actual := expected{value: "unchanged"}
			if err := registry.Unmarshal(test.input.data, &actual.value); err != nil {
				actual.errorText = err.Error()
			}
			assert.Equal(t, test.expected, actual)
		})
	}
}

func TestCompressedDocumentOwnsMutableValues(t *testing.T) {
	registry := NewCodecRegistry()
	original := bytes.Repeat([]byte("original media "), 4096)
	data, err := registry.Marshal([][]byte{original, original, original})
	assert.NoError(t, err)
	before := bytes.Clone(data)
	var values [][]byte
	assert.NoError(t, registry.Unmarshal(data, &values))
	assert.Equal(t, [][]byte{original, original, original}, values)
	values[0][0] = '!'
	assert.Equal(t, [][]byte{original, original}, values[1:])
	assert.Equal(t, before, data)
}

func FuzzCompressedSavedDocument(f *testing.F) {
	f.Add(snappy.Encode(nil, []byte(oversizedArrayDocument)))
	f.Add(snappy.Encode(nil, []byte(`{"types":["nil"],"layouts":[[["type",{"type":0}],`+
		`["version",{"constant":1}],["data"]]],"shared":[],"root":[0,null]}`)))
	f.Add(snappy.Encode(nil, []byte(`{"types":[],"layouts":[],"shared":[],"root":[-2,0]}`)))
	f.Add(binary.AppendUvarint(nil, 1<<30))
	f.Fuzz(func(t *testing.T, compressed []byte) {
		if len(compressed) > 16*1024 {
			return
		}
		registry := NewCodecRegistry(WithCodecLimits(CodecLimits{
			MaxExpandedBytes: 64 * 1024, MaxExpandedValues: 8192, MaxBinaryBytes: 16 * 1024,
		}))
		var value any
		if err := registry.Unmarshal(compressedTestDocument(t, compressed), &value); err != nil {
			return
		}
		data, err := registry.Marshal(value)
		if !assert.NoError(t, err) {
			return
		}
		var restored any
		assert.NoError(t, registry.Unmarshal(data, &restored))
		assert.Equal(t, value, restored)
	})
}
