package gent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

type codecConcreteError struct{ Message string }

func (problem codecConcreteError) Error() string     { return problem.Message }
func (problem codecConcreteError) ErrorCode() string { return "app:problem" }

type codecPointerError struct{ Message string }

func (problem *codecPointerError) Error() string { return problem.Message }

type codecErrorRecord struct{ Problem codecConcreteError }
type codecPointerErrorRecord struct{ Problem *codecPointerError }
type codecNestedErrorRecord struct{ Record codecErrorRecord }
type codecCodedErrorRecord struct{ Problem CodedError }
type codecErrorInterfaces struct {
	Problem error
	Value   any
}
type codecErrorSlice []codecConcreteError

// This fixture has the same JSON shape for each concrete type. Decode returns exactly T, including
// typed nil pointers. Registering the value type alone does not bind its pointer type.
type concreteErrorCodec[T any] struct{ name string }

func (codec concreteErrorCodec[T]) Type() string { return codec.name }
func (codec concreteErrorCodec[T]) Version() int { return 1 }
func (codec concreteErrorCodec[T]) Encode(value any) (json.RawMessage, error) {
	return json.Marshal(value.(T))
}
func (codec concreteErrorCodec[T]) Decode(data json.RawMessage) (any, error) {
	var value T
	err := json.Unmarshal(data, &value)
	return value, err
}

func errorValueCodecs(t *testing.T, compression CodecCompression, custom bool) *CodecRegistry {
	t.Helper()
	codecs := NewCodecRegistry(WithCodecCompression(compression))
	if custom {
		assert.NoError(t, codecs.Register(codecConcreteError{},
			concreteErrorCodec[codecConcreteError]{"app.error-value"}))
		assert.NoError(t, codecs.Register((*codecConcreteError)(nil),
			concreteErrorCodec[*codecConcreteError]{"app.error-value-pointer"}))
		assert.NoError(t, codecs.Register((*codecPointerError)(nil),
			concreteErrorCodec[*codecPointerError]{"app.error-pointer"}))
	} else {
		assert.NoError(t, codecs.RegisterType("app.error-value", 1, codecConcreteError{}))
		assert.NoError(t, codecs.RegisterType("app.error-value-pointer", 1, (*codecConcreteError)(nil)))
		assert.NoError(t, codecs.RegisterType("app.error-pointer", 1, (*codecPointerError)(nil)))
	}
	assert.NoError(t, codecs.RegisterType("app.pointer-value", 1, codecPointerError{}))
	assert.NoError(t, codecs.RegisterType("app.error-record", 1, codecErrorRecord{}))
	assert.NoError(t, codecs.RegisterType("app.pointer-record", 1, codecPointerErrorRecord{}))
	assert.NoError(t, codecs.RegisterType("app.nested-record", 1, codecNestedErrorRecord{}))
	assert.NoError(t, codecs.RegisterType("app.coded-record", 1, codecCodedErrorRecord{}))
	assert.NoError(t, codecs.RegisterType("app.error-interfaces", 1, codecErrorInterfaces{}))
	assert.NoError(t, codecs.RegisterType("app.error-slice", 1, codecErrorSlice{}))
	assert.NoError(t, codecs.RegisterType("app.saved-error", 1, SavedError{}))
	assert.NoError(t, codecs.RegisterType("app.nil-error-key", 1, codecNilErrorKey{}))
	assert.NoError(t, codecs.Register((*privateHistoryError)(nil), privateErrorCodec{}))
	return codecs
}

func errorDestinationMessage(source, destination reflect.Type) string {
	return fmt.Sprintf("gent: error value %s is saved as *gent.SavedError, "+
		"which is not assignable to %s; register a ValueCodec for the concrete error type",
		source, destination)
}

func TestConcreteErrorWriteBoundary(t *testing.T) {
	type input struct{ value any }
	type expected struct{ errorText string }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	problem := codecConcreteError{"saved problem"}
	pointer := &codecPointerError{"saved pointer problem"}
	valueType := reflect.TypeFor[codecConcreteError]()
	valueError := errorDestinationMessage(valueType, valueType)
	ptrType := reflect.TypeFor[*codecConcreteError]()
	ptrError := errorDestinationMessage(ptrType, ptrType)
	pointerType := reflect.TypeFor[*codecPointerError]()
	pointerError := errorDestinationMessage(pointerType, pointerType)
	problemPointer := &problem
	cases := []testCase{
		{"slice", input{[]codecConcreteError{problem}}, expected{valueError}},
		{"array", input{[1]codecConcreteError{problem}}, expected{valueError}},
		{"pointer slice", input{[]*codecConcreteError{&problem}}, expected{ptrError}},
		{"pointer receiver", input{[]*codecPointerError{pointer}}, expected{pointerError}},
		{"pointer target", input{&problemPointer}, expected{ptrError}},
		{"map value", input{map[string]codecConcreteError{"entry": problem}}, expected{valueError}},
		{"map key", input{map[codecConcreteError]string{problem: "entry"}}, expected{valueError}},
		{"pointer map key", input{map[*codecConcreteError]string{&problem: "entry"}},
			expected{ptrError}},
		{"field", input{codecErrorRecord{problem}},
			expected{"gent.codecErrorRecord.Problem: " + valueError}},
		{"pointer field", input{codecPointerErrorRecord{pointer}},
			expected{"gent.codecPointerErrorRecord.Problem: " + pointerError}},
		{"nested field", input{codecNestedErrorRecord{codecErrorRecord{problem}}}, expected{
			"gent.codecNestedErrorRecord.Record: gent.codecErrorRecord.Problem: " + valueError}},
		{"specialized interface", input{codecCodedErrorRecord{problem}}, expected{
			"gent.codecCodedErrorRecord.Problem: " +
				errorDestinationMessage(valueType, reflect.TypeFor[CodedError]())}},
		{"container in interface", input{[]any{[]codecConcreteError{problem}}}, expected{valueError}},
		{"named slice", input{codecErrorSlice{problem}}, expected{valueError}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Reflection registration describes the destination type. It does not opt out of
			// normal error snapshots. An explicit codec is needed to preserve that concrete type.
			checkErrorValuePaths(t, test.input.value, nil, test.expected.errorText, false)
			checkErrorValuePaths(t, test.input.value, test.input.value, "", true)
		})
	}
}

