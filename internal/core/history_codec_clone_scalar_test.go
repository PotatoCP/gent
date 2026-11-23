package gent

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

var errCloneScalar = errors.New("scalar validation failed")

type cloneJSONScalar string

func (value cloneJSONScalar) MarshalJSON() ([]byte, error) {
	if value == "encode error" {
		return nil, errCloneScalar
	}
	return json.Marshal(strings.ToLower(string(value)))
}
func (value *cloneJSONScalar) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	if text == "decode error" {
		return errCloneScalar
	}
	*value = cloneJSONScalar(strings.ToLower(text))
	return nil
}
func (cloneJSONScalar) MarshalText() ([]byte, error) { return nil, errCloneScalar }
func (*cloneJSONScalar) UnmarshalText([]byte) error  { return errCloneScalar }

type cloneTextScalar string

func (value cloneTextScalar) MarshalText() ([]byte, error) {
	return []byte(strings.ToLower(string(value))), nil
}
func (value *cloneTextScalar) UnmarshalText(data []byte) error {
	*value = cloneTextScalar(strings.ToLower(string(data)))
	return nil
}

type clonePointerMarshal string

func (*clonePointerMarshal) MarshalJSON() ([]byte, error) { return nil, errCloneScalar }

type cloneDecodeInt int

func (value *cloneDecodeInt) UnmarshalJSON(data []byte) error {
	var number int
	if err := json.Unmarshal(data, &number); err != nil {
		return err
	}
	*value = 7
	return nil
}

type cloneScalarRecord struct{ Value cloneJSONScalar }

// Each type exercises a distinct immutable kind in the direct-copy switch. The value method
// changes the portable value, so copying the original scalar would be incorrect.
type cloneBool bool

func (cloneBool) MarshalJSON() ([]byte, error) { return []byte(`false`), nil }

type cloneInt int

func (cloneInt) MarshalJSON() ([]byte, error) { return []byte(`7`), nil }

type cloneInt8 int8

func (cloneInt8) MarshalJSON() ([]byte, error) { return []byte(`7`), nil }

type cloneInt16 int16

func (cloneInt16) MarshalJSON() ([]byte, error) { return []byte(`7`), nil }

type cloneInt32 int32

func (cloneInt32) MarshalJSON() ([]byte, error) { return []byte(`7`), nil }

type cloneInt64 int64

func (cloneInt64) MarshalJSON() ([]byte, error) { return []byte(`7`), nil }

type cloneUint uint

func (cloneUint) MarshalJSON() ([]byte, error) { return []byte(`7`), nil }

type cloneUint8 uint8

func (cloneUint8) MarshalJSON() ([]byte, error) { return []byte(`7`), nil }

type cloneUint16 uint16

func (cloneUint16) MarshalJSON() ([]byte, error) { return []byte(`7`), nil }

type cloneUint32 uint32

func (cloneUint32) MarshalJSON() ([]byte, error) { return []byte(`7`), nil }

type cloneUint64 uint64

func (cloneUint64) MarshalJSON() ([]byte, error) { return []byte(`7`), nil }

type cloneFloat32 float32

func (cloneFloat32) MarshalJSON() ([]byte, error) { return []byte(`7`), nil }

type cloneFloat64 float64

func (cloneFloat64) MarshalJSON() ([]byte, error) { return []byte(`7`), nil }

type fixedScalarCodec struct{}

func (fixedScalarCodec) Type() string { return "app.fixed-scalar" }
func (fixedScalarCodec) Version() int { return 1 }
func (fixedScalarCodec) Encode(any) (json.RawMessage, error) {
	return json.RawMessage(`"custom"`), nil
}
func (fixedScalarCodec) Decode(json.RawMessage) (any, error) {
	return cloneJSONScalar("custom"), nil
}

