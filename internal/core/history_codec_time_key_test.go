package gent

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type identityTimeKey struct {
	Time    time.Time
	Label   string
	Pointer *identityEmptyKey
	Number  float64
}

func identityTimes() (time.Time, time.Time) {
	first := time.Date(2026, 11, 21, 10, 0, 0, 0, time.FixedZone("first", 3600))
	second := time.Date(2026, 11, 21, 10, 0, 0, 0, time.FixedZone("second", 3600))
	return first, second
}

func TestTimeMapKeyNormalization(t *testing.T) {
	type input struct{ value any }
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
	values := []string{"first", "second"}
	want := expected{values: values}
	reject := func(typ string) expected {
		return expected{errorText: "gent: map " + typ +
			" has keys that collide after time normalization"}
	}
	pointers := map[identityTimeKey]string{}
	for pointer, value := range identityZeroMap() {
		pointers[identityTimeKey{Time: first, Pointer: pointer}] = value
	}
	cases := []testCase{
		{"same timestamp", input{map[time.Time]string{first: "first", second: "second"}},
			reject("map[time.Time]string")},
		{"struct", input{map[identityTimeKey]string{{Time: first}: "first", {Time: second}: "second"}},
			reject("map[gent.identityTimeKey]string")},
		{"equal signed zeros", input{map[identityTimeKey]string{
			{Time: first}: "first", {Time: second, Number: math.Copysign(0, -1)}: "second"}},
			reject("map[gent.identityTimeKey]string")},
		{"array", input{map[[1]time.Time]string{{first}: "first", {second}: "second"}},
			reject("map[[1]time.Time]string")},
		{"interface", input{map[any]string{first: "first", second: "second"}},
			reject("map[interface {}]string")},
		{"distinct instants", input{map[time.Time]string{
			first: "first", first.Add(time.Second): "second"}}, want},
		{"distinct offsets", input{map[time.Time]string{first: "first", first.UTC(): "second"}}, want},
		{"distinct fields", input{map[identityTimeKey]string{
			{Time: first, Label: "one"}: "first", {Time: second, Label: "two"}: "second"}}, want},
		{"distinct zero pointers", input{pointers}, want},
		{"nil map", input{map[time.Time]string(nil)}, expected{}},
		{"empty map", input{map[time.Time]string{}}, expected{}},
	}
	for _, test := range cases {
		for _, compression := range []CodecCompression{CodecCompressionNone, CodecCompressionSnappy} {
			t.Run(test.name+"/"+string(compression), func(t *testing.T) {
				codecs := identityCodecs(t, compression)
				component, writeError := codecs.Encode(test.input.value)
				cloned, cloneError := codecs.Clone(test.input.value)
				inputValue := []any{test.input.value, strings.Repeat("saved timestamp ", 2048)}
				data, storageError := codecs.Marshal(inputValue)
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
				if !assert.NoError(t, err) {
					return
				}
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
