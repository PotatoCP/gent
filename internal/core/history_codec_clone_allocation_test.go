package gent

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

type cloneLargeRecord struct{ Values [17]int64 }
type cloneSliceRecord struct{ Values []int64 }
type cloneNestedRecord struct{ Record cloneSliceRecord }
type cloneMapRecord struct{ Values map[uint8]int64 }
type cloneBoundaryRecord struct{ Values [16]int64 }
type cloneEmptyElement struct{}
type cloneBoundBytes []byte

type cloneLargeCodec struct{}

func (cloneLargeCodec) Type() string { return "app.large" }
func (cloneLargeCodec) Version() int { return 1 }
func (cloneLargeCodec) Encode(value any) (json.RawMessage, error) {
	return json.Marshal(value.(cloneLargeRecord).Values)
}
func (cloneLargeCodec) Decode(data json.RawMessage) (any, error) {
	var value cloneLargeRecord
	err := json.Unmarshal(data, &value.Values)
	return value, err
}

func TestCloneAllocationBounds(t *testing.T) {
	type input struct {
		value       any
		limit       int
		binaryLimit int
		custom      bool
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
	largeKeys, largeValues := map[int64]uint8{}, map[uint8]int64{}
	for i := range 17 {
		largeKeys[int64(i)], largeValues[uint8(i)] = uint8(i), int64(i)
	}
	boundaryMap := map[int64]int64{}
	for i := range 16 {
		boundaryMap[int64(i)] = int64(i)
	}
	boundary := make([]int64, 16)
	zero := make([]cloneEmptyElement, 129)
	allocationError := "gent: decoded allocation of 17 int64 values (8 bytes each) exceeds 128 bytes"
	arrayError := "gent: decoded allocation of 1 [17]int64 values " +
		"(136 bytes each) exceeds 128 bytes"
	recordError := "gent: decoded allocation of 1 gent.cloneLargeRecord values " +
		"(136 bytes each) exceeds 128 bytes"
	cases := []testCase{
		{"slice", input{value: make([]int64, 17), limit: 128}, expected{errorText: allocationError}},
		{"map keys", input{value: largeKeys, limit: 128}, expected{errorText: allocationError}},
		{"map values", input{value: largeValues, limit: 128}, expected{errorText: allocationError}},
		{"array", input{value: [17]int64{}, limit: 128}, expected{errorText: arrayError}},
		{"record", input{value: cloneLargeRecord{}, limit: 128}, expected{errorText: recordError}},
		{"nested slice", input{value: cloneSliceRecord{make([]int64, 17)}, limit: 128},
			expected{errorText: "gent.cloneSliceRecord.Values: " + allocationError}},
		{"nested record", input{value: cloneNestedRecord{cloneSliceRecord{make([]int64, 17)}},
			limit: 128}, expected{errorText: "gent.cloneNestedRecord.Record: " +
			"gent.cloneSliceRecord.Values: " + allocationError}},
		{"nested map", input{value: cloneMapRecord{largeValues}, limit: 128},
			expected{errorText: "gent.cloneMapRecord.Values: " + allocationError}},
		{"pointer", input{value: &[17]int64{}, limit: 128}, expected{errorText: arrayError}},
		{"slice boundary", input{value: boundary, limit: 128}, expected{value: boundary}},
		{"separate map bounds", input{value: boundaryMap, limit: 128}, expected{value: boundaryMap}},
		{"array boundary", input{value: [16]int64{}, limit: 128}, expected{value: [16]int64{}}},
		{"record boundary", input{value: cloneBoundaryRecord{}, limit: 128},
			expected{value: cloneBoundaryRecord{}}},
		{"nil map", input{value: map[int64]int64(nil), limit: 128},
			expected{value: map[int64]int64(nil)}},
		{"empty map", input{value: map[int64]int64{}, limit: 128}, expected{value: map[int64]int64{}}},
		{"nil slice", input{value: []int64(nil), limit: 128}, expected{value: []int64(nil)}},
		{"empty slice", input{value: []int64{}, limit: 128}, expected{value: []int64{}}},
		{"zero-size elements", input{value: zero, limit: 128}, expected{value: zero}},
		{"byte buffer", input{value: cloneBoundBytes(make([]byte, 129)), limit: 128},
			expected{value: cloneBoundBytes(make([]byte, 129))}},
		{"binary limit", input{value: cloneBoundBytes(make([]byte, 129)), limit: 128, binaryLimit: 128},
			expected{errorText: "gent: binary value exceeds 128 bytes"}},
		{"explicit codec", input{value: cloneLargeRecord{}, limit: 128, custom: true},
			expected{value: cloneLargeRecord{}}},
		{"scalar methods", input{value: cloneJSONScalar("HELLO"), limit: 128},
			expected{value: cloneJSONScalar("hello")}},
		{"default bound", input{value: [][defaultExpandedBytes + 1]byte(nil)}, expected{
			errorText: "gent: decoded allocation of 1 [268435457]uint8 values " +
				"(268435457 bytes each) exceeds 268435456 bytes"}},
		{"default boundary", input{value: [][defaultExpandedBytes]byte(nil)},
			expected{value: [][defaultExpandedBytes]byte(nil)}},
	}
	for _, test := range cases {
		for _, path := range []string{"component", "clone default", "clone bounded"} {
			t.Run(test.name+"/"+path, func(t *testing.T) {
				limits := CodecLimits{MaxExpandedBytes: test.input.limit,
					MaxBinaryBytes: test.input.binaryLimit}
				if path == "clone bounded" {
					limits.MaxEncodedBytes = 1 << 20
				}
				codecs := NewCodecRegistry(WithCodecLimits(limits))
				if test.input.custom {
					assert.NoError(t, codecs.Register(cloneLargeRecord{}, cloneLargeCodec{}))
				} else {
					assert.NoError(t, codecs.RegisterType("app.large", 1, cloneLargeRecord{}))
				}
				for _, value := range []any{cloneSliceRecord{}, cloneNestedRecord{}, cloneMapRecord{},
					cloneBoundaryRecord{}, cloneEmptyElement{}, cloneBoundBytes{}, cloneJSONScalar("")} {
					assert.NoError(t, codecs.RegisterValueType(reflect.TypeOf(value)))
				}
				var value any
				var err error
				if path == "component" {
					var encoded EncodedValue
					encoded, err = codecs.Encode(test.input.value)
					if err == nil {
						value, err = codecs.Decode(encoded)
					} else {
						assert.Equal(t, EncodedValue{}, encoded)
					}
				} else {
					value, err = codecs.Clone(test.input.value)
				}
				actual := expected{value: value}
				if err != nil {
					actual.errorText = err.Error()
				}
				assert.Equal(t, test.expected, actual)
			})
		}
	}
}

func TestCloneBoundaryOwnership(t *testing.T) {
	type input struct {
		value  any
		change func(any)
	}
	type expected struct{ value any }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	values, wanted := map[int64]int64{}, map[int64]int64{}
	for i := range 16 {
		values[int64(i)] = int64(i)
		wanted[int64(i)] = int64(i)
	}
	cases := []testCase{
		{"slice", input{make([]int64, 16), func(value any) { value.([]int64)[0] = 7 }},
			expected{make([]int64, 16)}},
		{"map", input{values, func(value any) { value.(map[int64]int64)[0] = 7 }}, expected{wanted}},
		{"pointer", input{&[16]int64{}, func(value any) { value.(*[16]int64)[0] = 7 }},
			expected{&[16]int64{}}},
		{"record", input{cloneSliceRecord{make([]int64, 16)},
			func(value any) { value.(cloneSliceRecord).Values[0] = 7 }},
			expected{cloneSliceRecord{make([]int64, 16)}}},
	}
	for _, test := range cases {
		for _, path := range []string{"default", "bounded"} {
			t.Run(test.name+"/"+path, func(t *testing.T) {
				encodedLimit := 0
				if path == "bounded" {
					encodedLimit = 1 << 20
				}
				codecs := NewCodecRegistry(WithCodecLimits(CodecLimits{
					MaxExpandedBytes: 128, MaxEncodedBytes: encodedLimit}))
				assert.NoError(t, codecs.RegisterValueType(reflect.TypeFor[cloneSliceRecord]()))
				first, err := codecs.Clone(test.input.value)
				if !assert.NoError(t, err) {
					return
				}
				second, err := codecs.Clone(test.input.value)
				if !assert.NoError(t, err) {
					return
				}
				test.input.change(first)
				assert.Equal(t, test.expected, expected{test.input.value})
				assert.Equal(t, test.expected, expected{second})
			})
		}
	}
}