func TestCloneScalarMethods(t *testing.T) {
	type input struct {
		value  any
		custom bool
	}
	type expected struct {
		value     any
		errorText string
		cause     error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	pointer := clonePointerMarshal("UNCHANGED")
	cases := []testCase{
		{"JSON and text precedence", input{value: cloneJSONScalar("HELLO")},
			expected{value: cloneJSONScalar("hello")}},
		{"text", input{value: cloneTextScalar("HELLO")}, expected{value: cloneTextScalar("hello")}},
		{"pointer-only marshal", input{value: &pointer}, expected{value: &pointer}},
		{"pointer-only decode", input{value: cloneDecodeInt(42)}, expected{value: cloneDecodeInt(7)}},
		{"record", input{value: cloneScalarRecord{"HELLO"}}, expected{value: cloneScalarRecord{"hello"}}},
		{"slice", input{value: []cloneJSONScalar{"HELLO"}},
			expected{value: []cloneJSONScalar{"hello"}}},
		{"array", input{value: [1]cloneJSONScalar{"HELLO"}},
			expected{value: [1]cloneJSONScalar{"hello"}}},
		{"map value", input{value: map[string]cloneJSONScalar{"entry": "HELLO"}},
			expected{value: map[string]cloneJSONScalar{"entry": "hello"}}},
		{"custom codec", input{value: cloneJSONScalar("encode error"), custom: true},
			expected{value: cloneJSONScalar("custom")}},
		{"encode error", input{value: cloneJSONScalar("encode error")}, expected{
			errorText: "json: error calling MarshalJSON for type gent.cloneJSONScalar: " +
				"scalar validation failed",
			cause: errCloneScalar}},
		{"decode error", input{value: cloneJSONScalar("decode error")},
			expected{errorText: "scalar validation failed", cause: errCloneScalar}},
		{"field decode error", input{value: cloneScalarRecord{"decode error"}}, expected{
			errorText: "github.com/rickchristie/gent/cloneScalarRecord.Value: scalar validation failed",
			cause:     errCloneScalar}},
		{"bool", input{value: cloneBool(true)}, expected{value: cloneBool(false)}},
		{"int", input{value: cloneInt(42)}, expected{value: cloneInt(7)}},
		{"int8", input{value: cloneInt8(42)}, expected{value: cloneInt8(7)}},
		{"int16", input{value: cloneInt16(42)}, expected{value: cloneInt16(7)}},
		{"int32", input{value: cloneInt32(42)}, expected{value: cloneInt32(7)}},
		{"int64", input{value: cloneInt64(42)}, expected{value: cloneInt64(7)}},
		{"uint", input{value: cloneUint(42)}, expected{value: cloneUint(7)}},
		{"uint8", input{value: cloneUint8(42)}, expected{value: cloneUint8(7)}},
		{"uint16", input{value: cloneUint16(42)}, expected{value: cloneUint16(7)}},
		{"uint32", input{value: cloneUint32(42)}, expected{value: cloneUint32(7)}},
		{"uint64", input{value: cloneUint64(42)}, expected{value: cloneUint64(7)}},
		{"float32", input{value: cloneFloat32(42)}, expected{value: cloneFloat32(7)}},
		{"float64", input{value: cloneFloat64(42)}, expected{value: cloneFloat64(7)}},
	}
	for _, test := range cases {
		for _, path := range []string{"component", "clone", "bounded clone"} {
			t.Run(test.name+"/"+path, func(t *testing.T) {
				limit := 0
				if path == "bounded clone" {
					limit = 1 << 20
				}
				codecs := NewCodecRegistry(WithCodecLimits(CodecLimits{MaxEncodedBytes: limit}))
				if test.input.custom {
					assert.NoError(t, codecs.Register(cloneJSONScalar(""), fixedScalarCodec{}))
				} else {
					assert.NoError(t, codecs.RegisterValueType(reflect.TypeOf(test.input.value)))
				}
				var value any
				var err error
				if path == "component" {
					var encoded EncodedValue
					encoded, err = codecs.Encode(test.input.value)
					if err == nil {
						value, err = codecs.Decode(encoded)
					}
				} else {
					value, err = codecs.Clone(test.input.value)
				}
				actual := expected{value: value}
				if err != nil {
					actual.errorText = err.Error()
					if assert.ErrorIs(t, err, test.expected.cause) {
						actual.cause = test.expected.cause
					}
				}
				assert.Equal(t, test.expected, actual)
			})
		}
	}
}

func TestCloneScalarContainerOwnership(t *testing.T) {
	type input struct{ limit int }
	type expected struct{ value []cloneJSONScalar }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"default", input{}, expected{[]cloneJSONScalar{"hello"}}},
		{"bounded", input{1 << 20}, expected{[]cloneJSONScalar{"hello"}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := NewCodecRegistry(WithCodecLimits(CodecLimits{MaxEncodedBytes: test.input.limit}))
			assert.NoError(t, codecs.RegisterValueType(reflect.TypeFor[cloneJSONScalar]()))
			original := []cloneJSONScalar{"HELLO"}
			first, err := codecs.Clone(original)
			if !assert.NoError(t, err) {
				return
			}
			second, err := codecs.Clone(original)
			assert.NoError(t, err)
			assert.Equal(t, test.expected.value, first)
			first.([]cloneJSONScalar)[0] = "changed"
			assert.Equal(t, []cloneJSONScalar{"HELLO"}, original)
			assert.Equal(t, test.expected.value, second)
		})
	}
}
