package gent

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

type identityEmptyKey struct{}
type identityPointerKey struct{ Key *identityEmptyKey }
type identityNamedPointer *identityEmptyKey

// Fields in nonzero allocations give the input distinct addresses without unsafe code. Separate
// new(empty) calls cannot prove this: Go permits zero-size allocations to share an address.
func identityZeroMap() map[*identityEmptyKey]string {
	type holder struct {
		Key identityEmptyKey
		Pad byte
	}
	storage := new([2]holder)
	return map[*identityEmptyKey]string{&storage[0].Key: "first", &storage[1].Key: "second"}
}

func identityCodecs(t *testing.T, compression CodecCompression) *CodecRegistry {
	t.Helper()
	codecs := NewCodecRegistry(WithCodecCompression(compression))
	assert.NoError(t, codecs.RegisterType("app.empty-key", 1, identityEmptyKey{}))
	assert.NoError(t, codecs.RegisterType("app.pointer-key", 1, identityPointerKey{}))
	assert.NoError(t, codecs.RegisterType("app.named-pointer", 1, identityNamedPointer(nil)))
	assert.NoError(t, codecs.RegisterType("app.time-key", 1, identityTimeKey{}))
	assert.NoError(t, codecs.RegisterType("app.string-record", 1, identityStringRecord{}))
	assert.NoError(t, codecs.RegisterType("app.string", 1, identityString("")))
	assert.NoError(t, codecs.RegisterType("app.json-string", 1, identityJSONString("")))
	assert.NoError(t, codecs.RegisterType("app.text-string", 1, identityTextString("")))
	return codecs
}

func identityMapValues(value any) []string {
	var values []string
	iter := reflect.ValueOf(value).MapRange()
	for iter.Next() {
		values = append(values, iter.Value().String())
	}
	sort.Strings(values)
	return values
}

func identityKeyPointer(value reflect.Value) uintptr {
	for value.Kind() != reflect.Pointer {
		switch value.Kind() {
		case reflect.Interface:
			value = value.Elem()
		case reflect.Struct:
			value = value.Field(0)
		case reflect.Array:
			value = value.Index(0)
		}
	}
	return value.Pointer()
}

func TestZeroSizePointerMapIdentity(t *testing.T) {
	type input struct{ value any }
	type expected struct {
		values []string
		nilMap bool
		err    error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	pointers := identityZeroMap()
	structs, arrays := map[identityPointerKey]string{}, map[[1]*identityEmptyKey]string{}
	interfaces, named := map[any]string{}, map[identityNamedPointer]string{}
	for key, value := range pointers {
		structs[identityPointerKey{key}], arrays[[1]*identityEmptyKey{key}] = value, value
		interfaces[key], named[identityNamedPointer(key)] = value, value
	}
	type holder struct {
		Key [0]byte
		Pad byte
	}
	storage := new([2]holder)
	zeroArrays := map[*[0]byte]string{&storage[0].Key: "first", &storage[1].Key: "second"}
	want := expected{values: []string{"first", "second"}}
	cases := []testCase{
		{"pointer", input{pointers}, want},
		{"struct", input{structs}, want},
		{"array", input{arrays}, want},
		{"interface", input{interfaces}, want},
		{"named pointer", input{named}, want},
		{"zero array", input{zeroArrays}, want},
		{"nil map", input{map[*identityEmptyKey]string(nil)}, expected{nilMap: true}},
		{"empty map", input{map[*identityEmptyKey]string{}}, expected{}},
	}
	for _, test := range cases {
		for _, compression := range []CodecCompression{CodecCompressionNone, CodecCompressionSnappy} {
			t.Run(test.name+"/"+string(compression), func(t *testing.T) {
				codecs := identityCodecs(t, compression)
				assert.Equal(t, test.expected.values, identityMapValues(test.input.value))
				encoded, err := codecs.Encode(test.input.value)
				if !assert.NoError(t, err) {
					return
				}
				// Padding makes the compressed case use format 2, rather than only its policy.
				inputValue := []any{test.input.value, strings.Repeat("saved map content ", 2048)}
				data, err := codecs.Marshal(inputValue)
				if !assert.NoError(t, err) {
					return
				}
				var document struct{ FormatVersion int }
				assert.NoError(t, json.Unmarshal(data, &document))
				version := 1
				if compression == CodecCompressionSnappy {
					version = 2
				}
				assert.Equal(t, version, document.FormatVersion)
				component, err := codecs.Decode(encoded)
				if !assert.NoError(t, err) {
					return
				}
				cloned, err := codecs.Clone(test.input.value)
				if !assert.NoError(t, err) {
					return
				}
				var restored []any
				if !assert.NoError(t, codecs.Unmarshal(data, &restored)) {
					return
				}
				seen := map[uintptr]bool{}
				for _, value := range []any{test.input.value, component, cloned, restored[0]} {
					assert.Equal(t, reflect.TypeOf(test.input.value), reflect.TypeOf(value))
					assert.Equal(t, test.expected, expected{values: identityMapValues(value),
						nilMap: reflect.ValueOf(value).IsNil()})
					iter := reflect.ValueOf(value).MapRange()
					for iter.Next() {
						pointer := identityKeyPointer(iter.Key())
						assert.False(t, seen[pointer], "each occurrence must own a distinct target")
						seen[pointer] = true
					}
					for attempt := 0; attempt < 8; attempt++ {
						again, err := codecs.Encode(value)
						assert.NoError(t, err)
						assert.Equal(t, encoded, again)
						wantDigest, err := BatchDigest(mapOrderBatch(test.input.value), codecs)
						assert.NoError(t, err)
						digest, err := BatchDigest(mapOrderBatch(value), codecs)
						assert.NoError(t, err)
						assert.Equal(t, wantDigest, digest)
					}
				}
				again, err := codecs.Marshal(restored)
				assert.NoError(t, err)
				assert.Equal(t, data, again)
				reflect.ValueOf(component).Clear()
				assert.Equal(t, test.expected.values, identityMapValues(test.input.value))
				assert.Equal(t, test.expected.values, identityMapValues(cloned))
				assert.Equal(t, test.expected.values, identityMapValues(restored[0]))
			})
		}
	}
}
