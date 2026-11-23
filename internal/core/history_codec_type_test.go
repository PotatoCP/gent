package gent

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func pointerType(depth int) reflect.Type {
	typ := reflect.TypeFor[int]()
	for range depth {
		typ = reflect.PointerTo(typ)
	}
	return typ
}

func TestCodecDerivedTypeRoundTrip(t *testing.T) {
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
	deep := reflect.Zero(reflect.SliceOf(pointerType(100))).Interface()
	tooDeep := reflect.Zero(reflect.SliceOf(pointerType(101))).Interface()
	mapKey := reflect.Zero(reflect.MapOf(pointerType(100), reflect.TypeFor[string]())).Interface()
	badMapKey := reflect.Zero(reflect.MapOf(pointerType(101), reflect.TypeFor[string]())).Interface()
	cases := []testCase{
		{"one MiB element", input{[][1048576]byte(nil)}, expected{value: [][1048576]byte(nil)}},
		{"one MiB element in empty slice", input{[][1048576]byte{}},
			expected{value: [][1048576]byte{}}},
		{"zero size element", input{[][1048576][0]byte(nil)},
			expected{value: [][1048576][0]byte(nil)}},
		{"large array map key", input{map[[1048576]byte]string(nil)},
			expected{value: map[[1048576]byte]string(nil)}},
		{"type depth boundary", input{deep}, expected{value: deep}},
		{"nested value at type depth boundary", input{map[string]any{"buffer": deep}},
			expected{value: map[string]any{"buffer": deep}}},
		{"map key type depth boundary", input{mapKey}, expected{value: mapKey}},
		{"type depth exceeded", input{tooDeep},
			expected{errorText: "gent: type nesting exceeds 100"}},
		{"map key type depth exceeded", input{badMapKey},
			expected{errorText: "gent: type nesting exceeds 100"}},
	}
	for _, test := range cases {
		for _, path := range []string{"component", "plain", "snappy", "clone"} {
			t.Run(test.name+"/"+path, func(t *testing.T) {
				codecs := NewCodecRegistry()
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
					if path == "plain" {
						codecs = NewCodecRegistry(WithCodecCompression(CodecCompressionNone))
					}
					var data []byte
					data, err = codecs.Marshal(test.input.value)
					if err != nil {
						assert.Nil(t, data)
						break
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