func TestErrorDestinationsThatRemainReadable(t *testing.T) {
	type input struct{ value any }
	type expected struct{ value any }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	problem := codecConcreteError{"saved problem"}
	saved := &SavedError{Message: problem.Message, Codes: []string{"app:problem"}}
	plain := errors.New("plain error")
	plainSaved := &SavedError{Message: "plain error"}
	causes := &SavedError{Message: "request failed", Codes: []string{"app:request"},
		Causes: []*SavedError{{Message: context.Canceled.Error(), Codes: []string{"canceled"}}}}
	private := &privateHistoryError{message: "private error", detail: []byte{0, 255, 7}}
	cases := []testCase{
		{"root value", input{problem}, expected{saved}},
		{"root pointer", input{&problem}, expected{saved}},
		{"error slice", input{[]error{problem, plain, nil}}, expected{[]error{saved, plainSaved, nil}}},
		{"any slice", input{[]any{problem, plain}}, expected{[]any{saved, plainSaved}}},
		{"interface fields", input{codecErrorInterfaces{problem, plain}},
			expected{codecErrorInterfaces{saved, plainSaved}}},
		{"interface map", input{map[string]error{"entry": problem}},
			expected{map[string]error{"entry": saved}}},
		{"nil slice", input{[]codecConcreteError(nil)}, expected{[]codecConcreteError(nil)}},
		{"empty slice", input{[]codecConcreteError{}}, expected{[]codecConcreteError{}}},
		{"empty array", input{[0]codecConcreteError{}}, expected{[0]codecConcreteError{}}},
		{"nil map", input{map[codecConcreteError]codecConcreteError(nil)},
			expected{map[codecConcreteError]codecConcreteError(nil)}},
		{"empty map", input{map[codecConcreteError]codecConcreteError{}},
			expected{map[codecConcreteError]codecConcreteError{}}},
		{"nil pointer element", input{[]*codecConcreteError{nil}},
			expected{[]*codecConcreteError{nil}}},
		{"nil pointer field", input{codecPointerErrorRecord{}}, expected{codecPointerErrorRecord{}}},
		{"nil pointer key", input{map[*codecConcreteError]string{nil: "entry"}},
			expected{map[*codecConcreteError]string{nil: "entry"}}},
		{"pointer receiver value", input{[]codecPointerError{{Message: "field value"}}},
			expected{[]codecPointerError{{Message: "field value"}}}},
		{"saved codes and causes", input{[]*SavedError{causes}}, expected{[]*SavedError{causes}}},
		{"private custom codec", input{[]*privateHistoryError{private}},
			expected{[]*privateHistoryError{private}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			checkErrorValuePaths(t, test.input.value, test.expected.value, "", false)
		})
	}
}

func checkErrorValuePaths(t *testing.T, value, want any, errorText string, custom bool) {
	t.Helper()
	for _, path := range []string{"component", "plain", "compressed", "clone"} {
		t.Run(fmt.Sprintf("%s/custom=%v", path, custom), func(t *testing.T) {
			compression := CodecCompressionNone
			if path == "compressed" {
				compression = CodecCompressionSnappy
			}
			codecs := errorValueCodecs(t, compression, custom)
			var actual any
			var err error
			switch path {
			case "clone":
				actual, err = codecs.Clone(value)
			case "component":
				var encoded EncodedValue
				encoded, err = codecs.Encode(value)
				if err == nil {
					actual, err = codecs.Decode(encoded)
				} else {
					assert.Equal(t, EncodedValue{}, encoded)
				}
			default:
				var data []byte
				data, err = codecs.Marshal(value)
				if err == nil {
					err = codecs.Unmarshal(data, &actual)
				} else {
					assert.Nil(t, data)
				}
			}
			if errorText != "" {
				assert.EqualError(t, err, errorText)
				assert.Nil(t, actual)
				return
			}
			if !assert.NoError(t, err) {
				return
			}
			if reflect.ValueOf(want).Kind() == reflect.Map &&
				reflect.TypeOf(want).Key().Kind() == reflect.Pointer {
				assertIdentityValue(t, codecs, want, actual)
			} else {
				assert.Equal(t, want, actual)
			}
		})
	}
}
