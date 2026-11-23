package gent

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

type identityStringRecord struct{ Text string }
type identityString string
type identityJSONString string
type identityTextString string

func (value identityJSONString) MarshalJSON() ([]byte, error) {
	return json.Marshal([]byte(value))
}

func (value *identityJSONString) UnmarshalJSON(data []byte) error {
	var raw []byte
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*value = identityJSONString(raw)
	return nil
}

func (value identityTextString) MarshalText() ([]byte, error) {
	return []byte(base64.StdEncoding.EncodeToString([]byte(value))), nil
}

func (value *identityTextString) UnmarshalText(data []byte) error {
	raw, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return err
	}
	*value = identityTextString(raw)
	return nil
}

type identityStringCodec struct{}

func (identityStringCodec) Type() string { return "app.binary-string" }
func (identityStringCodec) Version() int { return 1 }
func (identityStringCodec) Encode(value any) (json.RawMessage, error) {
	return json.Marshal([]byte(value.(identityString)))
}
func (identityStringCodec) Decode(data json.RawMessage) (any, error) {
	var raw []byte
	err := json.Unmarshal(data, &raw)
	return identityString(raw), err
}

func TestStringValidationBeforeEncoding(t *testing.T) {
	type input struct{ value any }
	type expected struct {
		writeError string
		cloneError string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	invalid := string([]byte{255})
	message := "gent: invalid UTF-8 in string value of type string"
	streamError := "encode github.com/rickchristie/gent/StreamChunk: " +
		"gent: invalid UTF-8 in stream source message ID"
	cases := []testCase{
		{"short", input{invalid}, expected{message, message}},
		{"cached long", input{[]string{strings.Repeat("a", 300) + invalid}},
			expected{message, message}},
		{"truncated", input{string([]byte{0xe2, 0x82})}, expected{message, message}},
		{"nested field", input{identityStringRecord{invalid}},
			expected{"gent.identityStringRecord.Text: " + message, message}},
		{"one key", input{map[string]int{invalid: 1}}, expected{message, message}},
		{"two keys", input{map[string]int{invalid: 1, string([]byte{254}): 2}},
			expected{message, message}},
		{"replacement collision", input{map[string]int{invalid: 1, "�": 2}},
			expected{message, message}},
		{"named string", input{identityString(invalid)}, expected{
			"gent: invalid UTF-8 in string value of type gent.identityString",
			"gent: invalid UTF-8 in string value of type gent.identityString"}},
		{"error message", input{errors.New(invalid)}, expected{
			"gent: invalid UTF-8 in saved error message", "gent: invalid UTF-8 in saved error message"}},
		{"error code", input{&SavedError{Message: "failure", Codes: []string{invalid}}}, expected{
			"gent: invalid UTF-8 in saved error code", "gent: invalid UTF-8 in saved error code"}},
		{"error cause", input{&SavedError{Message: "failure", Causes: []*SavedError{
			{Message: invalid}}}}, expected{
			"gent: invalid UTF-8 in saved error message", "gent: invalid UTF-8 in saved error message"}},
		{"component type", input{EncodedValue{Type: invalid, Version: 1}}, expected{
			"gent: invalid UTF-8 in encoded value type", "gent: invalid UTF-8 in encoded value type"}},
		{"declared type", input{EncodedValue{Type: "app.value", DeclaredType: invalid, Version: 1}},
			expected{"gent: invalid UTF-8 in encoded value declared type",
				"gent: invalid UTF-8 in encoded value declared type"}},
		{"stream IDs", input{StreamChunk{SourceMessageIds: []string{invalid}}},
			expected{streamError, streamError}},
	}
	for _, test := range cases {
		for _, compression := range []CodecCompression{CodecCompressionNone, CodecCompressionSnappy} {
			t.Run(test.name+"/"+string(compression), func(t *testing.T) {
				codecs := identityCodecs(t, compression)
				component, err := codecs.Encode(test.input.value)
				assert.EqualError(t, err, test.expected.writeError)
				assert.Equal(t, EncodedValue{}, component)
				data, err := codecs.Marshal(test.input.value)
				assert.EqualError(t, err, test.expected.writeError)
				assert.Nil(t, data)
				cloned, err := codecs.Clone(test.input.value)
				assert.EqualError(t, err, test.expected.cloneError)
				assert.Nil(t, cloned)
			})
		}
	}
}

func TestStringValidationKeepsUnicodeAndApplicationMethods(t *testing.T) {
	type input struct {
		value  any
		custom bool
	}
	type expected struct{ value any }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	invalid := string([]byte{255, 254})
	long := strings.Repeat("保管 � ", 100)
	cases := []testCase{
		{"replacement", input{value: "�"}, expected{"�"}},
		{"long repeated Unicode", input{value: []string{long, long}}, expected{[]string{long, long}}},
		{"valid map keys", input{value: map[string]int{"保管": 1, "�": 2}},
			expected{map[string]int{"保管": 1, "�": 2}}},
		{"valid source IDs", input{value: StreamChunk{SourceMessageIds: []string{"保管", "�"}}},
			expected{StreamChunk{SourceMessageIds: []string{"保管", "�"}}}},
		{"valid error text", input{value: &SavedError{Message: "保管", Codes: []string{"�"}}},
			expected{&SavedError{Message: "保管", Codes: []string{"�"}}}},
		{"valid component names", input{value: EncodedValue{
			Type: "app.�", DeclaredType: "app.保管", Version: 1, Data: json.RawMessage(`1`)}},
			expected{EncodedValue{Type: "app.�", DeclaredType: "app.保管", Version: 1,
				Data: json.RawMessage(`1`)}}},
		{"JSON method", input{value: identityJSONString(invalid)}, expected{identityJSONString(invalid)}},
		{"text method", input{value: identityTextString(invalid)}, expected{identityTextString(invalid)}},
		{"custom codec", input{identityString(invalid), true}, expected{identityString(invalid)}},
	}
	for _, test := range cases {
		for _, compression := range []CodecCompression{CodecCompressionNone, CodecCompressionSnappy} {
			t.Run(test.name+"/"+string(compression), func(t *testing.T) {
				codecs := identityCodecs(t, compression)
				if test.input.custom {
					codecs = NewCodecRegistry(WithCodecCompression(compression))
					assert.NoError(t, codecs.Register(identityString(""), identityStringCodec{}))
				}
				assertRegistrationRoundTrip(t, codecs, test.input.value)
				value, err := codecs.Clone(test.input.value)
				assert.NoError(t, err)
				assert.Equal(t, test.expected, expected{value})
			})
		}
	}
}
