package gent

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

var errSavedScalar = errors.New("saved scalar rejected")

type savedJSONString string

func (value savedJSONString) MarshalJSON() ([]byte, error) {
	return json.Marshal("json:" + string(value))
}

func (value *savedJSONString) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	text, valid := strings.CutPrefix(text, "json:")
	*value = savedJSONString(text)
	if !valid || text == "reject" {
		return errSavedScalar
	}
	return nil
}

// JSON decoding must take precedence when both decode interfaces are present.
func (*savedJSONString) UnmarshalText([]byte) error {
	return errors.New("text decoder must not replace JSON decoding")
}

type savedTextString string

func (value savedTextString) MarshalText() ([]byte, error) {
	return []byte("text:" + string(value)), nil
}

func (value *savedTextString) UnmarshalText(data []byte) error {
	text, valid := strings.CutPrefix(string(data), "text:")
	*value = savedTextString(text)
	if !valid || text == "reject" {
		return errSavedScalar
	}
	return nil
}

type savedScalarRecord struct {
	Code    savedJSONString
	Label   savedTextString
	Padding string
}

type savedObjectString string

func (value savedObjectString) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"code": string(value)})
}

func (value *savedObjectString) UnmarshalJSON(data []byte) error {
	var fields map[string]string
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*value = savedObjectString(fields["code"])
	return nil
}

func TestCodecNamedStringMethods(t *testing.T) {
	type input struct{ value any }
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
	type plainString string
	record := savedScalarRecord{Code: "accepted", Label: "order",
		Padding: strings.Repeat("saved business data ", 1024)}
	cases := []testCase{
		{"JSON", input{savedJSONString("accepted")}, expected{value: savedJSONString("accepted")}},
		{"text", input{savedTextString("order")}, expected{value: savedTextString("order")}},
		{"JSON object", input{savedObjectString("accepted")},
			expected{value: savedObjectString("accepted")}},
		{"plain named", input{plainString("original")}, expected{value: plainString("original")}},
		{"number", input{json.Number("18446744073709551615")},
			expected{value: json.Number("18446744073709551615")}},
		{"custom record", input{record}, expected{value: record}},
		{"JSON rejects", input{savedJSONString("reject")},
			expected{errorText: "saved scalar rejected", cause: errSavedScalar}},
		{"text rejects", input{savedTextString("reject")},
			expected{errorText: "saved scalar rejected", cause: errSavedScalar}},
		{"record rejects", input{savedScalarRecord{Code: "reject", Padding: record.Padding}},
			expected{errorText: "github.com/rickchristie/gent/savedScalarRecord.Code: saved scalar rejected",
				cause: errSavedScalar}},
	}
	for _, test := range cases {
		for _, path := range []string{"component", "plain", "compressed", "clone", "bounded clone"} {
			t.Run(test.name+"/"+path, func(t *testing.T) {
				compression := CodecCompressionSnappy
				if path == "plain" {
					compression = CodecCompressionNone
				}
				limit := 0
				if path == "bounded clone" {
					limit = 1 << 20
				}
				codecs := NewCodecRegistry(WithCodecCompression(compression),
					WithCodecLimits(CodecLimits{MaxEncodedBytes: limit}))
				if !assert.NoError(t, codecs.RegisterValueType(reflect.TypeOf(test.input.value))) {
					return
				}
				value := test.input.value
				// Repeated saves must not apply an application's wire prefix to its state again.
				for range 2 {
					var result any
					var err error
					if path == "clone" || path == "bounded clone" {
						result, err = codecs.Clone(value)
					} else if path == "component" {
						encoded, encodeErr := codecs.Encode(value)
						if !assert.NoError(t, encodeErr) {
							return
						}
						result, err = codecs.Decode(encoded)
					} else {
						data, encodeErr := codecs.Marshal(value)
						if !assert.NoError(t, encodeErr) {
							return
						}
						result = "unchanged target"
						err = codecs.Unmarshal(data, &result)
						if err != nil {
							assert.Equal(t, "unchanged target", result)
							result = nil
						}
					}
					actual := expected{value: result}
					if err != nil {
						actual.errorText = err.Error()
						if assert.ErrorIs(t, err, test.expected.cause) {
							actual.cause = test.expected.cause
						}
					}
					if !assert.Equal(t, test.expected, actual) || err != nil {
						return
					}
					value = result
				}
			})
		}
	}
}
