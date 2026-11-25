package gent

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type codecNilErrorKey struct {
	When    time.Time
	Problem error
	Value   any
	Pointer *int
}

func nilKeyCollision(value any) string {
	return "gent: map " + reflect.TypeOf(value).String() +
		" has keys that collide after time or nil-error normalization"
}

func TestNilErrorKeyNormalization(t *testing.T) {
	type input struct {
		value  any
		custom bool
	}
	type expected struct {
		values    []string
		errorText string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	first, second := identityTimes()
	var typedNil *codecPointerError
	type collisionCase struct {
		name  string
		value any
	}
	bad := []collisionCase{
		{"SavedError keys", map[error]string{nil: "first", (*SavedError)(nil): "second"}},
		{"application error keys", map[error]string{nil: "first", typedNil: "second"}},
		{"two typed nil keys", map[error]string{(*codecConcreteError)(nil): "first", typedNil: "second"}},
		{"any keys", map[any]string{nil: "first", typedNil: "second"}},
		{"array keys", map[[1]error]string{{nil}: "first", {typedNil}: "second"}},
		{"struct keys", map[codecNilErrorKey]string{{}: "first", {Problem: typedNil}: "second"}},
		{"combined keys", map[codecNilErrorKey]string{
			{When: first}: "first", {When: second, Value: typedNil}: "second"}},
	}
	var cases []testCase
	for _, test := range bad {
		cases = append(cases, testCase{test.name, input{value: test.value},
			expected{errorText: nilKeyCollision(test.value)}})
	}
	cases = append(cases,
		testCase{"nil", input{value: map[error]string(nil)}, expected{}},
		testCase{"empty", input{value: map[error]string{}}, expected{}},
		testCase{"nil key", input{value: map[error]string{nil: "first"}},
			expected{values: []string{"first"}}},
		testCase{"typed nil key", input{value: map[error]string{typedNil: "first"}},
			expected{values: []string{"first"}}},
		testCase{"non-nil errors", input{value: map[error]string{
			errors.New("same"): "first", errors.New("same"): "second"}},
			expected{values: []string{"first", "second"}}},
		testCase{"owned pointers", input{value: map[codecNilErrorKey]string{
			{When: first, Pointer: new(int)}:                     "first",
			{When: second, Problem: typedNil, Pointer: new(int)}: "second"}},
			expected{values: []string{"first", "second"}}},
		testCase{"explicit nil codec", input{value: map[error]string{nil: "first", typedNil: "second"},
			custom: true}, expected{values: []string{"first", "second"}}},
	)
	for _, test := range cases {
		for _, compression := range []CodecCompression{CodecCompressionNone, CodecCompressionSnappy} {
			t.Run(test.name+"/"+string(compression), func(t *testing.T) {
				codecs := errorValueCodecs(t, compression, test.input.custom)
				component, writeError := codecs.Encode(test.input.value)
				cloned, cloneError := codecs.Clone(test.input.value)
				data, storageError := codecs.Marshal([]any{test.input.value,
					strings.Repeat("Keep every logical map entry. ", 1024)})
				if test.expected.errorText != "" {
					assert.Equal(t, 2, reflect.ValueOf(test.input.value).Len())
					for _, err := range []error{writeError, cloneError, storageError} {
						assert.EqualError(t, err, test.expected.errorText)
					}
					assert.Equal(t, EncodedValue{}, component)
					assert.Nil(t, cloned)
					assert.Nil(t, data)
					return
				}
				if !assert.NoError(t, writeError) || !assert.NoError(t, cloneError) ||
					!assert.NoError(t, storageError) {
					return
				}
				assert.Equal(t, compression == CodecCompressionSnappy, v3Compressed(t, data))
				decoded, err := codecs.Decode(component)
				assert.NoError(t, err)
				var restored []any
				if !assert.NoError(t, codecs.Unmarshal(data, &restored)) {
					return
				}
				for _, value := range []any{decoded, cloned, restored[0]} {
					assert.Equal(t, reflect.TypeOf(test.input.value), reflect.TypeOf(value))
					assert.Equal(t, test.expected, expected{values: identityMapValues(value)})
					assert.Equal(t, reflect.ValueOf(test.input.value).IsNil(), reflect.ValueOf(value).IsNil())
					again, err := codecs.Encode(value)
					assert.NoError(t, err)
					assert.Equal(t, component, again)
					wantDigest, err := BatchDigest(mapOrderBatch(test.input.value), codecs)
					assert.NoError(t, err)
					digest, err := BatchDigest(mapOrderBatch(value), codecs)
					assert.NoError(t, err)
					assert.Equal(t, wantDigest, digest)
				}
			})
		}
	}
}
