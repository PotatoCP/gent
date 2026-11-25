package gent

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/golang/snappy"
	"github.com/stretchr/testify/assert"
)

type depthSlice []depthSlice
type depthMap map[string]depthMap
type depthPointer *depthNode
type depthNode struct{ Next depthPointer }
type depthArray [1]*depthArray
type depthValues []any
type depthObject map[string]any
type depthBox struct{ Value any }
type depthBytes []byte

func depthCodecs(t *testing.T, compression CodecCompression) *CodecRegistry {
	t.Helper()
	codecs := NewCodecRegistry(WithCodecCompression(compression))
	for _, value := range []any{depthSlice{}, depthMap{}, depthPointer(nil), depthArray{},
		depthValues{}, depthObject{}, depthBox{}, depthBytes{}} {
		if !assert.NoError(t, codecs.RegisterValueType(reflect.TypeOf(value))) {
			t.FailNow()
		}
	}
	return codecs
}

func sliceAtDepth(depth int) depthSlice {
	value := depthSlice{}
	for range depth {
		value = depthSlice{value}
	}
	return value
}

func mapAtDepth(depth int) depthMap {
	value := depthMap{}
	for range depth {
		value = depthMap{"next": value}
	}
	return value
}

func pointerAtDepth(depth int) depthPointer {
	var value depthPointer
	for range depth / 2 {
		value = depthPointer(&depthNode{Next: value})
	}
	return value
}

func arrayAtDepth(depth int) *depthArray {
	var value *depthArray
	for range depth / 2 {
		value = &depthArray{value}
	}
	return value
}

func wrappedAtDepth(depth int, leaf any) any {
	value := leaf
	for range depth {
		value = depthValues{value}
	}
	return value
}

func mixedAtDepth(depth int) any {
	var value any = depthBytes{}
	for range depth / 4 {
		value = depthValues{&depthBox{Value: depthObject{"next": value}}}
	}
	return value
}

