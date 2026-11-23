package gent

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

type checkedErrorCodec struct {
	concreteErrorCodec[codecConcreteError]
	failure     error
	failEncode  bool
	decodeCalls int
}

func (codec *checkedErrorCodec) Encode(value any) (json.RawMessage, error) {
	if codec.failEncode {
		return nil, codec.failure
	}
	return codec.concreteErrorCodec.Encode(value)
}

func (codec *checkedErrorCodec) Decode(json.RawMessage) (any, error) {
	codec.decodeCalls++
	return nil, codec.failure
}

func TestConcreteErrorCodecCallbacks(t *testing.T) {
	type input struct{ failEncode bool }
	type expected struct {
		errorText   string
		decodeCalls int
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"encode failure", input{true}, expected{"encode app.checked-error: codec failed", 0}},
		{"decode failure", input{}, expected{`gent: decode codec "app.checked-error": codec failed`, 1}},
	}
	for _, test := range cases {
		for _, path := range []string{"component", "storage", "clone"} {
			t.Run(test.name+"/"+path, func(t *testing.T) {
				failure := errors.New("codec failed")
				codec := &checkedErrorCodec{concreteErrorCodec[codecConcreteError]{"app.checked-error"},
					failure, test.input.failEncode, 0}
				codecs := NewCodecRegistry()
				assert.NoError(t, codecs.Register(codecConcreteError{}, codec))
				value := []codecConcreteError{{Message: "use the application codec"}}
				var err error
				switch path {
				case "component":
					var encoded EncodedValue
					encoded, err = codecs.Encode(value)
					assert.Equal(t, 0, codec.decodeCalls)
					if err == nil {
						var actual any
						actual, err = codecs.Decode(encoded)
						assert.Nil(t, actual)
					} else {
						assert.Equal(t, EncodedValue{}, encoded)
					}
				case "storage":
					var data []byte
					data, err = codecs.Marshal(value)
					assert.Equal(t, 0, codec.decodeCalls)
					if err == nil {
						var actual any = "unchanged target"
						err = codecs.Unmarshal(data, &actual)
						assert.Equal(t, "unchanged target", actual)
					} else {
						assert.Nil(t, data)
					}
				case "clone":
					var actual any
					actual, err = codecs.Clone(value)
					assert.Nil(t, actual)
				}
				assert.EqualError(t, err, test.expected.errorText)
				assert.ErrorIs(t, err, failure)
				assert.Equal(t, test.expected.decodeCalls, codec.decodeCalls)
			})
		}
	}
}

func TestErrorValuesOwnTheirDecodedState(t *testing.T) {
	for _, path := range []string{"component", "storage", "clone"} {
		t.Run(path, func(t *testing.T) {
			codecs := errorValueCodecs(t, CodecCompressionSnappy, false)
			value := []error{
				&SavedError{Message: "saved error", Codes: []string{"app:error"},
					Causes: []*SavedError{{Message: "saved cause"}}},
				&privateHistoryError{message: "private error", detail: []byte{0, 255, 7}},
			}
			var copyValue func() (any, error)
			switch path {
			case "component":
				encoded, err := codecs.Encode(value)
				if !assert.NoError(t, err) {
					return
				}
				copyValue = func() (any, error) { return codecs.Decode(encoded) }
			case "storage":
				data, err := codecs.Marshal(value)
				if !assert.NoError(t, err) {
					return
				}
				copyValue = func() (any, error) {
					var actual any
					err := codecs.Unmarshal(data, &actual)
					return actual, err
				}
			case "clone":
				copyValue = func() (any, error) { return codecs.Clone(value) }
			}
			first, err := copyValue()
			if !assert.NoError(t, err) {
				return
			}
			second, err := copyValue()
			assert.NoError(t, err)
			assert.Equal(t, value, first)
			assert.Equal(t, value, second)
			items := first.([]error)
			items[0].(*SavedError).Codes[0] = "changed code"
			items[0].(*SavedError).Causes[0].Message = "changed cause"
			items[1].(*privateHistoryError).detail[0] = 99
			want := []error{
				&SavedError{Message: "saved error", Codes: []string{"app:error"},
					Causes: []*SavedError{{Message: "saved cause"}}},
				&privateHistoryError{message: "private error", detail: []byte{0, 255, 7}},
			}
			assert.Equal(t, want, value)
			assert.Equal(t, want, second)
		})
	}
}
