package gent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

type validationValue struct{}
type validationCodec struct{ calls int }

func (*validationCodec) Type() string { return "test:json-validation" }
func (*validationCodec) Version() int { return 1 }
func (*validationCodec) Encode(any) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
func (codec *validationCodec) Decode(json.RawMessage) (any, error) {
	codec.calls++
	return validationValue{}, nil
}

func TestCodecValidatesNestedJSONBeforeCustomDecode(t *testing.T) {
	type input struct{ data string }
	type expected struct {
		value     any
		calls     int
		errorText string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"valid", input{`{"element":"any","values":[
{"type":"test:json-validation","version":1,"data":{}}
]}`}, expected{value: []any{validationValue{}}, calls: 1}},
		{"absent nested data", input{`{"element":"any","values":[
{"type":"test:json-validation","version":1}
]}`}, expected{errorText: "gent: invalid encoded JSON"}},
		{"absent data and unknown type", input{`{"element":"any","values":[
{"type":"unknown-type","version":1}
]}`}, expected{errorText: "gent: invalid encoded JSON"}},
		{"invalid later JSON", input{`{"element":"any","values":[
{"type":"test:json-validation","version":1,"data":{}},
{"type":"test:json-validation","version":1,"data":
]}`}, expected{errorText: "gent: invalid encoded JSON"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs, codec := NewCodecRegistry(), &validationCodec{}
			if !assert.NoError(t, codecs.Register(validationValue{}, codec)) {
				return
			}
			value, err := codecs.Decode(EncodedValue{Type: "slice", Version: 1,
				Data: json.RawMessage(test.input.data)})
			message := ""
			if err != nil {
				message = err.Error()
			}
			assert.Equal(t, test.expected, expected{value, codec.calls, message})
		})
	}
}