func TestCodecLogicalValueDepth(t *testing.T) {
	type input struct{ value any }
	type expected struct {
		value     any
		errorText string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	const limitError = "gent: value nesting exceeds 100"
	part, err := DataPart("captured value")
	assert.NoError(t, err)
	partEnvelope, err := NewCodecRegistry().Encode(part)
	assert.NoError(t, err)
	partError := "encode " + partEnvelope.Type + ": " + limitError
	cases := []testCase{
		{"nil named bytes", input{depthBytes(nil)}, expected{value: depthBytes(nil)}},
		{"named bytes", input{depthBytes{1, 2, 3}}, expected{value: depthBytes{1, 2, 3}}},
		{"slice below boundary", input{sliceAtDepth(99)}, expected{value: sliceAtDepth(99)}},
		{"slice boundary", input{sliceAtDepth(100)}, expected{value: sliceAtDepth(100)}},
		{"slice too deep", input{sliceAtDepth(101)}, expected{errorText: limitError}},
		{"map boundary", input{mapAtDepth(100)}, expected{value: mapAtDepth(100)}},
		{"map too deep", input{mapAtDepth(101)}, expected{errorText: limitError}},
		{"pointer and struct boundary", input{pointerAtDepth(100)},
			expected{value: pointerAtDepth(100)}},
		{"pointer and struct too deep", input{pointerAtDepth(102)}, expected{
			errorText: strings.Repeat("gent.depthNode.Next: ", 50) + limitError}},
		{"array and pointer boundary", input{arrayAtDepth(100)},
			expected{value: arrayAtDepth(100)}},
		{"array and pointer too deep", input{arrayAtDepth(102)}, expected{errorText: limitError}},
		{"interface and named byte boundary", input{wrappedAtDepth(100, depthBytes{})},
			expected{value: wrappedAtDepth(100, depthBytes{})}},
		{"interface too deep", input{wrappedAtDepth(101, depthBytes{})},
			expected{errorText: limitError}},
		{"mixed boundary", input{mixedAtDepth(100)}, expected{value: mixedAtDepth(100)}},
		{"codec child boundary", input{wrappedAtDepth(99, part)},
			expected{value: wrappedAtDepth(99, part)}},
		{"codec child too deep", input{wrappedAtDepth(100, part)}, expected{errorText: partError}},
	}
	for _, test := range cases {
		for _, path := range []string{"component", "plain", "compressed", "default", "clone"} {
			t.Run(test.name+"/"+path, func(t *testing.T) {
				compression := CodecCompressionSnappy
				if path == "plain" || path == "compressed" {
					compression = CodecCompressionNone
				}
				codecs := depthCodecs(t, compression)
				var actual expected
				var err error
				switch path {
				case "component":
					var encoded EncodedValue
					encoded, err = codecs.Encode(test.input.value)
					if err != nil {
						assert.Equal(t, EncodedValue{}, encoded)
						break
					}
					actual.value, err = codecs.Decode(encoded)
				case "clone":
					actual.value, err = codecs.Clone(test.input.value)
				default:
					var data []byte
					data, err = codecs.Marshal(test.input.value)
					if err != nil {
						assert.Nil(t, data)
						break
					}
					if path == "compressed" {
						// Deep values can still have small tables. Exercise compressed reading even
						// when the default writer would correctly keep the smaller plain document.
						var document savedDocument
						assert.NoError(t, json.Unmarshal(data, &document))
						data = compressedTestDocument(t, snappy.Encode(nil, document.Data))
					}
					err = codecs.Unmarshal(data, &actual.value)
				}
				if err != nil {
					actual.errorText = err.Error()
				}
				assert.Equal(t, test.expected, actual)
			})
		}
	}
}

// Build incoming envelopes through the public component representation. The encoder must reject
// the excessive logical depth, but the decoder must also reject it in an untrusted saved document.
func savedDepthSlice(t *testing.T, codecs *CodecRegistry, depth int) EncodedValue {
	t.Helper()
	type container struct {
		Element string         `json:"element"`
		Values  []EncodedValue `json:"values"`
	}
	value, err := codecs.Encode(depthSlice{})
	assert.NoError(t, err)
	for range depth {
		data, err := json.Marshal(container{value.Type, []EncodedValue{value}})
		assert.NoError(t, err)
		inner := EncodedValue{Type: "slice", Version: 1, Data: data}
		value.Data, err = json.Marshal(inner)
		assert.NoError(t, err)
	}
	return value
}

func TestCodecNamedEnvelopeValidation(t *testing.T) {
	type input struct{ value EncodedValue }
	type expected struct {
		value     any
		errorText string
		status    RunStateLoadStatus
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	codecs := depthCodecs(t, CodecCompressionNone)
	leaf, err := codecs.Encode(depthSlice{})
	assert.NoError(t, err)
	wrap := func(inner EncodedValue) EncodedValue {
		data, err := json.Marshal(inner)
		assert.NoError(t, err)
		return EncodedValue{Type: leaf.Type, Version: 1, Data: data}
	}
	nested := leaf
	for range 120 {
		nested = wrap(nested)
	}
	cases := []testCase{
		{"boundary", input{value: savedDepthSlice(t, codecs, 100)},
			expected{value: sliceAtDepth(100)}},
		{"excessive logical depth", input{value: savedDepthSlice(t, codecs, 101)}, expected{
			errorText: "gent: value nesting exceeds 100", status: RunStateInvalid}},
		{"nested named envelopes", input{value: nested}, expected{errorText: fmt.Sprintf(
			"gent: named container %s requires slice encoding, got %q", leaf.Type, leaf.Type),
			status: RunStateInvalid}},
		{"wrong underlying kind", input{value: wrap(EncodedValue{
			Type: "map", Version: 1, Data: json.RawMessage(`{"nil":true}`)})}, expected{
			errorText: fmt.Sprintf("gent: named container %s requires slice encoding, got %q",
				leaf.Type, "map"), status: RunStateInvalid}},
		{"wrong underlying version", input{value: wrap(EncodedValue{
			Type: "slice", Version: 2, Data: json.RawMessage(`{"nil":true}`)})}, expected{
			errorText: "gent: unsupported slice version 2", status: RunStateIncompatible}},
	}
	for _, test := range cases {
		for _, compressed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/compressed=%t", test.name, compressed), func(t *testing.T) {
				data := savedTypeDocument(t, test.input.value)
				if compressed {
					var document savedDocument
					assert.NoError(t, json.Unmarshal(data, &document))
					data = compressedTestDocument(t, snappy.Encode(nil, document.Data))
				}
				value, err := codecs.Decode(test.input.value)
				if test.expected.errorText == "" {
					assert.NoError(t, err)
					assert.Equal(t, test.expected.value, value)
					var loaded any
					assert.NoError(t, codecs.Unmarshal(data, &loaded))
					assert.Equal(t, test.expected.value, loaded)
					return
				}
				assert.EqualError(t, err, test.expected.errorText)
				assert.Nil(t, value)
				var target any = "unchanged"
				assert.EqualError(t, codecs.Unmarshal(data, &target), test.expected.errorText)
				assert.Equal(t, "unchanged", target)
				result, err := codecs.UnmarshalRunState(data)
				assert.NoError(t, err)
				assert.Equal(t, &RunStateLoadResult{Status: test.expected.status,
					Detail: test.expected.errorText}, result)
			})
		}
	}
}
